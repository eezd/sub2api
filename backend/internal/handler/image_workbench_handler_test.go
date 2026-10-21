//go:build unit

package handler

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type workbenchHandlerRepository struct {
	service.ImageWorkbenchRepository
	asset service.WorkbenchAsset
}

func (r *workbenchHandlerRepository) Interrupt(context.Context) error { return nil }
func (r *workbenchHandlerRepository) AddAsset(_ context.Context, _ int64, a service.WorkbenchAsset, write func() error) error {
	r.asset = a
	return write()
}
func (r *workbenchHandlerRepository) Asset(_ context.Context, uid int64, id string) (service.WorkbenchAsset, error) {
	if uid != 1 || id != r.asset.ID {
		return service.WorkbenchAsset{}, service.ErrWorkbenchNotFound
	}
	return r.asset, nil
}
func TestWorkbenchPrivateImages(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	repo := &workbenchHandlerRepository{}
	svc, err := service.NewImageWorkbenchService(repo, nil, nil, &config.Config{})
	require.NoError(t, err)
	h := NewImageWorkbenchHandler(svc)
	router := func(uid int64) *gin.Engine {
		r := gin.New()
		if uid > 0 {
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: uid}) })
		}
		r.POST("/assets", h.Upload)
		r.GET("/assets/:asset", h.Download)
		return r
	}
	var imageData bytes.Buffer
	require.NoError(t, png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "private.png")
	require.NoError(t, err)
	_, err = part.Write(imageData.Bytes())
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest("POST", "/assets", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router(1).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	for _, tc := range []struct {
		uid    int64
		status int
	}{{0, 401}, {2, 404}, {1, 200}} {
		w = httptest.NewRecorder()
		router(tc.uid).ServeHTTP(w, httptest.NewRequest("GET", "/assets/"+repo.asset.ID, nil))
		require.Equal(t, tc.status, w.Code)
		if tc.status == 200 {
			require.Equal(t, imageData.Bytes(), w.Body.Bytes())
			require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
			require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			require.Contains(t, w.Header().Get("Content-Disposition"), "attachment;")
		}
	}
	w = httptest.NewRecorder()
	router(1).ServeHTTP(w, httptest.NewRequest("GET", "/assets/not-a-uuid", nil))
	require.Equal(t, 400, w.Code)
}
