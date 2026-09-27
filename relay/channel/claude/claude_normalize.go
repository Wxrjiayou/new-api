package claude

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var big62 = big.NewInt(62)

var nonStreamHeaderWhitelist = map[string]bool{
	"content-type":      true,
	"cache-control":     true,
	"anthropic-version": true,
	"x-should-retry":    true,
	"retry-after":       true,
}

var streamHeaderWhitelist = map[string]bool{
	"content-type":      true,
	"cache-control":     true,
	"transfer-encoding": true,
	"x-accel-buffering": true,
}

func encodeUint64Base62(n uint64, length int) string {
	result := make([]byte, length)
	for i := length - 1; i >= 0; i-- {
		result[i] = base62Chars[n%62]
		n /= 62
	}
	return string(result)
}

func encodeBytesBase62(data []byte, length int) string {
	n := new(big.Int).SetBytes(data)
	mod := new(big.Int)
	result := make([]byte, length)
	for i := length - 1; i >= 0; i-- {
		n.DivMod(n, big62, mod)
		result[i] = base62Chars[mod.Int64()]
	}
	return string(result)
}

func randBase62(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		b = make([]byte, length)
	}
	result := make([]byte, length)
	for i := range result {
		result[i] = base62Chars[int(b[i])%62]
	}
	return string(result)
}

func genRequestID() string {
	ts := uint64(time.Now().UnixMilli())
	return "req_01" + encodeUint64Base62(ts, 7) + randBase62(15)
}

func deriveOrgID(tokenKey string) string {
	h := sha256.Sum256([]byte("org:" + tokenKey))
	h[6] = (h[6] & 0x0F) | 0x40 // UUID version 4
	h[8] = (h[8] & 0x3F) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func deriveWorkspaceID(tokenKey string) string {
	h := sha256.Sum256([]byte("wrkspc:" + tokenKey))
	return "wrkspc_01" + encodeBytesBase62(h[:16], 22)
}

func deleteBytes(data []byte, path string) []byte {
	result, err := sjson.DeleteBytes(data, path)
	if err != nil {
		return data
	}
	return result
}

func deleteStr(data string, path string) string {
	result, err := sjson.Delete(data, path)
	if err != nil {
		return data
	}
	return result
}

func setStr(data string, path string, value interface{}) string {
	result, err := sjson.Set(data, path, value)
	if err != nil {
		return data
	}
	return result
}

func setBytes(data []byte, path string, value interface{}) []byte {
	result, err := sjson.SetBytes(data, path, value)
	if err != nil {
		return data
	}
	return result
}

// normalizeClaudeBody normalizes a non-streaming Claude response body to match
// official API format: removes Max-specific fields, adds official-only fields.
func normalizeClaudeBody(data []byte) []byte {
	data = deleteBytes(data, "usage.iterations")
	data = deleteBytes(data, "usage.cache_creation")
	data = deleteBytes(data, "context_management")
	data = deleteBytes(data, "input_transformations")
	data = deleteBytes(data, "diagnostics")
	if gjson.GetBytes(data, "usage.inference_geo").Exists() {
		data = setBytes(data, "usage.inference_geo", "global")
	}
	if !gjson.GetBytes(data, "container").Exists() {
		data = setBytes(data, "container", nil)
	}
	return data
}

// normalizeClaudeStreamEvent normalizes a single SSE event string for streaming.
func normalizeClaudeStreamEvent(data string, eventType string) string {
	switch eventType {
	case "message_start":
		data = deleteStr(data, "message.context_management")
		data = deleteStr(data, "message.usage.cache_creation")
		data = deleteStr(data, "message.input_transformations")
		data = deleteStr(data, "message.diagnostics")
		if gjson.Get(data, "message.usage.inference_geo").Exists() {
			data = setStr(data, "message.usage.inference_geo", "global")
		}
		if !gjson.Get(data, "message.container").Exists() {
			data = setStr(data, "message.container", nil)
		}
	case "message_delta":
		data = deleteStr(data, "context_management")
		data = deleteStr(data, "usage.iterations")
		data = deleteStr(data, "usage.cache_creation")
		if !gjson.Get(data, "delta.container").Exists() {
			data = setStr(data, "delta.container", nil)
		}
	}
	return data
}

func upstreamHeader(resp *http.Response, name string) string {
	if resp == nil {
		return ""
	}
	return resp.Header.Get(name)
}

func setNormalizedHeaders(c *gin.Context, httpResp *http.Response, info *relaycommon.RelayInfo) {
	if c.Writer == nil {
		return
	}

	if reqID := upstreamHeader(httpResp, "Request-Id"); reqID != "" {
		c.Writer.Header().Set("request-id", reqID)
	} else {
		c.Writer.Header().Set("request-id", genRequestID())
	}

	tokenKey := info.TokenKey
	if orgID := upstreamHeader(httpResp, "Anthropic-Organization-Id"); orgID != "" {
		c.Writer.Header().Set("anthropic-organization-id", orgID)
	} else if tokenKey != "" {
		c.Writer.Header().Set("anthropic-organization-id", deriveOrgID(tokenKey))
	}

	if wsID := upstreamHeader(httpResp, "Anthropic-Workspace-Id"); wsID != "" {
		c.Writer.Header().Set("anthropic-workspace-id", wsID)
	} else if tokenKey != "" {
		c.Writer.Header().Set("anthropic-workspace-id", deriveWorkspaceID(tokenKey))
	}

	c.Writer.Header().Set("content-security-policy", "default-src 'none'; frame-ancestors 'none'")
	c.Writer.Header().Set("strict-transport-security", "max-age=31536000; includeSubDomains; preload")
	c.Writer.Header().Set("x-robots-tag", "none")

	setClaudeRatelimitHeaders(c, info)
}

func writeClaudeNormalizedResponse(c *gin.Context, httpResp *http.Response, data []byte, info *relaycommon.RelayInfo) {
	if c.Writer == nil {
		return
	}

	if httpResp != nil {
		for k, v := range httpResp.Header {
			if nonStreamHeaderWhitelist[strings.ToLower(k)] && len(v) > 0 {
				c.Writer.Header().Set(k, v[0])
			}
		}
	}

	setNormalizedHeaders(c, httpResp, info)

	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	statusCode := http.StatusOK
	if httpResp != nil {
		statusCode = httpResp.StatusCode
	}
	c.Writer.WriteHeader(statusCode)

	_, _ = io.Copy(c.Writer, io.NopCloser(strings.NewReader(string(data))))
	c.Writer.Flush()
}

func shouldNormalizeBody(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	return info.ChannelSetting.NormalizeClaudeBody || info.ChannelSetting.ForceClaudeFormat
}

func shouldNormalizeHeaders(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	return info.ChannelSetting.NormalizeClaudeHeaders || info.ChannelSetting.ForceClaudeFormat
}

func shouldApplyClaudeNormalize(info *relaycommon.RelayInfo) bool {
	return shouldNormalizeBody(info) || shouldNormalizeHeaders(info)
}

func IsStreamHeaderAllowed(name string) bool {
	return streamHeaderWhitelist[strings.ToLower(name)]
}
