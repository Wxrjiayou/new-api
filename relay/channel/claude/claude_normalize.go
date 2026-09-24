package claude

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var claudeFormatNamespace = uuid.MustParse("a3e4c7f0-1b2d-4e6a-8c0f-9d3b5e7a1f4c")

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

func genBase58RequestID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		b = make([]byte, 24)
	}
	result := make([]byte, 24)
	for i := range result {
		result[i] = base58Alphabet[int(b[i])%len(base58Alphabet)]
	}
	return "req_" + string(result)
}

func deriveOrgID(tokenKey string) string {
	return uuid.NewSHA1(claudeFormatNamespace, []byte(tokenKey)).String()
}

func deriveWorkspaceID(tokenKey string) string {
	return uuid.NewSHA1(claudeFormatNamespace, []byte(tokenKey+"workspace")).String()
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

	c.Writer.Header().Set("request-id", genBase58RequestID())

	tokenKey := info.TokenKey
	if tokenKey != "" {
		c.Writer.Header().Set("anthropic-organization-id", deriveOrgID(tokenKey))
		c.Writer.Header().Set("anthropic-workspace-id", deriveWorkspaceID(tokenKey))
	}

	c.Writer.Header().Set("content-security-policy", "default-src 'none'; frame-ancestors 'none'")
	c.Writer.Header().Set("strict-transport-security", "max-age=31536000; includeSubDomains; preload")
	c.Writer.Header().Set("x-robots-tag", "none")

	setClaudeRatelimitHeaders(c, info)

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
