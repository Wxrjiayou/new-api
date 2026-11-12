package claude

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
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

var stripSignatureFieldTags = map[uint64]bool{14: true, 17: true, 22: true}

func stripMaxSignatureFields(sig string) string {
	if sig == "" {
		return sig
	}
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return sig
	}
	out, changed := stripProtoFieldsFromSignature(raw)
	if !changed {
		return sig
	}
	return base64.StdEncoding.EncodeToString(out)
}

func stripProtoFieldsFromSignature(data []byte) ([]byte, bool) {
	changed := false
	var result []byte
	pos := 0
	for pos < len(data) {
		fieldStart := pos
		tag, n := binary.Uvarint(data[pos:])
		if n <= 0 {
			return data, false
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		switch wireType {
		case 0: // varint
			for pos < len(data) && data[pos]&0x80 != 0 {
				pos++
			}
			if pos >= len(data) {
				return data, false
			}
			pos++
		case 2: // length-delimited
			length, n := binary.Uvarint(data[pos:])
			if n <= 0 {
				return data, false
			}
			pos += n
			if uint64(pos)+length > uint64(len(data)) {
				return data, false
			}
			if fieldNum == 2 {
				innerBody := data[pos : pos+int(length)]
				newInner, innerChanged := rebuildInnerField2(innerBody)
				if innerChanged {
					changed = true
					result = append(result, encodeTag(2, 2)...)
					result = append(result, encodeVarint(uint64(len(newInner)))...)
					result = append(result, newInner...)
					pos += int(length)
					continue
				}
			}
			pos += int(length)
		case 1: // 64-bit
			pos += 8
		case 5: // 32-bit
			pos += 4
		default:
			return data, false
		}
		if pos > len(data) {
			return data, false
		}
		result = append(result, data[fieldStart:pos]...)
	}
	return result, changed
}

func rebuildInnerField2(data []byte) ([]byte, bool) {
	changed := false
	var result []byte
	pos := 0
	for pos < len(data) {
		fieldStart := pos
		tag, n := binary.Uvarint(data[pos:])
		if n <= 0 {
			return data, false
		}
		pos += n
		fieldNum := tag >> 3
		wireType := tag & 0x7

		switch wireType {
		case 0: // varint
			for pos < len(data) && data[pos]&0x80 != 0 {
				pos++
			}
			if pos >= len(data) {
				return data, false
			}
			pos++
		case 2: // length-delimited
			length, n := binary.Uvarint(data[pos:])
			if n <= 0 {
				return data, false
			}
			pos += n
			if uint64(pos)+length > uint64(len(data)) {
				return data, false
			}
			if fieldNum == 1 {
				innerMsg := data[pos : pos+int(length)]
				newMsg, msgChanged := stripFieldsFromMessage(innerMsg, stripSignatureFieldTags)
				if msgChanged {
					changed = true
					result = append(result, encodeTag(1, 2)...)
					result = append(result, encodeVarint(uint64(len(newMsg)))...)
					result = append(result, newMsg...)
					pos += int(length)
					continue
				}
			}
			pos += int(length)
		case 1:
			pos += 8
		case 5:
			pos += 4
		default:
			return data, false
		}
		if pos > len(data) {
			return data, false
		}
		result = append(result, data[fieldStart:pos]...)
	}
	return result, changed
}

func stripFieldsFromMessage(data []byte, stripTags map[uint64]bool) ([]byte, bool) {
	changed := false
	var result []byte
	pos := 0
	for pos < len(data) {
		fieldStart := pos
		tag, n := binary.Uvarint(data[pos:])
		if n <= 0 {
			return data, false
		}
		pos += n
		wireType := tag & 0x7
		fieldNum := tag >> 3

		switch wireType {
		case 0:
			for pos < len(data) && data[pos]&0x80 != 0 {
				pos++
			}
			if pos >= len(data) {
				return data, false
			}
			pos++
		case 2:
			length, n := binary.Uvarint(data[pos:])
			if n <= 0 {
				return data, false
			}
			pos += n
			if uint64(pos)+length > uint64(len(data)) {
				return data, false
			}
			pos += int(length)
		case 1:
			pos += 8
		case 5:
			pos += 4
		default:
			return data, false
		}
		if pos > len(data) {
			return data, false
		}
		if stripTags[fieldNum] {
			changed = true
			continue
		}
		result = append(result, data[fieldStart:pos]...)
	}
	return result, changed
}

func encodeTag(fieldNum uint64, wireType uint64) []byte {
	return encodeVarint(fieldNum<<3 | wireType)
}

func encodeVarint(v uint64) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(buf, v)
	return buf[:n]
}

