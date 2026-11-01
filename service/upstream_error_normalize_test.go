package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func makeAPIError(msg string, statusCode int) *types.NewAPIError {
	return types.NewOpenAIError(errors.New(msg), types.ErrorCodeBadResponseStatusCode, statusCode)
}

func TestClassifyUpstreamError_GWhitelist(t *testing.T) {
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
			assert.Equal(t, actionPassthrough, classifyUpstreamError(tt.message, tt.status, true))
		})
	}
}

func TestClassifyUpstreamError_ASubscriptionLimit(t *testing.T) {
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
			assert.Equal(t, actionReplaceOverloaded, classifyUpstreamError(tt.message, 429, true))
		})
	}
}

func TestClassifyUpstreamError_BExtraUsage(t *testing.T) {
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
			assert.Equal(t, actionReplaceOverloaded, classifyUpstreamError(tt.message, 403, true))
		})
	}
}

func TestClassifyUpstreamError_CCredit(t *testing.T) {
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
			assert.Equal(t, actionReplaceOverloaded, classifyUpstreamError(tt.message, tt.status, true))
		})
	}
}

func TestClassifyUpstreamError_CInsufficientNot400(t *testing.T) {
	assert.Equal(t, actionReplaceOverloaded, classifyUpstreamError("insufficient quota", 403, true))
	// 400 + "insufficient" should NOT match C (to avoid eating "insufficient tokens in prompt")
	assert.Equal(t, actionPassthrough, classifyUpstreamError("insufficient tokens in prompt", 400, true))
}

func TestClassifyUpstreamError_CBillingNot400(t *testing.T) {
	assert.Equal(t, actionReplaceOverloaded, classifyUpstreamError("billing issue", 403, true))
	assert.Equal(t, actionPassthrough, classifyUpstreamError("billing parameter invalid", 400, true))
}

func TestClassifyUpstreamError_D2RefusalGuard(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"stopped locally", "Request stopped locally before forwarding to upstream"},
		{"refusal guard", "refusal guard triggered on this content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionReplaceOverloaded, classifyUpstreamError(tt.message, 403, true))
		})
	}
}

func TestClassifyUpstreamError_E400RateLimit(t *testing.T) {
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
			assert.Equal(t, actionReplaceOverloaded429, classifyUpstreamError(tt.message, 400, true))
		})
	}
}

func TestClassifyUpstreamError_ENon400NotMatched(t *testing.T) {
	assert.Equal(t, actionPassthrough, classifyUpstreamError("upstream rate limit exceeded", 429, true))
}

func TestClassifyUpstreamError_F2HTML(t *testing.T) {
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
			assert.Equal(t, actionReplaceInternalError, classifyUpstreamError(tt.message, 502, true))
		})
	}
}

func TestClassifyUpstreamError_UnknownPassthrough(t *testing.T) {
	assert.Equal(t, actionPassthrough, classifyUpstreamError("some random error message", 504, true))
}

func TestClassifyUpstreamError_GWhitelistTakesPriorityOverAB(t *testing.T) {
	// "token limit" is in G whitelist; even if "limit reached" (A档) also matches,
	// G档 takes priority because it's checked first.
	assert.Equal(t, actionPassthrough, classifyUpstreamError("token limit reached", 400, true))
}

func TestClassifyUpstreamError_402AlwaysC(t *testing.T) {
	// 402 triggers C档 regardless of message, as long as G档 doesn't match
	assert.Equal(t, actionReplaceOverloaded, classifyUpstreamError("random unknown message", 402, true))
}

