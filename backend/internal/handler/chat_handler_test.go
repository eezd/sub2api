//go:build unit

package handler

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type chatHandlerRepository struct {
	service.ChatRepository
	conversation string
	attachment   service.ChatAttachment
}

func (r *chatHandlerRepository) Attachment(_ context.Context, uid int64, id string) (service.ChatAttachment, error) {
	if uid != 1 || id != r.attachment.ID {
		return service.ChatAttachment{}, service.ErrChatNotFound
	}
	return r.attachment, nil
}
func (r *chatHandlerRepository) AddAttachment(_ context.Context, _ int64, a service.ChatAttachment, persist func() error) error {
	r.attachment = a
	return persist()
}

func TestChatHandlerPrivateAttachments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &chatHandlerRepository{conversation: uuid.NewString()}
	cfg := &config.Config{Chat: config.ChatConfig{AttachmentDir: t.TempDir()}}
	h := NewChatHandler(service.NewChatService(repo, cfg))
	router := func(uid int64) *gin.Engine {
		r := gin.New()
		if uid > 0 {
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: uid}) })
		}
		r.POST("/conversations/:id/attachments", h.Upload)
		r.GET("/attachments/:attachment", h.Download)
		return r
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "notes.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("私有资料"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/conversations/"+repo.conversation+"/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router(1).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	_, err = os.Stat(filepath.Join(cfg.Chat.AttachmentDir, repo.conversation, repo.attachment.ID))
	require.NoError(t, err)
	w = httptest.NewRecorder()
	router(0).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/attachments/"+repo.attachment.ID, nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	w = httptest.NewRecorder()
	router(2).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/attachments/"+repo.attachment.ID, nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	w = httptest.NewRecorder()
	router(1).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/attachments/"+repo.attachment.ID, nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "私有资料", w.Body.String())
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, w.Header().Get("Content-Disposition"), "attachment;")
	w = httptest.NewRecorder()
	router(1).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/attachments/not-a-uuid", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
}
func TestChatHandlerRejectsOversizedFile(t *testing.T) {
	repo := &chatHandlerRepository{conversation: uuid.NewString()}
	svc := service.NewChatService(repo, &config.Config{Chat: config.ChatConfig{AttachmentDir: t.TempDir()}})
	_, err := svc.Upload(context.Background(), 1, repo.conversation, "huge.txt", "", strings.NewReader(strings.Repeat("a", int(service.ChatFileLimit)+1)))
	require.Error(t, err)
	require.Empty(t, repo.attachment.ID)
}
