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

func removeClaudeIterations(data []byte) []byte {
	if !gjson.GetBytes(data, "usage.iterations").Exists() {
		return data
	}
	result, err := sjson.DeleteBytes(data, "usage.iterations")
	if err != nil {
		return data
	}
	return result
}

func removeClaudeIterationsStr(data string) string {
	if !gjson.Get(data, "usage.iterations").Exists() {
		return data
	}
	result, err := sjson.Delete(data, "usage.iterations")
	if err != nil {
		return data
	}
	return result
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

func shouldApplyClaudeNormalize(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil && info.ChannelSetting.ForceClaudeFormat
}

func IsStreamHeaderAllowed(name string) bool {
	return streamHeaderWhitelist[strings.ToLower(name)]
}
