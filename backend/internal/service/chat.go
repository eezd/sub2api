package service

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	_ "golang.org/x/image/webp"
)

const (
	ChatFileLimit    int64 = 20 << 20
	ChatStorageLimit int64 = 100_000_000
	ChatTextLimit          = 2_000_000
	ChatRequestLimit int64 = 100_000_000
)

var (
	ErrChatNotFound = infraerrors.NotFound("CHAT_NOT_FOUND", "会话、消息或附件不存在")
	ErrChatBusy     = infraerrors.Conflict("CHAT_BUSY", "会话正在生成或删除，请稍后重试")
	ErrChatStorage  = infraerrors.BadRequest("CHAT_STORAGE_LIMIT", "附件原文件与提取文本合计不能超过 100 MB，请先删除部分会话或附件")
	ErrChatHistory  = infraerrors.Conflict("CHAT_HISTORY_CHANGED", "会话历史已在其他窗口更新，请查看最新历史后重新发送")
)

type ChatConversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	APIKeyID  *int64    `json:"api_key_id"`
	Model     string    `json:"model"`
	Deleting  bool      `json:"deleting"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChatAttachment struct {
	ID             string  `json:"id"`
	ConversationID string  `json:"conversation_id"`
	MessageID      *string `json:"message_id"`
	Filename       string  `json:"filename"`
	MimeType       string  `json:"mime_type"`
	Size           int64   `json:"size"`
	ExtractedText  string  `json:"extracted_text"`
}

type ChatMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Reasoning string    `json:"reasoning"`
	Model     string    `json:"model"`
	APIKeyID  *int64    `json:"api_key_id"`
	Status    string    `json:"status"`
	Revision  int64     `json:"revision"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChatDetail struct {
	Conversation ChatConversation `json:"conversation"`
	Messages     []ChatMessage    `json:"messages"`
	Attachments  []ChatAttachment `json:"attachments"`
}

type ChatTurnInput struct {
	RequestID        string    `json:"request_id"`
	APIKeyID         int64     `json:"api_key_id"`
	Model            string    `json:"model"`
	Content          string    `json:"content"`
	AttachmentIDs    []string  `json:"attachment_ids"`
	HistoryUpdatedAt time.Time `json:"history_updated_at"`
}

type ChatTurn struct {
	UserMessageID      string `json:"user_message_id"`
	AssistantMessageID string `json:"assistant_message_id"`
	Existing           bool   `json:"existing"`
}

type ChatSnapshot struct {
	Content   string `json:"content"`
	Reasoning string `json:"reasoning"`
	Status    string `json:"status"`
	Revision  int64  `json:"revision"`
	Error     string `json:"error"`
}

// ChatRepository 的所有方法都以登录用户归属为查询条件。
type ChatRepository interface {
	List(context.Context, int64, int) ([]ChatConversation, error)
	Create(context.Context, int64, string) (ChatConversation, error)
	Get(context.Context, int64, string) (ChatDetail, error)
	Rename(context.Context, int64, string, string) error
	MarkDeleting(context.Context, int64, string) error
	Delete(context.Context, int64, string) error
	AddAttachment(context.Context, int64, ChatAttachment, func() error) error
	Attachment(context.Context, int64, string) (ChatAttachment, error)
	DeleteAttachment(context.Context, int64, string, func(ChatAttachment) error) error
	StorageUsed(context.Context, int64) (int64, error)
	StartTurn(context.Context, int64, string, ChatTurnInput) (ChatTurn, error)
	SaveReply(context.Context, int64, string, string, ChatSnapshot) error
}

type ChatService struct {
	repo         ChatRepository
	dir          string
	requestLimit int64
}

func NewChatService(repo ChatRepository, cfg *config.Config) *ChatService {
	dir := strings.TrimSpace(cfg.Chat.AttachmentDir)
	if dir == "" {
		base := strings.TrimSpace(os.Getenv("DATA_DIR"))
		if base == "" {
			base = "./data"
		}
		dir = filepath.Join(base, "chat")
	}
	limit := int64(95_000_000)
	for _, configured := range []int64{cfg.Gateway.MaxBodySize, cfg.Server.MaxRequestBodySize} {
		if configured > 0 && configured < limit {
			limit = configured
		}
	}
	return &ChatService{repo: repo, dir: dir, requestLimit: limit}
}

