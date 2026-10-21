package handler

import (
	"log"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ChatHandler struct{ service *service.ChatService }

func NewChatHandler(svc *service.ChatService) *ChatHandler { return &ChatHandler{service: svc} }

func chatCaller(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录")
		return 0, false
	}
	return subject.UserID, true
}
func chatID(c *gin.Context, name string) (string, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		response.BadRequest(c, "无效的聊天标识")
		return "", false
	}
	return id.String(), true
}
func chatRespond(c *gin.Context, data any, err error) {
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

func (h *ChatHandler) List(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	page := 1
	if raw := c.Query("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100000 {
			response.BadRequest(c, "页码无效")
			return
		}
		page = n
	}
	items, err := h.service.List(c.Request.Context(), uid, page)
	chatRespond(c, items, err)
}
func (h *ChatHandler) Create(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	item, err := h.service.Create(c.Request.Context(), uid)
	chatRespond(c, item, err)
}
func (h *ChatHandler) Get(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	id, ok := chatID(c, "id")
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), uid, id)
	chatRespond(c, item, err)
}
func (h *ChatHandler) Rename(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	id, ok := chatID(c, "id")
	if !ok {
		return
	}
	var in struct {
		Title string `json:"title"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "标题请求无效")
		return
	}
	chatRespond(c, nil, h.service.Rename(c.Request.Context(), uid, id, in.Title))
}
func (h *ChatHandler) Delete(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	id, ok := chatID(c, "id")
	if !ok {
		return
	}
	chatRespond(c, nil, h.service.Delete(c.Request.Context(), uid, id))
}
func (h *ChatHandler) Limits(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	used, err := h.service.StorageUsed(c.Request.Context(), uid)
	chatRespond(c, gin.H{"used_bytes": used, "storage_bytes": service.ChatStorageLimit, "file_bytes": service.ChatFileLimit, "text_bytes": service.ChatTextLimit, "request_bytes": h.service.RequestLimit()}, err)
}
func (h *ChatHandler) Upload(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	cid, ok := chatID(c, "id")
	if !ok {
		return
	}
	// 包含原文件、最多 2 MB 提取文本和 multipart 边界；超额读取由 MaxBytesReader 中止。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.ChatFileLimit+service.ChatTextLimit+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
		response.BadRequest(c, "附件请求无效或过大")
		return
	}
	defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	files := c.Request.MultipartForm.File["file"]
	if len(files) != 1 || len(c.Request.MultipartForm.File) != 1 {
		response.BadRequest(c, "每次只能上传一个附件")
		return
	}
	f, err := files[0].Open()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("关闭聊天附件失败: %v", err)
		}
	}()
	a, err := h.service.Upload(c.Request.Context(), uid, cid, files[0].Filename, c.PostForm("extracted_text"), f)
	chatRespond(c, a, err)
}
func (h *ChatHandler) Download(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	id, ok := chatID(c, "attachment")
	if !ok {
		return
	}
	a, f, err := h.service.OpenAttachment(c.Request.Context(), uid, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("关闭聊天附件失败: %v", err)
		}
	}()
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename}))
	c.Header("Content-Type", a.MimeType)
	http.ServeContent(c.Writer, c.Request, a.Filename, time.Time{}, f)
}
func (h *ChatHandler) DeleteAttachment(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	id, ok := chatID(c, "attachment")
	if !ok {
		return
	}
	chatRespond(c, nil, h.service.DeleteAttachment(c.Request.Context(), uid, id))
}
func (h *ChatHandler) StartTurn(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	cid, ok := chatID(c, "id")
	if !ok {
		return
	}
	var in service.ChatTurnInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "消息请求无效")
		return
	}
	out, err := h.service.StartTurn(c.Request.Context(), uid, cid, in)
	chatRespond(c, out, err)
}
func (h *ChatHandler) SaveReply(c *gin.Context) {
	uid, ok := chatCaller(c)
	if !ok {
		return
	}
	cid, ok := chatID(c, "id")
	if !ok {
		return
	}
	mid, ok := chatID(c, "message")
	if !ok {
		return
	}
	var in service.ChatSnapshot
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "回复快照无效")
		return
	}
	chatRespond(c, nil, h.service.SaveReply(c.Request.Context(), uid, cid, mid, in))
}
