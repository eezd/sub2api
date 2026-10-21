package handler

import (
	"errors"
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

type ImageWorkbenchHandler struct {
	service *service.ImageWorkbenchService
}

func NewImageWorkbenchHandler(svc *service.ImageWorkbenchService) *ImageWorkbenchHandler {
	return &ImageWorkbenchHandler{service: svc}
}
func (h *ImageWorkbenchHandler) BindGateway(gateway http.Handler) { h.service.BindGateway(gateway) }

func workbenchCaller(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "请先登录")
		return 0, false
	}
	c.Header("Cache-Control", "private, no-store")
	return subject.UserID, true
}
func workbenchID(c *gin.Context, param string) (string, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		response.BadRequest(c, "生图任务或图片标识无效")
		return "", false
	}
	return id.String(), true
}
func workbenchRespond(c *gin.Context, data any, err error) {
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, data)
}

func (h *ImageWorkbenchHandler) Limits(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	usage, err := h.service.Usage(c.Request.Context(), uid)
	workbenchRespond(c, gin.H{"used_bytes": usage.UsedBytes, "reserved_bytes": usage.ReservedBytes, "storage_bytes": service.WorkbenchStorageLimit, "file_bytes": service.WorkbenchFileLimit, "request_bytes": h.service.RequestLimit()}, err)
}
func (h *ImageWorkbenchHandler) Models(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Query("api_key_id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "请选择 API 密钥")
		return
	}
	models, err := h.service.Models(c.Request.Context(), uid, id, c.Query("protocol"), c.Request)
	workbenchRespond(c, models, err)
}
func (h *ImageWorkbenchHandler) List(c *gin.Context) {
	uid, ok := workbenchCaller(c)
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
	jobs, err := h.service.List(c.Request.Context(), uid, page)
	workbenchRespond(c, jobs, err)
}
func (h *ImageWorkbenchHandler) Get(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	id, ok := workbenchID(c, "id")
	if !ok {
		return
	}
	job, err := h.service.Get(c.Request.Context(), uid, id)
	workbenchRespond(c, job, err)
}
func (h *ImageWorkbenchHandler) Submit(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	var in service.WorkbenchInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "生图参数格式无效")
		return
	}
	job, existing, err := h.service.Submit(c.Request.Context(), uid, in, c.Request)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"code": 0, "data": gin.H{"job": job, "existing": existing}})
}
func (h *ImageWorkbenchHandler) Cancel(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	id, ok := workbenchID(c, "id")
	if !ok {
		return
	}
	workbenchRespond(c, nil, h.service.Cancel(c.Request.Context(), uid, id))
}
func (h *ImageWorkbenchHandler) Delete(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	id, ok := workbenchID(c, "id")
	if !ok {
		return
	}
	workbenchRespond(c, nil, h.service.Delete(c.Request.Context(), uid, id))
}
func (h *ImageWorkbenchHandler) Drafts(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	assets, err := h.service.Drafts(c.Request.Context(), uid)
	workbenchRespond(c, assets, err)
}
func (h *ImageWorkbenchHandler) Upload(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.WorkbenchFileLimit+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"code": 413, "message": "图片请求过大，单张最多 20 MiB"})
		} else {
			response.BadRequest(c, "图片上传请求无效")
		}
		return
	}
	defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	files := c.Request.MultipartForm.File["file"]
	if len(files) != 1 || len(c.Request.MultipartForm.File) != 1 {
		response.BadRequest(c, "每次只能上传一张图片")
		return
	}
	f, err := files[0].Open()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("关闭生图上传文件失败: %v", err)
		}
	}()
	a, err := h.service.Upload(c.Request.Context(), uid, files[0].Filename, f)
	workbenchRespond(c, a, err)
}
func (h *ImageWorkbenchHandler) Download(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	id, ok := workbenchID(c, "asset")
	if !ok {
		return
	}
	a, f, err := h.service.Open(c.Request.Context(), uid, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Printf("关闭生图图片失败: %v", err)
		}
	}()
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename}))
	c.Header("Content-Type", a.MimeType)
	http.ServeContent(c.Writer, c.Request, a.Filename, time.Time{}, f)
}
func (h *ImageWorkbenchHandler) DeleteAsset(c *gin.Context) {
	uid, ok := workbenchCaller(c)
	if !ok {
		return
	}
	id, ok := workbenchID(c, "asset")
	if !ok {
		return
	}
	workbenchRespond(c, nil, h.service.DeleteAsset(c.Request.Context(), uid, id))
}