func (s *ChatService) List(ctx context.Context, uid int64, page int) ([]ChatConversation, error) {
	return s.repo.List(ctx, uid, page)
}
func (s *ChatService) Create(ctx context.Context, uid int64) (ChatConversation, error) {
	return s.repo.Create(ctx, uid, uuid.NewString())
}
func (s *ChatService) Get(ctx context.Context, uid int64, id string) (ChatDetail, error) {
	return s.repo.Get(ctx, uid, id)
}
func (s *ChatService) Rename(ctx context.Context, uid int64, id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 100 || !chatTextValid(title) {
		return infraerrors.BadRequest("CHAT_TITLE", "标题必须为 1 至 100 个字符")
	}
	return s.repo.Rename(ctx, uid, id, title)
}

func (s *ChatService) path(a ChatAttachment) string {
	return filepath.Join(s.dir, a.ConversationID, a.ID)
}

func (s *ChatService) Delete(ctx context.Context, uid int64, id string) error {
	err := s.repo.MarkDeleting(ctx, uid, id)
	if err != nil {
		return err
	}
	// 删除范围仅为已锁定并标记删除的用户会话；同时回收异常退出留下的未登记文件。
	if err := os.RemoveAll(filepath.Join(s.dir, id)); err != nil {
		return fmt.Errorf("清理聊天附件失败: %w", err)
	}
	return s.repo.Delete(ctx, uid, id)
}

func (s *ChatService) Upload(ctx context.Context, uid int64, cid, name, extracted string, r io.Reader) (ChatAttachment, error) {
	if len(extracted) > ChatTextLimit || !chatTextValid(extracted) {
		return ChatAttachment{}, infraerrors.BadRequest("CHAT_TEXT_LIMIT", "提取文本必须为有效 UTF-8，且不超过 2 MB")
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "" || len(name) > 255 {
		return ChatAttachment{}, infraerrors.BadRequest("CHAT_FILENAME", "附件名称无效或过长")
	}
	data, err := io.ReadAll(io.LimitReader(r, ChatFileLimit+1))
	if err != nil {
		return ChatAttachment{}, fmt.Errorf("读取附件失败: %w", err)
	}
	if len(data) == 0 || int64(len(data)) > ChatFileLimit {
		return ChatAttachment{}, infraerrors.BadRequest("CHAT_FILE_LIMIT", "单个附件必须非空且不超过 20 MiB")
	}
	mimeType, text, err := chatAttachmentContent(name, data, extracted)
	if err != nil {
		return ChatAttachment{}, err
	}
	a := ChatAttachment{ID: uuid.NewString(), ConversationID: cid, Filename: name, MimeType: mimeType, Size: int64(len(data)), ExtractedText: text}
	err = s.repo.AddAttachment(ctx, uid, a, func() error {
		if err := os.MkdirAll(filepath.Join(s.dir, cid), 0700); err != nil {
			return fmt.Errorf("创建附件目录失败: %w", err)
		}
		if err := os.WriteFile(s.path(a), data, 0600); err != nil {
			return fmt.Errorf("保存附件失败: %w", err)
		}
		return nil
	})
	if err != nil {
		if removeErr := os.Remove(s.path(a)); removeErr != nil && !os.IsNotExist(removeErr) {
			return a, fmt.Errorf("登记附件失败: %w；清理附件失败: %v", err, removeErr)
		}
		return a, err
	}
	return a, nil
}

func chatAttachmentContent(name string, data []byte, extracted string) (string, string, error) {
	bad := func(message string) (string, string, error) {
		return "", "", infraerrors.BadRequest("CHAT_FILE_TYPE", message)
	}
	actual := http.DetectContentType(data)
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".webp":
		if actual != "image/png" && actual != "image/jpeg" && actual != "image/webp" {
			return bad("图片内容与支持的格式不符")
		}
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return bad("无法读取图片")
		}
		return actual, "", nil
	case ".pdf":
		if actual != "application/pdf" || strings.TrimSpace(extracted) == "" {
			return bad("PDF 无有效提取文字；扫描件请改传图片")
		}
		return "application/pdf", extracted, nil
	default:
		if !chatTextExtension(strings.ToLower(filepath.Ext(name))) {
			return bad("仅支持 PNG、JPEG、WebP、PDF 和 UTF-8 文本或代码文件")
		}
		if len(data) > ChatTextLimit || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
			return bad("文本文件必须为 UTF-8 且不超过 2 MB")
		}
		return mime.FormatMediaType("text/plain", map[string]string{"charset": "utf-8"}), string(data), nil
	}
}

