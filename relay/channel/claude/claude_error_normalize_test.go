package claude

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeAPIError(msg string, statusCode int) *types.NewAPIError {
	return types.NewOpenAIError(errors.New(msg), types.ErrorCodeBadResponseStatusCode, statusCode)
}

func TestClassifyClaudeError_GWhitelist(t *testing.T) {
	tests := []struct {
		name    string
		message string
		status  int
	}{
		{"input too long", "Your input is too long for model claude-3.5-sonnet", 400},
		{"too many tokens", "too many tokens: 200000", 400},
		{"token limit", "token limit exceeded", 400},
		{"context length", "context length exceeded", 400},
		{"maximum context", "maximum context window", 400},
		{"exceeds the model", "exceeds the model's maximum", 400},
		{"prompt too long", "prompt is too long", 400},
		{"request too large", "request too large", 413},
		{"payload too large", "payload too large", 413},
		{"max_tokens", "max_tokens must be > 0", 400},
		{"invalid signature", "invalid signature in thinking block", 400},
		{"oneof schema", "does not support oneOf", 400},
		{"tool_result block", "tool_result block has wrong format", 400},
		{"image exceeds", "image exceeds maximum size", 400},
		{"content length exceeds", "content length exceeds limit", 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionPassthrough, classifyClaudeError(tt.message, tt.status))
		})
	}
}

func TestClassifyClaudeError_ASubscriptionLimit(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"switch model", "Please switch to another model"},
		{"spend limit", "You've reached your spend limit"},
		{"shared budget", "shared budget has been exhausted"},
		{"usage credits", "You have run out of usage credits"},
		{"limit reached", "Rate limit reached for this model"},
		{"regex hit limit", "You've reached your organization's daily limit"},
		{"regex reached limit", "You've hit your monthly limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionReplaceOverloaded, classifyClaudeError(tt.message, 429))
		})
	}
}

func TestClassifyClaudeError_BExtraUsage(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"extra usage", "This requires extra usage billing"},
		{"plan limits", "These are not your plan limits"},
		{"settings url", "Visit claude.ai/settings/usage to check"},
		{"pro plan", "Available with the claude pro plan"},
		{"not enabled", "This feature may not be enabled for your organization"},
		{"restricted", "Access restricted by your organization policy"},
		{"all upstreams", "all upstreams failed to respond"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionReplaceOverloaded, classifyClaudeError(tt.message, 403))
		})
	}
}

func TestClassifyClaudeError_CCredit(t *testing.T) {
	tests := []struct {
		name    string
		message string
		status  int
	}{
		{"402 any message", "Payment required", 402},
		{"credit balance", "Your credit balance is too low", 403},
		{"out of credit", "You are out of credit", 403},
		{"quota exceeded", "API quota exceeded", 429},
		{"purchase credits", "Please purchase credits to continue", 403},
		{"add credits", "Please add credits", 403},
		{"usage limit", "usage limit reached", 429},
		{"operation not allowed", "operation not allowed", 403},
		{"insufficient non-400", "insufficient quota", 403},
		{"billing non-400", "billing account suspended", 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionReplaceOverloaded, classifyClaudeError(tt.message, tt.status))
		})
	}
}

func TestClassifyClaudeError_CInsufficientNot400(t *testing.T) {
	assert.Equal(t, actionReplaceOverloaded, classifyClaudeError("insufficient quota", 403))
	// 400 + "insufficient" should NOT match C (to avoid eating "insufficient tokens in prompt")
	assert.Equal(t, actionPassthrough, classifyClaudeError("insufficient tokens in prompt", 400))
}

func TestClassifyClaudeError_CBillingNot400(t *testing.T) {
	assert.Equal(t, actionReplaceOverloaded, classifyClaudeError("billing issue", 403))
	assert.Equal(t, actionPassthrough, classifyClaudeError("billing parameter invalid", 400))
}

