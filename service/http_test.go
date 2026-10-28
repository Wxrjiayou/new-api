package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestIOCopyBytesGracefullyHidesUpstreamFingerprintHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Header("X-New-Api-Version", "local")
	c.Header("Access-Control-Allow-Origin", "*")

	upstream := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}
	for _, name := range []string{
		"X-New-Api-Version", "X-Client-Request-Id", "X-Upstream-Request-Id", "X-Served-By",
		"X-Debug-Tracking-Id", "Traceresponse", "1-Request-Id", "1-Via", "1-Version",
		"X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Pragma", "Expires",
		"Nel", "Report-To", "Via", "Server", "Access-Control-Allow-Origin", "Cf-Ray", "Eo-Log-Uuid",
	} {
		upstream.Header.Set(name, "upstream")
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Ratelimit-Remaining-Requests", "9")

	IOCopyBytesGracefully(c, upstream, []byte(`{}`))

	got := recorder.Header()
	assert.Equal(t, "local", got.Get("X-New-Api-Version"))
	assert.Equal(t, "*", got.Get("Access-Control-Allow-Origin"))
	for _, name := range []string{"X-Client-Request-Id", "X-Served-By", "Traceresponse", "1-Via", "Pragma", "Nel", "Via", "Server", "Cf-Ray", "Eo-Log-Uuid"} {
		assert.Empty(t, got.Get(name), name)
	}
	assert.Equal(t, "application/json", got.Get("Content-Type"))
	assert.Equal(t, "9", got.Get("X-Ratelimit-Remaining-Requests"))
}
