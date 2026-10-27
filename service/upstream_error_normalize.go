package service

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// upstreamOriginalErrorKey holds the upstream error NormalizeUpstreamError
// replaced, so ProcessChannelError can log it for admins and judge channel
// disabling on what the upstream actually returned.
const upstreamOriginalErrorKey constant.ContextKey = "upstream_original_error"

func IsClaudeUpstream(apiType int) bool {
	switch apiType {
	case constant.APITypeAnthropic, constant.APITypeAws:
		return true
	default:
		return false
	}
}

type upstreamErrorAction int

const (
	actionPassthrough upstreamErrorAction = iota
	actionReplaceOverloaded
	actionReplaceOverloaded429
	actionReplaceInternalError
)

var gWhitelistKeywords = []string{
	"input is too long",
	"too many tokens",
	"token limit",
	"context length",
	"maximum context",
	"exceeds the model",
	"content length exceeds",
	"prompt is too long",
	"request too large",
	"payload too large",
	"max_tokens",
	"invalid signature",
	"does not support oneof",
	"does not support allof",
	"does not support anyof",
	"tool_result block",
	"image exceeds",
	"上下文长度",
	"超出上下文",
	"输入过长",
}

var abKeywords = []string{
	"switch to another model",
	"spend limit",
	"shared budget",
	"usage credits",
	"limit reached",
	"extra usage",
	"not your plan limits",
	"claude.ai/settings/usage",
	"with the claude pro plan",
	"may not be enabled for your organization",
	"restricted by your organization",
	"all upstreams failed",
	"can't use claude code",
	"only authorized for use with claude code",
	"stream ended without receiving any events",
	"previous_message_id",
	"when `thread` is set",
	"organization has been disabled",
	"organization has disabled",
	"account has been disabled",
	"account is on hold",
	"oauth token",
	"oauth authentication",
	"please run /login",
	"claude code",
	"x-anthropic-billing-header",
}

var abRegexes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:reached|hit) your .{0,40}limit`),
}

var cKeywordsAlways = []string{
	"usage limit",
	"wholesale credit",
	"add credits",
	"add additional credits",
	"top up",
	"topup",
	"out of credit",
	"credit balance is too low",
	"purchase credits",
	"quota exceeded",
	"payment required",
	"operation not allowed",
	"余额",
	"欠费",
	"充值",
	"额度不足",
}

var cKeywordsNon400 = []string{
	"insufficient",
	"billing",
}

var d2Keywords = []string{
	"stopped locally before forwarding",
	"refusal guard",
}

var bedrockDeadKeywords = []string{
	"deployment request could not be completed",
	"independently manages customer access",
	"555420",
	"access to bedrock models is not allowed",
}

// Relays that list a model they cannot price will fail every request for it.
var unpricedModelKeywords = []string{
	"has not been priced by the administrator",
	"尚未由管理员配置",
}

var eKeywords400 = []string{
	"upstream rate limit exceeded",
}

var eRegexes400 = []*regexp.Regexp{
	regexp.MustCompile(`(?i)exceed(?:ed|s)? your (?:organization|account|org)'?s? rate limit`),
}

// classifyUpstreamError decides what the client sees for an upstream error.
// The balance rules apply to every upstream; the account, plan, relay and
// rate-limit rules were written for Claude and apply only with claudeRules.
func classifyUpstreamError(message string, statusCode int, claudeRules bool) upstreamErrorAction {
	lower := strings.ToLower(message)

	for _, kw := range gWhitelistKeywords {
		if strings.Contains(lower, kw) {
			return actionPassthrough
		}
	}

	if statusCode == http.StatusPaymentRequired {
		return actionReplaceOverloaded
	}
	for _, kw := range cKeywordsAlways {
		if strings.Contains(lower, kw) {
			return actionReplaceOverloaded
		}
	}
	if statusCode != http.StatusBadRequest {
		for _, kw := range cKeywordsNon400 {
			if strings.Contains(lower, kw) {
				return actionReplaceOverloaded
			}
		}
	}

	if !claudeRules {
		return actionPassthrough
	}

	for _, kw := range abKeywords {
		if strings.Contains(lower, kw) {
			return actionReplaceOverloaded
		}
	}
	for _, re := range abRegexes {
		if re.MatchString(message) {
			return actionReplaceOverloaded
		}
	}

	for _, kw := range d2Keywords {
		if strings.Contains(lower, kw) {
			return actionReplaceOverloaded
		}
	}

	for _, kw := range bedrockDeadKeywords {
		if strings.Contains(lower, kw) {
			return actionReplaceOverloaded
		}
	}
	for _, kw := range unpricedModelKeywords {
		if strings.Contains(lower, kw) {
			return actionReplaceOverloaded
		}
	}

	if statusCode == http.StatusBadRequest {
		for _, kw := range eKeywords400 {
			if strings.Contains(lower, kw) {
				return actionReplaceOverloaded429
			}
		}
		for _, re := range eRegexes400 {
			if re.MatchString(message) {
				return actionReplaceOverloaded429
			}
		}
	}

	trimmed := strings.TrimLeft(lower, " \t\r\n")
	if strings.HasPrefix(trimmed, "<html") || strings.HasPrefix(trimmed, "<!doctype") {
		return actionReplaceInternalError
	}

	return actionPassthrough
}

// NormalizeUpstreamError hides upstream errors that reveal the upstream's
// balance, account or load behind a generic overloaded error that clients
// retry. Claude clients get 529; other clients get 503, which every official
// SDK retries. The replaced error is kept for ProcessChannelError.
func NormalizeUpstreamError(c *gin.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) *types.NewAPIError {
	// Errors the gateway raises itself, such as the user's own quota running
	// out, are not upstream errors and must reach the client unchanged.
	if apiErr == nil || info == nil || apiErr.GetErrorType() == types.ErrorTypeNewAPIError {
		return apiErr
	}

	var normalized *types.NewAPIError
	status := apiErr.StatusCode
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		normalized = types.NewOpenAIError(errors.New("Overloaded"), types.ErrorCodeBadResponseStatusCode, status)
	} else {
		overloadedStatus := http.StatusServiceUnavailable
		if info.RelayFormat == types.RelayFormatClaude {
			overloadedStatus = 529
		}
		claudeRules := info.RelayFormat == types.RelayFormatClaude || (info.ChannelMeta != nil && IsClaudeUpstream(info.ApiType))
		switch classifyUpstreamError(apiErr.Error(), status, claudeRules) {
		case actionReplaceOverloaded:
			normalized = types.WithClaudeError(types.ClaudeError{Type: "overloaded_error", Message: "Overloaded"}, overloadedStatus)
		case actionReplaceOverloaded429:
			normalized = types.WithClaudeError(types.ClaudeError{Type: "overloaded_error", Message: "Overloaded"}, http.StatusTooManyRequests)
		case actionReplaceInternalError:
			normalized = types.WithClaudeError(types.ClaudeError{Type: "api_error", Message: "Internal server error"}, status)
		default:
			return apiErr
		}
	}
	common.SetContextKey(c, upstreamOriginalErrorKey, apiErr)
	return normalized
}
