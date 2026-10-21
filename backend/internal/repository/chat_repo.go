package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type chatRepository struct{ db *sql.DB }

func NewChatRepository(db *sql.DB) service.ChatRepository { return &chatRepository{db: db} }

const chatConversationColumns = `id, title, api_key_id, model, deleting, updated_at`
const chatAttachmentColumns = `id, conversation_id, message_id, filename, mime_type, size, extracted_text`

func scanChatConversation(row interface{ Scan(...any) error }) (service.ChatConversation, error) {
	var c service.ChatConversation
	err := row.Scan(&c.ID, &c.Title, &c.APIKeyID, &c.Model, &c.Deleting, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = service.ErrChatNotFound
	}
	return c, err
}
func scanChatAttachment(row interface{ Scan(...any) error }) (service.ChatAttachment, error) {
	var a service.ChatAttachment
	err := row.Scan(&a.ID, &a.ConversationID, &a.MessageID, &a.Filename, &a.MimeType, &a.Size, &a.ExtractedText)
	if errors.Is(err, sql.ErrNoRows) {
		err = service.ErrChatNotFound
	}
	return a, err
}
func lockChat(ctx context.Context, tx *sql.Tx, uid int64, cid string, allowDeleting bool) (service.ChatConversation, error) {
	c, err := scanChatConversation(tx.QueryRowContext(ctx, `SELECT `+chatConversationColumns+` FROM chat_conversations WHERE id=$1 AND user_id=$2 FOR UPDATE`, cid, uid))
	if err != nil {
		return c, err
	}
	if c.Deleting && !allowDeleting {
		return c, service.ErrChatBusy
	}
	return c, nil
}
func interruptStaleChat(ctx context.Context, tx *sql.Tx, cid string) error {
	_, err := tx.ExecContext(ctx, `UPDATE chat_messages SET status='interrupted', error='生成连接已中断，仅保留最近保存的内容', updated_at=NOW() WHERE conversation_id=$1 AND status='generating' AND updated_at < NOW()-INTERVAL '30 seconds'`, cid)
	return err
}

func (r *chatRepository) List(ctx context.Context, uid int64, page int) ([]service.ChatConversation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+chatConversationColumns+` FROM chat_conversations WHERE user_id=$1 ORDER BY updated_at DESC, id LIMIT 50 OFFSET $2`, uid, (page-1)*50)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ChatConversation, 0)
	for rows.Next() {
		c, err := scanChatConversation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, c)
	}
	return items, rows.Err()
}
func (r *chatRepository) Create(ctx context.Context, uid int64, id string) (service.ChatConversation, error) {
	return scanChatConversation(r.db.QueryRowContext(ctx, `INSERT INTO chat_conversations(id,user_id,title) VALUES($1,$2,'新对话') RETURNING `+chatConversationColumns, id, uid))
}
func (r *chatRepository) Get(ctx context.Context, uid int64, cid string) (service.ChatDetail, error) {
	d := service.ChatDetail{Messages: make([]service.ChatMessage, 0), Attachments: make([]service.ChatAttachment, 0)}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()
	d.Conversation, err = lockChat(ctx, tx, uid, cid, true)
	if err != nil {
		return d, err
	}
	if err := interruptStaleChat(ctx, tx, cid); err != nil {
		return d, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,role,content,reasoning,model,api_key_id,status,revision,error,created_at,updated_at FROM chat_messages WHERE conversation_id=$1 ORDER BY position`, cid)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var m service.ChatMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Reasoning, &m.Model, &m.APIKeyID, &m.Status, &m.Revision, &m.Error, &m.CreatedAt, &m.UpdatedAt); err != nil {
			_ = rows.Close()
			return d, err
		}
		d.Messages = append(d.Messages, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return d, err
	}
	_ = rows.Close()
	d.Attachments, err = chatAttachments(ctx, tx, cid)
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}
func chatAttachments(ctx context.Context, tx *sql.Tx, cid string) ([]service.ChatAttachment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+chatAttachmentColumns+` FROM chat_attachments WHERE conversation_id=$1 ORDER BY created_at,id`, cid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.ChatAttachment, 0)
	for rows.Next() {
		a, err := scanChatAttachment(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}
func (r *chatRepository) Rename(ctx context.Context, uid int64, cid, title string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE chat_conversations SET title=$3,updated_at=NOW() WHERE id=$1 AND user_id=$2 AND NOT deleting`, cid, uid, title)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return service.ErrChatNotFound
	}
	return nil
}
func (r *chatRepository) MarkDeleting(ctx context.Context, uid int64, cid string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = lockChat(ctx, tx, uid, cid, true); err != nil {
		return err
	}
	if err = interruptStaleChat(ctx, tx, cid); err != nil {
		return err
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_messages WHERE conversation_id=$1 AND status='generating')`, cid).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return service.ErrChatBusy
	}
	if _, err = tx.ExecContext(ctx, `UPDATE chat_conversations SET deleting=TRUE WHERE id=$1`, cid); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *chatRepository) Delete(ctx context.Context, uid int64, cid string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM chat_conversations WHERE id=$1 AND user_id=$2 AND deleting`, cid, uid)
	return err
}