func TestClassifyUpstreamError_MAXThirdPartyKeywords(t *testing.T) {
	tests := []struct {
		name    string
		message string
		status  int
	}{
		{"can't use claude code", "You can't use claude code with this account", 403},
		{"only authorized for claude code", "This key is only authorized for use with claude code", 400},
		{"stream ended no events", "stream ended without receiving any events", 400},
		{"previous_message_id", "Invalid previous_message_id in request", 400},
		{"when thread is set", "model is not supported when `thread` is set", 400},
		{"organization disabled", "Your organization has been disabled", 403},
		{"organization has disabled", "Your organization has disabled API access", 403},
		{"account disabled", "Your account has been disabled", 403},
		{"account on hold", "Your account is on hold", 403},
		{"oauth token", "Invalid oauth token provided", 401},
		{"oauth authentication", "oauth authentication is not supported", 400},
		{"please run /login", "Session expired, please run /login", 401},
		{"claude code generic", "This endpoint is reserved for claude code clients", 403},
		{"billing header", "Unknown header x-anthropic-billing-header", 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionReplaceOverloaded, classifyUpstreamError(tt.message, tt.status, true))
		})
	}
}

func TestClassifyUpstreamError_BedrockDeadUpstream(t *testing.T) {
	tests := []struct {
		name    string
		message string
		status  int
	}{
		{"deployment failed", "The deployment request could not be completed", 400},
		{"manages customer access", "AWS independently manages customer access to this service", 403},
		{"error code 555420", "Request failed with error code 555420", 500},
		{"bedrock not allowed", "access to bedrock models is not allowed for this account", 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, actionReplaceOverloaded, classifyUpstreamError(tt.message, tt.status, true))
		})
	}
}

func TestClassifyUpstreamError_ClaudeCodeWhitelistPriority(t *testing.T) {
	assert.Equal(t, actionPassthrough, classifyUpstreamError("input is too long for claude code model", 400, true))
	assert.Equal(t, actionPassthrough, classifyUpstreamError("token limit exceeded in claude code session", 400, true))
}

func TestIsClaudeUpstream(t *testing.T) {
	assert.True(t, IsClaudeUpstream(constant.APITypeAnthropic))
	assert.True(t, IsClaudeUpstream(constant.APITypeAws))
	assert.False(t, IsClaudeUpstream(constant.APITypeOpenAI))
	assert.False(t, IsClaudeUpstream(0))
}

func TestClassifyUpstreamError_ChineseBalanceAndUnpricedModel(t *testing.T) {
	tests := []struct {
		name    string
		message string
		status  int
		want    upstreamErrorAction
	}{
		{"balance 400", "账户余额不足，请充值后再试", 400, actionReplaceOverloaded},
		{"balance 500", "余额不足", 500, actionReplaceOverloaded},
		{"arrears 403", "账户已欠费", 403, actionReplaceOverloaded},
		{"recharge 400", "请先充值", 400, actionReplaceOverloaded},
		{"quota short 429", "用户额度不足", 429, actionReplaceOverloaded},
		{"unpriced model en", "model claude-x has not been priced by the administrator", 400, actionReplaceOverloaded},
		{"unpriced model zh", "模型 claude-x 的价格尚未由管理员配置", 500, actionReplaceOverloaded},
		{"chinese context too long passes through", "输入过长，超出上下文长度限制", 400, actionPassthrough},
		{"insufficient tokens in prompt 400 passes through", "insufficient tokens in prompt", 400, actionPassthrough},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, classifyUpstreamError(tt.message, tt.status, true))
		})
	}
}

