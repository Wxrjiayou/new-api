package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStreamTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c, recorder
}

func newStreamTestInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "test-model",
		},
	}
}

func TestOaiStreamHandler_UpstreamErrorInSSE_ReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	sseBody := "data: {\"error\":{\"code\":\"stream_initialization_failed\",\"message\":\"Tool message must have tool_call_id\"}}\n\ndata: [DONE]\n\n"

	c, recorder := newStreamTestContext(t)
	info := newStreamTestInfo()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sseBody)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}

	usage, apiErr := OaiStreamHandler(c, info, resp)

	require.NotNil(t, apiErr, "should return an error for upstream SSE error")
	assert.Nil(t, usage)
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.Contains(t, apiErr.Error(), "stream_initialization_failed")
	assert.Equal(t, 0, info.SendResponseCount, "no data should have been flushed to client")
	assert.Empty(t, recorder.Body.String(), "no SSE data should have been written to client")
}

func TestOaiStreamHandler_NormalStream_NoError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	sseBody := "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

	c, _ := newStreamTestContext(t)
	info := newStreamTestInfo()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sseBody)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}

	usage, apiErr := OaiStreamHandler(c, info, resp)

	require.Nil(t, apiErr, "normal stream should not return error")
	require.NotNil(t, usage)
}

func TestOaiStreamHandler_ErrorAfterValidChunks_NoRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	// Error arrives after a valid chunk — SendResponseCount > 0, can't retry
	sseBody := "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\ndata: {\"error\":{\"code\":\"internal_error\",\"message\":\"something broke\"}}\n\ndata: [DONE]\n\n"

	c, _ := newStreamTestContext(t)
	info := newStreamTestInfo()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(sseBody)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}

	usage, apiErr := OaiStreamHandler(c, info, resp)

	assert.Nil(t, apiErr, "error after valid chunks should not trigger retry (headers already committed)")
	assert.NotNil(t, usage)
	assert.Greater(t, info.SendResponseCount, 0)
}
