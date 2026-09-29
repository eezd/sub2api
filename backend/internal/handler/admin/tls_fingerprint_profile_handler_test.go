package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBindTLSFingerprintProfileJSONRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	body := `{"name":"` + strings.Repeat("a", int(maxTLSFingerprintProfileRequestBytes)) + `"}`
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/tls-fingerprint-profiles", strings.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")

	var request CreateTLSFingerprintProfileRequest
	require.False(t, bindTLSFingerprintProfileJSON(context, &request))
	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
}