func TestNormalizeUpstreamError(t *testing.T) {
	claudeUpstream := &relaycommon.ChannelMeta{ApiType: constant.APITypeAnthropic}
	geminiUpstream := &relaycommon.ChannelMeta{ApiType: constant.APITypeGemini}
	openAIUpstream := &relaycommon.ChannelMeta{ApiType: constant.APITypeOpenAI}
	tests := []struct {
		name       string
		format     types.RelayFormat
		meta       *relaycommon.ChannelMeta
		err        *types.NewAPIError
		wantStatus int
		wantType   string
		wantMsg    string
	}{
		{"claude client balance gets 529", types.RelayFormatClaude, claudeUpstream, makeAPIError("Your credit balance is too low", 400), 529, "overloaded_error", "Overloaded"},
		{"openai client on claude upstream gets 503", types.RelayFormatOpenAI, claudeUpstream, makeAPIError("You've reached your spend limit", 403), 503, "overloaded_error", "Overloaded"},
		{"gemini balance gets 503", types.RelayFormatGemini, geminiUpstream, makeAPIError("billing account disabled", 403), 503, "overloaded_error", "Overloaded"},
		{"openai chinese balance gets 503", types.RelayFormatOpenAI, openAIUpstream, makeAPIError("账户余额不足", 400), 503, "overloaded_error", "Overloaded"},
		{"openai 402 gets 503", types.RelayFormatOpenAI, openAIUpstream, makeAPIError("payment required", 402), 503, "overloaded_error", "Overloaded"},
		{"claude 400 rate limit gets 429", types.RelayFormatClaude, claudeUpstream, makeAPIError("upstream rate limit exceeded", 400), 429, "overloaded_error", "Overloaded"},
		{"claude html keeps status", types.RelayFormatClaude, claudeUpstream, makeAPIError("<html><body>502 Bad Gateway</body></html>", 502), 502, "api_error", "Internal server error"},
		{"any upstream 429 is hidden", types.RelayFormatOpenAI, openAIUpstream, makeAPIError("Rate limit reached for org-123", 429), 429, "", "Overloaded"},
		{"any upstream 503 is hidden", types.RelayFormatGemini, geminiUpstream, makeAPIError("The model is overloaded", 503), 503, "", "Overloaded"},
		{"claude client upstream 401 gets 529", types.RelayFormatClaude, openAIUpstream, makeAPIError("Incorrect API key provided", 401), 529, "overloaded_error", "Overloaded"},
		{"openai client upstream 401 gets 503", types.RelayFormatOpenAI, openAIUpstream, makeAPIError("invalid x-api-key", 401), 503, "overloaded_error", "Overloaded"},
		{"upstream 404 gets 503", types.RelayFormatGemini, geminiUpstream, makeAPIError("models/gemini-x is not found for API version v1beta", 404), 503, "overloaded_error", "Overloaded"},
		{"upstream 404 html page gets 529", types.RelayFormatClaude, claudeUpstream, makeAPIError("<html><body>404 Not Found</body></html>", 404), 529, "overloaded_error", "Overloaded"},
		{"claude client upstream 500 gets 529", types.RelayFormatClaude, openAIUpstream, makeAPIError("The server had an error while processing your request", 500), 529, "overloaded_error", "Overloaded"},
		{"openai client upstream 500 gets 503", types.RelayFormatOpenAI, openAIUpstream, makeAPIError("internal error", 500), 503, "overloaded_error", "Overloaded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{RelayFormat: tt.format, ChannelMeta: tt.meta}
			result := NormalizeUpstreamError(c, info, tt.err)
			require.NotSame(t, tt.err, result)
			assert.Equal(t, tt.wantStatus, result.StatusCode)
			assert.Equal(t, tt.wantMsg, result.Error())
			if tt.wantType != "" {
				assert.Equal(t, tt.wantType, result.ToClaudeError().Type)
			}
			wantRetryAfter := ""
			if tt.wantMsg == "Overloaded" {
				wantRetryAfter = "5"
			}
			assert.Equal(t, wantRetryAfter, UpstreamErrorRetryAfter(c, result))
			original, ok := common.GetContextKeyType[*types.NewAPIError](c, upstreamOriginalErrorKey)
			require.True(t, ok)
			assert.Same(t, tt.err, original, "the upstream error is kept for admins")
		})
	}
}

