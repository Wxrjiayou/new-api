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

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

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
// bedrockFormat selects the AWSB target shape: fields real Bedrock returns
// (nested cache_creation, context_management when the beta is applied) are
// preserved instead of stripped.
func normalizeClaudeBody(data []byte, bedrockFormat bool) []byte {
	data = deleteBytes(data, "usage.iterations")
	if !bedrockFormat {
		data = deleteBytes(data, "usage.cache_creation")
		data = deleteBytes(data, "context_management")
	} else if cm := gjson.GetBytes(data, "context_management"); cm.Exists() && len(cm.Get("applied_edits").Array()) == 0 {
		// Real Bedrock echoes context_management only when edits were
		// actually applied; MAX returns an empty envelope on every request
		// (1118 post-fix captures: XDM/LYQ omit it on non-beta cases).
		data = deleteBytes(data, "context_management")
	}
	data = deleteBytes(data, "input_transformations")
	data = deleteBytes(data, "diagnostics")
	data = deleteBytes(data, "safeguard_results")
	data = deleteBytes(data, "stop_details")
	if gjson.GetBytes(data, "usage.inference_geo").Exists() {
		if bedrockFormat {
			data = deleteBytes(data, "usage.inference_geo")
		} else {
			data = setBytes(data, "usage.inference_geo", "global")
		}
	}
	if !gjson.GetBytes(data, "container").Exists() {
		data = setBytes(data, "container", nil)
	}
	gjson.GetBytes(data, "content").ForEach(func(key, value gjson.Result) bool {
		idx := key.String()
		if value.Get("type").String() == "tool_use" {
			data = deleteBytes(data, "content."+idx+".caller")
		}
		return true
	})
	return data
}