// 用户锁串行化配额核算，会话锁协调文件落盘、消息引用和删除；锁内失败会保留可重试的数据库状态。
func (r *chatRepository) AddAttachment(ctx context.Context, uid int64, a service.ChatAttachment, persist func() error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var userID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, uid).Scan(&userID); err != nil {
		return err
	}
	if _, err = lockChat(ctx, tx, uid, a.ConversationID, false); err != nil {
		return err
	}
	var used int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size + octet_length(extracted_text)),0) FROM chat_attachments WHERE user_id=$1`, uid).Scan(&used); err != nil {
		return err
	}
	if used+a.Size+int64(len(a.ExtractedText)) > service.ChatStorageLimit {
		return service.ErrChatStorage
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO chat_attachments(id,conversation_id,user_id,filename,mime_type,size,extracted_text) VALUES($1,$2,$3,$4,$5,$6,$7)`, a.ID, a.ConversationID, uid, a.Filename, a.MimeType, a.Size, a.ExtractedText); err != nil {
		return err
	}
	if err = persist(); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *chatRepository) Attachment(ctx context.Context, uid int64, id string) (service.ChatAttachment, error) {
	return scanChatAttachment(r.db.QueryRowContext(ctx, `SELECT `+chatAttachmentColumns+` FROM chat_attachments WHERE id=$1 AND user_id=$2`, id, uid))
}
func (r *chatRepository) DeleteAttachment(ctx context.Context, uid int64, id string, remove func(service.ChatAttachment) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var cid string
	if err = tx.QueryRowContext(ctx, `SELECT conversation_id FROM chat_attachments WHERE id=$1 AND user_id=$2`, id, uid).Scan(&cid); errors.Is(err, sql.ErrNoRows) {
		return service.ErrChatNotFound
	} else if err != nil {
		return err
	}
	if _, err = lockChat(ctx, tx, uid, cid, false); err != nil {
		return err
	}
	a, err := scanChatAttachment(tx.QueryRowContext(ctx, `SELECT `+chatAttachmentColumns+` FROM chat_attachments WHERE id=$1 AND user_id=$2`, id, uid))
	if err != nil {
		return err
	}
	if a.MessageID != nil {
		return infraerrors.Conflict("CHAT_ATTACHMENT_USED", "已发送的附件请随会话一起删除")
	}
	if err = remove(a); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM chat_attachments WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *chatRepository) StorageUsed(ctx context.Context, uid int64) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size + octet_length(extracted_text)),0) FROM chat_attachments WHERE user_id=$1`, uid).Scan(&n)
	return n, err
}

func (r *chatRepository) StartTurn(ctx context.Context, uid int64, cid string, in service.ChatTurnInput) (service.ChatTurn, error) {
	var out service.ChatTurn
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := lockChat(ctx, tx, uid, cid, false)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,role FROM chat_messages WHERE conversation_id=$1 AND turn_id=$2`, cid, in.RequestID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, role string
		if err = rows.Scan(&id, &role); err != nil {
			_ = rows.Close()
			return out, err
		}
		if role == "user" {
			out.UserMessageID = id
		} else {
			out.AssistantMessageID = id
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return out, err
	}
	_ = rows.Close()
	if out.UserMessageID != "" {
		out.Existing = true
		return out, tx.Commit()
	}
	if err = interruptStaleChat(ctx, tx, cid); err != nil {
		return out, err
	}
	var busy, owned bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_messages WHERE conversation_id=$1 AND status='generating')`, cid).Scan(&busy); err != nil {
		return out, err
	}
	if busy {
		return out, service.ErrChatBusy
	}
	// 在会话锁内核对构建请求时的版本，防止刷新历史到提交轮次之间被其他窗口更新。
	if !c.UpdatedAt.Equal(in.HistoryUpdatedAt) {
		return out, service.ErrChatHistory
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL)`, in.APIKeyID, uid).Scan(&owned); err != nil {
		return out, err
	}
	if !owned {
		return out, infraerrors.BadRequest("CHAT_KEY", "请选择自己的有效 API 密钥")
	}
	for _, id := range in.AttachmentIDs {
		var available bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_attachments WHERE id=$1 AND conversation_id=$2 AND user_id=$3 AND message_id IS NULL)`, id, cid, uid).Scan(&available); err != nil {
			return out, err
		}
		if !available {
			return out, service.ErrChatNotFound
		}
	}
	out.UserMessageID = uuid.NewString()
	out.AssistantMessageID = uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO chat_messages(id,conversation_id,turn_id,role,content,model,api_key_id,status) VALUES($1,$3,$4,'user',$5,$6,$7,'complete'),($2,$3,$4,'assistant','',$6,$7,'generating')`, out.UserMessageID, out.AssistantMessageID, cid, in.RequestID, in.Content, in.Model, in.APIKeyID); err != nil {
		return out, err
	}
	for _, id := range in.AttachmentIDs {
		if _, err = tx.ExecContext(ctx, `UPDATE chat_attachments SET message_id=$2 WHERE id=$1`, id, out.UserMessageID); err != nil {
			return out, err
		}
	}
	title := c.Title
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_messages WHERE conversation_id=$1`, cid).Scan(&count); err != nil {
		return out, err
	}
	if count == 2 && title == "新对话" {
		text := []rune(strings.TrimSpace(in.Content))
		if len(text) > 40 {
			text = text[:40]
		}
		if len(text) > 0 {
			title = string(text)
		} else {
			title = "附件对话"
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE chat_conversations SET title=$2,api_key_id=$3,model=$4,updated_at=NOW() WHERE id=$1`, cid, title, in.APIKeyID, in.Model); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (r *chatRepository) SaveReply(ctx context.Context, uid int64, cid, mid string, in service.ChatSnapshot) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = lockChat(ctx, tx, uid, cid, false); err != nil {
		return err
	}
	var status string
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT status,revision FROM chat_messages WHERE id=$1 AND conversation_id=$2 AND role='assistant'`, mid, cid).Scan(&status, &revision); errors.Is(err, sql.ErrNoRows) {
		return service.ErrChatNotFound
	} else if err != nil {
		return err
	}
	// 旧快照不得覆盖新内容；最终状态也不能被晚到的心跳重新打开。
	if in.Revision <= revision {
		return tx.Commit()
	}
	// 失联后允许补交最终快照，但不能由心跳重新开启生成。
	if status != "generating" && (status != "interrupted" || in.Status == "generating") {
		return infraerrors.Conflict("CHAT_FINISHED", "回复已结束，请刷新会话")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE chat_messages SET content=$2,reasoning=$3,status=$4,revision=$5,error=$6,updated_at=NOW() WHERE id=$1`, mid, in.Content, in.Reasoning, in.Status, in.Revision, in.Error); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE chat_conversations SET updated_at=NOW() WHERE id=$1`, cid); err != nil {
		return err
	}
	return tx.Commit()
}