// normalizeClaudeBody normalizes a non-streaming Claude response body to match
// official API format: removes Max-specific fields, adds official-only fields.
func normalizeClaudeBody(data []byte) []byte {
	data = deleteBytes(data, "usage.iterations")
	data = deleteBytes(data, "usage.cache_creation")
	data = deleteBytes(data, "context_management")
	data = deleteBytes(data, "input_transformations")
	data = deleteBytes(data, "diagnostics")
	data = deleteBytes(data, "stop_details")
	if gjson.GetBytes(data, "usage.inference_geo").Exists() {
		data = setBytes(data, "usage.inference_geo", "global")
	}
	if !gjson.GetBytes(data, "container").Exists() {
		data = setBytes(data, "container", nil)
	}
	gjson.GetBytes(data, "content").ForEach(func(key, value gjson.Result) bool {
		if value.Get("type").String() == "thinking" && value.Get("signature").String() != "" {
			sig := value.Get("signature").String()
			if cleaned := stripMaxSignatureFields(sig); cleaned != sig {
				data = setBytes(data, "content."+key.String()+".signature", cleaned)
			}
		}
		return true
	})
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
		data = deleteStr(data, "message.stop_details")
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
		data = deleteStr(data, "delta.stop_details")
		if !gjson.Get(data, "delta.container").Exists() {
			data = setStr(data, "delta.container", nil)
		}
	case "content_block_delta":
		data = deleteStr(data, "delta.estimated_tokens")
		if gjson.Get(data, "delta.type").String() == "signature_delta" {
			sig := gjson.Get(data, "delta.signature").String()
			if cleaned := stripMaxSignatureFields(sig); cleaned != sig {
				data = setStr(data, "delta.signature", cleaned)
			}
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

func bedrockOverlayBody(data []byte) []byte {
	if id := gjson.GetBytes(data, "id").String(); strings.HasPrefix(id, "msg_01") {
		data = setBytes(data, "id", rewriteIDToBedrock(id, "msg_01", bedrockMsgPrefix))
	}
	if gjson.GetBytes(data, "stop_details").Type == gjson.Null {
		// already null from Claude normalize, keep it
	} else if !gjson.GetBytes(data, "stop_details").Exists() {
		data = setBytes(data, "stop_details", nil)
	}
	data = deleteBytes(data, "container")
	if gjson.GetBytes(data, "usage.inference_geo").Exists() {
		data = setBytes(data, "usage.inference_geo", "not_available")
	}
	if !gjson.GetBytes(data, "usage.service_tier").Exists() {
		data = setBytes(data, "usage.service_tier", "standard")
	}
	if gjson.GetBytes(data, "usage.speed").Type == gjson.Null || !gjson.GetBytes(data, "usage.speed").Exists() {
		data = setBytes(data, "usage.speed", nil)
	}
	if !gjson.GetBytes(data, "usage.iterations").Exists() {
		data = setBytes(data, "usage.iterations", nil)
	}
	if !gjson.GetBytes(data, "usage.output_tokens_details").Exists() {
		data = setBytes(data, "usage.output_tokens_details", nil)
	}
	if !gjson.GetBytes(data, "usage.server_tool_use").Exists() {
		data = setBytes(data, "usage.server_tool_use", nil)
	}
	gjson.GetBytes(data, "content").ForEach(func(key, value gjson.Result) bool {
		idx := key.String()
		if value.Get("type").String() == "tool_use" {
			if tid := value.Get("id").String(); strings.HasPrefix(tid, "toolu_01") {
				data = setBytes(data, "content."+idx+".id", rewriteIDToBedrock(tid, "toolu_01", bedrockToolPrefix))
			}
		}
		if value.Get("type").String() == "text" && !value.Get("citations").Exists() {
			data = setBytes(data, "content."+idx+".citations", nil)
		}
		return true
	})
	return data
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
		if gjson.Get(data, "message.usage.inference_geo").Exists() {
			data = setStr(data, "message.usage.inference_geo", "not_available")
		}
		if !gjson.Get(data, "message.usage.service_tier").Exists() {
			data = setStr(data, "message.usage.service_tier", "standard")
		}
	case "content_block_start":
		if gjson.Get(data, "content_block.type").String() == "tool_use" {
			if tid := gjson.Get(data, "content_block.id").String(); strings.HasPrefix(tid, "toolu_01") {
				data = setStr(data, "content_block.id", rewriteIDToBedrock(tid, "toolu_01", bedrockToolPrefix))
			}
		}
		if gjson.Get(data, "content_block.type").String() == "text" && !gjson.Get(data, "content_block.citations").Exists() {
			data = setStr(data, "content_block.citations", nil)
		}
	case "message_delta":
		if !gjson.Get(data, "delta.stop_details").Exists() {
			data = setStr(data, "delta.stop_details", nil)
		}
		data = deleteStr(data, "delta.container")
		if !gjson.Get(data, "delta.usage.service_tier").Exists() && gjson.Get(data, "usage").Exists() {
			data = setStr(data, "usage.service_tier", "standard")
		}
	case "message_stop":
		if !gjson.Get(data, "amazon-bedrock-invocationMetrics").Exists() {
			data = setStr(data, "amazon-bedrock-invocationMetrics", map[string]any{
				"inputTokenCount":           0,
				"outputTokenCount":          0,
				"invocationLatency":         0,
				"firstByteLatency":          0,
				"cacheReadInputTokenCount":  0,
				"cacheWriteInputTokenCount": 0,
			})
		}
	}
	return data
}
