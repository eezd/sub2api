//go:build unit && embed

package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/web"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPelicanShowcaseSettingsRefreshEmbeddedSidebarFlag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &showcaseSettingRepo{toggleSettingRepo{values: map[string]string{
		service.SettingKeyPelicanShowcaseEnabled: "false",
	}}}
	settings := service.NewSettingService(repo, &config.Config{})
	frontend, err := web.NewFrontendServer(settings)
	require.NoError(t, err)
	settings.SetOnUpdateCallback(frontend.InvalidateCache)
	h := NewPelicanShowcaseHandler(service.NewPelicanShowcaseService(&showcaseHandlerRepo{}, settings))
	router := gin.New()
	router.Use(frontend.Middleware())
	router.PUT("/api/v1/admin/pelican-showcase/settings", h.UpdateSettings)

	page := httptest.NewRecorder()
	router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/pelican-showcase", nil))
	require.Equal(t, http.StatusOK, page.Code)
	require.Contains(t, page.Body.String(), `"pelican_showcase_enabled":false`)

	for _, enabled := range []bool{true, false} {
		previousETag := page.Header().Get("ETag")
		request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/pelican-showcase/settings",
			strings.NewReader(fmt.Sprintf(`{"enabled":%t,"max_items":20}`, enabled)))
		request.Header.Set("Content-Type", "application/json")
		updated := httptest.NewRecorder()
		router.ServeHTTP(updated, request)
		require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())

		request = httptest.NewRequest(http.MethodGet, "/pelican-showcase", nil)
		request.Header.Set("If-None-Match", previousETag)
		page = httptest.NewRecorder()
		router.ServeHTTP(page, request)
		require.Equal(t, http.StatusOK, page.Code)
		require.Contains(t, page.Body.String(), fmt.Sprintf(`"pelican_showcase_enabled":%t`, enabled))
		require.NotEqual(t, previousETag, page.Header().Get("ETag"))
	}
}