func TestNormalizeUpstreamErrorLeavesOtherErrorsUnchanged(t *testing.T) {
	openAIUpstream := &relaycommon.ChannelMeta{ApiType: constant.APITypeOpenAI}
	tests := []struct {
		name string
		info *relaycommon.RelayInfo
		err  *types.NewAPIError
	}{
		{"context overflow passes through", &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: openAIUpstream}, makeAPIError("prompt is too long", 400)},
		{"claude-only rules skip openai upstreams", &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, ChannelMeta: openAIUpstream}, makeAPIError("This key is only authorized for use with claude code", 403)},
		{"gateway quota errors reach the client", &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: openAIUpstream}, types.NewErrorWithStatusCode(errors.New("用户额度不足"), types.ErrorCodeInsufficientUserQuota, 403)},
		{"no channel yet", &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}, makeAPIError("some random error", 400)},
		{"no relay info", nil, makeAPIError("Your credit balance is too low", 400)},
		{"tools fetch 404 is the client's page", &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeToolsFetch, ChannelMeta: openAIUpstream}, makeAPIError("page not found", 404)},
		{"upstream 403 still passes through", &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: openAIUpstream}, makeAPIError("Request blocked by content policy", 403)},
		{"context overflow whitelist wins over 404", &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: openAIUpstream}, makeAPIError("prompt is too long: 250000 tokens", 404)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			assert.Same(t, tt.err, NormalizeUpstreamError(c, tt.info, tt.err))
			_, ok := common.GetContextKeyType[*types.NewAPIError](c, upstreamOriginalErrorKey)
			assert.False(t, ok)
		})
	}
	assert.Nil(t, NormalizeUpstreamError(nil, nil, nil))
}

// Retry-After belongs to the overloaded error the client receives, not to an
// error a later attempt returned after it.
func TestUpstreamErrorRetryAfterFollowsTheFinalError(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeOpenAI}}

	overloaded := NormalizeUpstreamError(c, info, makeAPIError("internal error", http.StatusInternalServerError))
	assert.Equal(t, "5", UpstreamErrorRetryAfter(c, overloaded))

	passedOn := NormalizeUpstreamError(c, info, makeAPIError("prompt is too long", http.StatusBadRequest))
	assert.Empty(t, UpstreamErrorRetryAfter(c, passedOn))

	noChannel := types.NewError(errors.New("The model is currently unavailable, please try again later"), types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
	assert.Empty(t, UpstreamErrorRetryAfter(c, noChannel))
	assert.Empty(t, UpstreamErrorRetryAfter(c, nil))
}

// An upstream error the client still sees keeps its status code but loses the
// identifiers that reveal the upstream; admins keep the original.
func TestNormalizeUpstreamErrorRedactsPassedOnErrors(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{"new-api relay request id", "upstream timeout (request id: 20261101123456789abcdef)", "upstream timeout"},
		{"bedrock arn", "Model arn:aws:bedrock:us-east-1:123456789012:inference-profile/claude is not ready", "Model arn:*** is not ready"},
		{"aws account id", "account 123456789012 is not authorized", "account *** is not authorized"},
		{"aws sdk request id", "https response error StatusCode: 500, RequestID: 6f1c2a9e-1b2c-4d5e-8f90-123456789abc, InternalServerException", "https response error StatusCode: 500, RequestID: ***, InternalServerException"},
		{"json trace id", `internal error {"trace_id":"abc123def456"}`, `internal error {"trace_id":"***"}`},
		{"anthropic request id", "Internal server error req_011CUa1b2c3d4e5f6g7h8i9j", "Internal server error req_***"},
		{"azure resource path", "resource /subscriptions/0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0/resourceGroups/prod-rg/providers/x failed", "resource /subscriptions/***/resourceGroups/***/providers/x failed"},
		{"gcp project", "projects/my-gcp-project/locations/us-central1/publishers/google/models/gemini failed", "projects/***/locations/us-central1/publishers/google/models/gemini failed"},
		{"bearer token", "rejected Bearer eyJhbGciOiJIUzI1NiJ9.payload", "rejected Bearer ***"},
		{"openai style key", "key sk-proj-abcdefghijklmnop1234 expired", "key sk-*** expired"},
	}
	openAIUpstream := &relaycommon.ChannelMeta{ApiType: constant.APITypeOpenAI}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, ChannelMeta: openAIUpstream}
			upstreamErr := types.WithOpenAIError(types.OpenAIError{Message: tt.message, Type: "server_error", Code: "server_error"}, http.StatusGatewayTimeout, types.ErrOptionWithSkipRetry())
			result := NormalizeUpstreamError(c, info, upstreamErr)
			require.NotSame(t, upstreamErr, result)
			assert.Equal(t, http.StatusGatewayTimeout, result.StatusCode)
			assert.Empty(t, UpstreamErrorRetryAfter(c, result), "only overloaded errors carry Retry-After")
			assert.Equal(t, tt.want, result.Error())
			assert.Equal(t, tt.want, result.ToOpenAIError().Message)
			assert.Equal(t, "server_error", result.ToOpenAIError().Type)
			assert.True(t, types.IsSkipRetryError(result), "retry options are kept")
			assert.Equal(t, tt.message, upstreamErr.Error(), "the upstream error is not modified")
			original, ok := common.GetContextKeyType[*types.NewAPIError](c, upstreamOriginalErrorKey)
			require.True(t, ok)
			assert.Same(t, upstreamErr, original)
		})
	}

	for _, message := range []string{
		"The request id is required",
		"input length 123456789012 exceeds the limit",
		"see https://docs.example.org/projects",
	} {
		upstreamErr := makeAPIError(message, http.StatusGatewayTimeout)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		assert.Same(t, upstreamErr, NormalizeUpstreamError(c, &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, ChannelMeta: openAIUpstream}, upstreamErr), message)
	}
}