func chatTextExtension(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".json", ".jsonl", ".yaml", ".yml", ".toml", ".xml", ".html", ".css", ".js", ".ts", ".jsx", ".tsx", ".vue", ".py", ".go", ".rs", ".java", ".c", ".h", ".cpp", ".hpp", ".sh", ".sql", ".log", ".ini", ".conf", ".rb", ".php", ".swift", ".kt":
		return true
	default:
		return false
	}
}

func (s *ChatService) OpenAttachment(ctx context.Context, uid int64, id string) (ChatAttachment, *os.File, error) {
	a, err := s.repo.Attachment(ctx, uid, id)
	if err != nil {
		return a, nil, err
	}
	f, err := os.Open(s.path(a))
	if err != nil {
		return a, nil, fmt.Errorf("打开附件失败: %w", err)
	}
	return a, f, nil
}
func (s *ChatService) DeleteAttachment(ctx context.Context, uid int64, id string) error {
	return s.repo.DeleteAttachment(ctx, uid, id, func(a ChatAttachment) error {
		if err := os.Remove(s.path(a)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清理附件失败: %w", err)
		}
		return nil
	})
}
func (s *ChatService) StorageUsed(ctx context.Context, uid int64) (int64, error) {
	return s.repo.StorageUsed(ctx, uid)
}

func (s *ChatService) StartTurn(ctx context.Context, uid int64, cid string, in ChatTurnInput) (ChatTurn, error) {
	requestID, err := uuid.Parse(in.RequestID)
	if err != nil {
		return ChatTurn{}, infraerrors.BadRequest("CHAT_REQUEST_ID", "请求标识无效")
	}
	if in.APIKeyID <= 0 || strings.TrimSpace(in.Model) == "" || len(in.Model) > 200 || !chatTextValid(in.Model) || len(in.Content) > ChatTextLimit || !chatTextValid(in.Content) {
		return ChatTurn{}, infraerrors.BadRequest("CHAT_MESSAGE", "密钥、模型或消息无效；文字不得超过 2 MB")
	}
	if len(in.AttachmentIDs) > 5 || (strings.TrimSpace(in.Content) == "" && len(in.AttachmentIDs) == 0) {
		return ChatTurn{}, infraerrors.BadRequest("CHAT_MESSAGE", "消息不能为空，且每条最多 5 个附件")
	}
	in.RequestID = requestID.String()
	if in.HistoryUpdatedAt.IsZero() {
		return ChatTurn{}, infraerrors.BadRequest("CHAT_HISTORY", "缺少会话历史版本，请刷新后重试")
	}
	seen := make(map[string]bool)
	for i, id := range in.AttachmentIDs {
		parsed, err := uuid.Parse(id)
		if err != nil || seen[parsed.String()] {
			return ChatTurn{}, infraerrors.BadRequest("CHAT_ATTACHMENT", "附件标识无效或重复")
		}
		in.AttachmentIDs[i] = parsed.String()
		seen[parsed.String()] = true
	}
	return s.repo.StartTurn(ctx, uid, cid, in)
}
func (s *ChatService) SaveReply(ctx context.Context, uid int64, cid, mid string, in ChatSnapshot) error {
	if len(in.Content)+len(in.Reasoning) > 8<<20 || len(in.Error) > 1000 || !chatTextValid(in.Error) || in.Revision <= 0 || !chatTextValid(in.Content) || !chatTextValid(in.Reasoning) {
		return infraerrors.BadRequest("CHAT_SNAPSHOT", "回复快照无效或过大")
	}
	switch in.Status {
	case "generating", "complete", "interrupted", "failed":
	default:
		return infraerrors.BadRequest("CHAT_STATUS", "回复状态无效")
	}
	return s.repo.SaveReply(ctx, uid, cid, mid, in)
}

func (s *ChatService) RequestLimit() int64 { return s.requestLimit }

func chatTextValid(text string) bool { return utf8.ValidString(text) && !strings.ContainsRune(text, 0) }