func TestClassifyClaudeError_D2RefusalGuard(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"stopped locally", "Request stopped locally before forwarding to upstream"},
		{"refusal guard", "refusal guard triggered on this content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionReplaceOverloaded, classifyClaudeError(tt.message, 403))
		})
	}
}

func TestClassifyClaudeError_E400RateLimit(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"upstream rate limit", "upstream rate limit exceeded"},
		{"org rate limit regex", "You've exceeded your organization's rate limit"},
		{"account rate limit regex", "Request exceeds your account's rate limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionReplaceOverloaded429, classifyClaudeError(tt.message, 400))
		})
	}
}

func TestClassifyClaudeError_ENon400NotMatched(t *testing.T) {
	assert.Equal(t, actionPassthrough, classifyClaudeError("upstream rate limit exceeded", 429))
}

func TestClassifyClaudeError_F2HTML(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"html tag", "<html><body>502 Bad Gateway</body></html>"},
		{"doctype", "<!DOCTYPE html><html><body>error</body></html>"},
		{"leading whitespace", "  \n<html><body>error</body></html>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionReplaceInternalError, classifyClaudeError(tt.message, 502))
		})
	}
}

func TestClassifyClaudeError_UnknownPassthrough(t *testing.T) {
	assert.Equal(t, actionPassthrough, classifyClaudeError("some random error message", 500))
}

func TestNormalizeClaudeError_Nil(t *testing.T) {
	assert.Nil(t, NormalizeClaudeError(nil, 500))
}

func TestNormalizeClaudeError_Passthrough(t *testing.T) {
	err := makeAPIError("input is too long for this model", 400)
	result := NormalizeClaudeError(err, 400)
	assert.Equal(t, err, result)
}

func TestNormalizeClaudeError_OverloadedReplacement(t *testing.T) {
	err := makeAPIError("You've reached your spend limit", 429)
	result := NormalizeClaudeError(err, 429)
	require.NotNil(t, result)
	assert.Equal(t, 529, result.StatusCode)
	assert.Equal(t, "Overloaded", result.Error())

	claudeErr := result.ToClaudeError()
	assert.Equal(t, "overloaded_error", claudeErr.Type)
	assert.Equal(t, "Overloaded", claudeErr.Message)
}

func TestNormalizeClaudeError_Overloaded429Replacement(t *testing.T) {
	err := makeAPIError("upstream rate limit exceeded", 400)
	result := NormalizeClaudeError(err, 400)
	require.NotNil(t, result)
	assert.Equal(t, 429, result.StatusCode)
	assert.Equal(t, "Overloaded", result.Error())
}

func TestNormalizeClaudeError_InternalErrorReplacement(t *testing.T) {
	err := makeAPIError("<html><body>502 Bad Gateway</body></html>", 502)
	result := NormalizeClaudeError(err, 502)
	require.NotNil(t, result)
	assert.Equal(t, 502, result.StatusCode)
	assert.Equal(t, "Internal server error", result.Error())

	claudeErr := result.ToClaudeError()
	assert.Equal(t, "api_error", claudeErr.Type)
}

func TestClassifyClaudeError_GWhitelistTakesPriorityOverAB(t *testing.T) {
	// "token limit" is in G whitelist; even if "limit reached" (A档) also matches,
	// G档 takes priority because it's checked first.
	assert.Equal(t, actionPassthrough, classifyClaudeError("token limit reached", 400))
}

func TestClassifyClaudeError_402AlwaysC(t *testing.T) {
	// 402 triggers C档 regardless of message, as long as G档 doesn't match
	assert.Equal(t, actionReplaceOverloaded, classifyClaudeError("random unknown message", 402))
}

func TestNormalizeClaudeError_PreservesOriginalStatusForHTML(t *testing.T) {
	err := makeAPIError("<!doctype html><html>503</html>", 503)
	result := NormalizeClaudeError(err, 503)
	assert.Equal(t, 503, result.StatusCode)
}