// The client and the user's log see the normalized error, while admin_info and
// automatic disabling use what the upstream returned.
func TestProcessChannelErrorRecordsOriginalUpstreamErrorForAdmins(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousCache, previousRedis := common.MemoryCacheEnabled, common.RedisEnabled
	previousAutoDisable, previousErrorLog := common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled
	previousKeywords := operation_setting.AutomaticDisableKeywords
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.MemoryCacheEnabled, common.RedisEnabled = previousCache, previousRedis
		common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = previousAutoDisable, previousErrorLog
		operation_setting.AutomaticDisableKeywords = previousKeywords
	})
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, database.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = database, database
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled, common.RedisEnabled = false, false
	common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = true, true
	operation_setting.AutomaticDisableKeywordsFromString("Your credit balance is too low")
	channel := &model.Channel{Name: "claude-balance", Key: "fixture-key", Type: constant.ChannelTypeAnthropic, Status: common.ChannelStatusEnabled, Group: "default", Models: "claude-test"}
	require.NoError(t, channel.Insert())

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatClaude, ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeAnthropic}}
	normalized := NormalizeUpstreamError(c, info, makeAPIError("Your credit balance is too low", 400))
	ProcessChannelError(c, types.ChannelError{ChannelId: channel.Id, ChannelName: channel.Name, AutoBan: true}, normalized, info)

	var log model.Log
	require.NoError(t, database.Where("type = ?", model.LogTypeError).First(&log).Error)
	assert.Equal(t, "status_code=529, Overloaded", log.Content)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	assert.EqualValues(t, 529, other["status_code"])
	adminInfo, ok := other["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"status_code": float64(400),
		"error_type":  string(types.ErrorTypeOpenAIError),
		"error_code":  string(types.ErrorCodeBadResponseStatusCode),
		"message":     "Your credit balance is too low",
	}, adminInfo["upstream_error"])

	require.Eventually(t, func() bool {
		loaded, err := model.GetChannelById(channel.Id, true)
		return err == nil && loaded.Status == common.ChannelStatusAutoDisabled
	}, 5*time.Second, 20*time.Millisecond, "the balance error disables the channel although the client saw Overloaded")

	_, ok = common.GetContextKeyType[*types.NewAPIError](c, upstreamOriginalErrorKey)
	assert.True(t, ok)
	original, _ := common.GetContextKeyType[*types.NewAPIError](c, upstreamOriginalErrorKey)
	assert.Nil(t, original, "the next attempt does not report this attempt's upstream error")
}