// normalizeClaudeStreamEvent normalizes a single SSE event string for streaming.
// bedrockFormat preserves fields real Bedrock streams (cache_creation nesting,
// context_management echo) instead of stripping them.
func normalizeClaudeStreamEvent(data string, eventType string, bedrockFormat bool) string {
	switch eventType {
	case "message_start":
		if !bedrockFormat {
			data = deleteStr(data, "message.context_management")
			data = deleteStr(data, "message.usage.cache_creation")
		} else if cm := gjson.Get(data, "message.context_management"); cm.Exists() && len(cm.Get("applied_edits").Array()) == 0 {
			data = deleteStr(data, "message.context_management")
		}
		data = deleteStr(data, "message.input_transformations")
		data = deleteStr(data, "message.diagnostics")
		data = deleteStr(data, "message.safeguard_results")
		data = deleteStr(data, "message.stop_details")
		if gjson.Get(data, "message.usage.inference_geo").Exists() {
			if bedrockFormat {
				data = deleteStr(data, "message.usage.inference_geo")
			} else {
				data = setStr(data, "message.usage.inference_geo", "global")
			}
		}
		if !bedrockFormat && !gjson.Get(data, "message.container").Exists() {
			data = setStr(data, "message.container", nil)
		}
	case "message_delta":
		if !bedrockFormat {
			data = deleteStr(data, "context_management")
			data = deleteStr(data, "usage.cache_creation")
		} else if cm := gjson.Get(data, "context_management"); cm.Exists() && len(cm.Get("applied_edits").Array()) == 0 {
			data = deleteStr(data, "context_management")
		}
		data = deleteStr(data, "usage.iterations")
		data = deleteStr(data, "delta.stop_details")
		data = deleteStr(data, "delta.safeguard_results")
		if !gjson.Get(data, "delta.container").Exists() {
			data = setStr(data, "delta.container", nil)
		}
	case "content_block_delta":
		data = deleteStr(data, "delta.estimated_tokens")
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

	// Bedrock responses carry none of the Claude API-specific headers.
	if shouldBedrockFormat(info) {
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
	return info.ChannelSetting.NormalizeClaudeBody || info.ChannelSetting.ForceClaudeFormat || info.ChannelSetting.ForceBedrockFormat
}

func shouldNormalizeHeaders(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	return info.ChannelSetting.NormalizeClaudeHeaders || info.ChannelSetting.ForceClaudeFormat || info.ChannelSetting.ForceBedrockFormat
}

func shouldNormalizeTools(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	return info.ChannelSetting.NormalizeClaudeTools || info.ChannelSetting.ForceClaudeFormat || info.ChannelSetting.ForceBedrockFormat
}

func shouldBedrockFormat(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	return info.ChannelSetting.ForceBedrockFormat
}

func shouldApplyClaudeNormalize(info *relaycommon.RelayInfo) bool {
	return shouldNormalizeBody(info) || shouldNormalizeHeaders(info)
}

func IsStreamHeaderAllowed(name string) bool {
	return streamHeaderWhitelist[strings.ToLower(name)]
}

// --- Bedrock format overlay ---
// Applied after Claude normalization to make MAX responses match AWS Bedrock.

const bedrockMsgPrefix = "msg_bdrk_01"
const bedrockToolPrefix = "toolu_bdrk_01"

func rewriteIDToBedrock(id string, origPrefix, bedrockPrefix string) string {
	if strings.HasPrefix(id, origPrefix) {
		return bedrockPrefix + id[len(origPrefix):]
	}
	return id
}

func rewriteIDFromBedrock(id string, bedrockPrefix, origPrefix string) string {
	if strings.HasPrefix(id, bedrockPrefix) {
		return origPrefix + id[len(bedrockPrefix):]
	}
	return id
}

// bedrockTopLevelKeyOrder is the observed top-level key order of real
// Bedrock messages (1119 XDM/XJB captures): stop_details sits between
// stop_sequence and usage. sjson appends added keys at the object tail, so
// the overlay reorders once at exit. Keys outside the list keep their
// original relative order at the end; nested objects are untouched.
var bedrockTopLevelKeyOrder = []string{
	"model", "id", "type", "role", "content", "container",
	"stop_reason", "stop_sequence", "stop_details", "usage",
}

func reorderTopLevelJSON(data []byte) []byte {
	result := gjson.GetBytes(data, "@this")
	if !result.IsObject() {
		return data
	}
	var original []string
	values := map[string]string{}
	result.ForEach(func(key, value gjson.Result) bool {
		name := key.String()
		if _, seen := values[name]; !seen {
			original = append(original, name)
		}
		values[name] = value.Raw
		return true
	})
	ordered := make([]string, 0, len(original))
	placed := make(map[string]bool, len(bedrockTopLevelKeyOrder))
	for _, name := range bedrockTopLevelKeyOrder {
		if _, ok := values[name]; ok && !placed[name] {
			ordered = append(ordered, name)
			placed[name] = true
		}
	}
	for _, name := range original {
		if !placed[name] {
			ordered = append(ordered, name)
		}
	}
	parts := make([]string, 0, len(ordered))
	for _, name := range ordered {
		keyJSON, err := common.Marshal(name)
		if err != nil {
			return data
		}
		parts = append(parts, string(keyJSON)+":"+values[name])
	}
	return []byte("{" + strings.Join(parts, ",") + "}")
}

func bedrockOverlayBody(data []byte) []byte {
	if id := gjson.GetBytes(data, "id").String(); strings.HasPrefix(id, "msg_01") {
		data = setBytes(data, "id", rewriteIDToBedrock(id, "msg_01", bedrockMsgPrefix))
	}
	if !gjson.GetBytes(data, "stop_details").Exists() {
		data = setBytes(data, "stop_details", nil)
	}
	data = deleteBytes(data, "container")
	// Real Bedrock usage carries none of these keys (1117/1118 dual-reference
	// captures): null placeholders are themselves a first-party fingerprint.
	data = deleteBytes(data, "usage.inference_geo")
	data = deleteBytes(data, "usage.speed")
	data = deleteBytes(data, "usage.iterations")
	data = deleteBytes(data, "usage.server_tool_use")
	if !gjson.GetBytes(data, "usage.service_tier").Exists() {
		data = setBytes(data, "usage.service_tier", "standard")
	}
	if !gjson.GetBytes(data, "usage.cache_creation").Exists() {
		data = setBytes(data, "usage.cache_creation", map[string]int{
			"ephemeral_5m_input_tokens": 0,
			"ephemeral_1h_input_tokens": 0,
		})
	}
	// output_tokens_details appears only when thinking actually occurred.
	if otd := gjson.GetBytes(data, "usage.output_tokens_details"); !otd.Exists() || otd.Type == gjson.Null {
		data = deleteBytes(data, "usage.output_tokens_details")
	}
	gjson.GetBytes(data, "content").ForEach(func(key, value gjson.Result) bool {
		idx := key.String()
		if value.Get("type").String() == "tool_use" {
			if tid := value.Get("id").String(); strings.HasPrefix(tid, "toolu_01") {
				data = setBytes(data, "content."+idx+".id", rewriteIDToBedrock(tid, "toolu_01", bedrockToolPrefix))
			}
		}
		return true
	})
	return reorderTopLevelJSON(data)
}

func bedrockOverlayStreamEvent(data string, eventType string) string {
	switch eventType {
	case "message_start":
		if id := gjson.Get(data, "message.id").String(); strings.HasPrefix(id, "msg_01") {
			data = setStr(data, "message.id", rewriteIDToBedrock(id, "msg_01", bedrockMsgPrefix))
		}
		if !gjson.Get(data, "message.stop_details").Exists() {
			data = setStr(data, "message.stop_details", nil)
		}
		data = deleteStr(data, "message.container")
		// Bedrock streaming usage carries none of these keys at all.
		data = deleteStr(data, "message.usage.inference_geo")
		data = deleteStr(data, "message.usage.speed")
		data = deleteStr(data, "message.usage.iterations")
		data = deleteStr(data, "message.usage.server_tool_use")
		if otd := gjson.Get(data, "message.usage.output_tokens_details"); otd.Exists() && otd.Type == gjson.Null {
			data = deleteStr(data, "message.usage.output_tokens_details")
		}
		if !gjson.Get(data, "message.usage.service_tier").Exists() {
			data = setStr(data, "message.usage.service_tier", "standard")
		}
		if !gjson.Get(data, "message.usage.cache_creation").Exists() {
			data = setStr(data, "message.usage.cache_creation", map[string]int{
				"ephemeral_5m_input_tokens": 0,
				"ephemeral_1h_input_tokens": 0,
			})
		}
	case "content_block_start":
		if gjson.Get(data, "content_block.type").String() == "tool_use" {
			if tid := gjson.Get(data, "content_block.id").String(); strings.HasPrefix(tid, "toolu_01") {
				data = setStr(data, "content_block.id", rewriteIDToBedrock(tid, "toolu_01", bedrockToolPrefix))
			}
		}
	case "message_delta":
		if !gjson.Get(data, "delta.stop_details").Exists() {
			data = setStr(data, "delta.stop_details", nil)
		}
		data = deleteStr(data, "delta.container")
		if gjson.Get(data, "usage").Exists() {
			data = deleteStr(data, "usage.speed")
			data = deleteStr(data, "usage.iterations")
			data = deleteStr(data, "usage.server_tool_use")
			if otd := gjson.Get(data, "usage.output_tokens_details"); otd.Exists() && otd.Type == gjson.Null {
				data = deleteStr(data, "usage.output_tokens_details")
			}
			if !gjson.Get(data, "usage.service_tier").Exists() {
				data = setStr(data, "usage.service_tier", "standard")
			}
			if !gjson.Get(data, "usage.cache_creation").Exists() {
				data = setStr(data, "usage.cache_creation", map[string]int{
					"ephemeral_5m_input_tokens": 0,
					"ephemeral_1h_input_tokens": 0,
				})
			}
		}
	}
	return data
}

// bedrockMessageStop builds a message_stop event with real invocation metrics.
func bedrockMessageStop(inputTokens, outputTokens, cacheRead, cacheWrite int, firstByteMs, totalMs int64) string {
	data := `{"type":"message_stop"}`
	data = setStr(data, "amazon-bedrock-invocationMetrics", map[string]any{
		"inputTokenCount":           inputTokens,
		"outputTokenCount":          outputTokens,
		"invocationLatency":         totalMs,
		"firstByteLatency":          firstByteMs,
		"cacheReadInputTokenCount":  cacheRead,
		"cacheWriteInputTokenCount": cacheWrite,
	})
	return data
}

func WrapBedrockRequestError(apiErr *types.NewAPIError) *types.NewAPIError {
	if apiErr == nil {
		return nil
	}
	// The error may already be Bedrock-wrapped by another stage of the relay
	// pipeline; wrapping twice nests the SDK prefix and fingerprints the relay.
	if strings.Contains(apiErr.Error(), "operation error Bedrock Runtime") {
		return apiErr
	}
	var exType string
	switch {
	case apiErr.StatusCode == http.StatusBadRequest:
		exType = "ValidationException"
	case apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusUnauthorized:
		exType = "AccessDeniedException"
	case apiErr.StatusCode == http.StatusNotFound:
		exType = "ResourceNotFoundException"
	case apiErr.StatusCode == http.StatusTooManyRequests:
		exType = "ThrottlingException"
	case apiErr.StatusCode >= 500:
		exType = "InternalServerException"
	default:
		exType = "ValidationException"
	}
	reqID := uuid.New().String()
	msg := apiErr.Error()
	bedrockMsg := fmt.Sprintf("operation error Bedrock Runtime: InvokeModel, https response error StatusCode: %d, RequestID: %s, %s: %s",
		apiErr.StatusCode, reqID, exType, msg)
	return types.WithClaudeError(types.ClaudeError{Message: bedrockMsg}, apiErr.StatusCode)
}
