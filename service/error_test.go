package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetStatusCode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		statusCode       int
		statusCodeConfig string
		expectedCode     int
	}{
		{
			name:             "map string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"503"}`,
			expectedCode:     503,
		},
		{
			name:             "map int value",
			statusCode:       429,
			statusCodeConfig: `{"429":503}`,
			expectedCode:     503,
		},
		{
			name:             "skip invalid string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"bad-code"}`,
			expectedCode:     429,
		},
		{
			name:             "skip status code 200",
			statusCode:       200,
			statusCodeConfig: `{"200":503}`,
			expectedCode:     200,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			newAPIError := &types.NewAPIError{
				StatusCode: tc.statusCode,
			}
			ResetStatusCode(newAPIError, tc.statusCodeConfig)
			require.Equal(t, tc.expectedCode, newAPIError.StatusCode)
		})
	}
}

func TestRelayErrorHandlerTruncatesInvalidJSONBodyInLog(t *testing.T) {
	withDebugEnabled(t, false)

	body := strings.Repeat("b", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, "bad response status code 500", newAPIError.Error())
	require.Contains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), fmt.Sprintf("original_length=%d", len(body)))
	require.NotContains(t, logBuffer.String(), strings.Repeat("b", common.LocalLogContentLimit+1))
}

func TestRelayErrorHandlerKeepsStructuredErrorMessage(t *testing.T) {
	message := strings.Repeat("c", common.LocalLogContentLimit+256)
	body := `{"message":"` + message + `"}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerKeepsOpenAIErrorMessage(t *testing.T) {
	message := strings.Repeat("d", common.LocalLogContentLimit+256)
	body := `{"error":{"message":"` + message + `","type":"server_error","code":"server_error"}}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerKeepsInvalidJSONBodyInDebugLog(t *testing.T) {
	withDebugEnabled(t, true)

	body := strings.Repeat("e", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.NotContains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), body)
}

func withDebugEnabled(t *testing.T, enabled bool) {
	t.Helper()

	oldDebug := common.DebugEnabled
	common.DebugEnabled = enabled
	t.Cleanup(func() {
		common.DebugEnabled = oldDebug
	})
}

func TestRelayErrorHandlerInterceptsUpstream429And503(t *testing.T) {
	t.Parallel()

	openaiBody := `{"error":{"message":"rate limit exceeded","type":"tokens","code":"rate_limit"}}`
	plainBody := `{"message":"service temporarily unavailable"}`
	invalidBody := `not json at all`

	testCases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{"429 with OpenAI error body", http.StatusTooManyRequests, openaiBody},
		{"503 with OpenAI error body", http.StatusServiceUnavailable, openaiBody},
		{"429 with plain JSON body", http.StatusTooManyRequests, plainBody},
		{"503 with plain JSON body", http.StatusServiceUnavailable, plainBody},
		{"429 with invalid JSON body", http.StatusTooManyRequests, invalidBody},
		{"503 with invalid JSON body", http.StatusServiceUnavailable, invalidBody},
		{"429 with empty body", http.StatusTooManyRequests, ""},
		{"503 with empty body", http.StatusServiceUnavailable, ""},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{
				StatusCode: tc.statusCode,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}

			newAPIError := RelayErrorHandler(context.Background(), resp, false)

			require.NotNil(t, newAPIError)
			assert.Equal(t, tc.statusCode, newAPIError.StatusCode, "status code must be preserved")
			assert.Equal(t, upstreamUnavailableMessage, newAPIError.Error(), "message must be replaced")

			oaiErr := newAPIError.ToOpenAIError()
			assert.Equal(t, upstreamUnavailableMessage, oaiErr.Message, "OpenAI error message must be replaced")

			claudeErr := newAPIError.ToClaudeError()
			assert.Equal(t, upstreamUnavailableMessage, claudeErr.Message, "Claude error message must be replaced")
		})
	}
}

func TestRelayErrorHandlerDoesNotInterceptOtherStatusCodes(t *testing.T) {
	t.Parallel()

	originalMessage := "something went wrong"
	body := fmt.Sprintf(`{"error":{"message":"%s","type":"server_error","code":"server_error"}}`, originalMessage)

	nonInterceptedCodes := []int{
		http.StatusBadRequest,          // 400
		http.StatusUnauthorized,        // 401
		http.StatusForbidden,           // 403
		http.StatusNotFound,            // 404
		http.StatusRequestTimeout,      // 408
		http.StatusConflict,            // 409
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusGatewayTimeout,      // 504
	}

	for _, code := range nonInterceptedCodes {
		code := code
		t.Run(fmt.Sprintf("status_%d_not_intercepted", code), func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{
				StatusCode: code,
				Body:       io.NopCloser(strings.NewReader(body)),
			}

			newAPIError := RelayErrorHandler(context.Background(), resp, false)

			require.NotNil(t, newAPIError)
			assert.Equal(t, code, newAPIError.StatusCode)
			assert.Equal(t, originalMessage, newAPIError.Error(), "message must NOT be replaced for status %d", code)
		})
	}
}

func TestRelayErrorHandlerInterceptionLogsOriginalBody(t *testing.T) {
	originalBody := `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"rate_limit"}}`

	var logBuffer bytes.Buffer
	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(originalBody)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	assert.Equal(t, upstreamUnavailableMessage, newAPIError.Error())
	assert.Contains(t, logBuffer.String(), "upstream 429 intercepted")
	assert.Contains(t, logBuffer.String(), "You exceeded your current quota")
}

func TestRelayErrorHandlerInterceptionPreservesShowBodyWhenFailBehavior(t *testing.T) {
	body := `{"error":{"message":"rate limit hit","type":"rate_limit","code":"rate_limit"}}`

	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, true)

	require.NotNil(t, newAPIError)
	assert.Equal(t, http.StatusTooManyRequests, newAPIError.StatusCode)
	assert.Equal(t, upstreamUnavailableMessage, newAPIError.Error(), "interception overrides even with showBodyWhenFail=true")
}
