package claude

import (
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
)

func IsClaudeUpstream(apiType int) bool {
	switch apiType {
	case constant.APITypeAnthropic, constant.APITypeAws:
		return true
	default:
		return false
	}
}

type claudeErrorAction int

const (
	actionPassthrough claudeErrorAction = iota
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

var eKeywords400 = []string{
	"upstream rate limit exceeded",
}

var eRegexes400 = []*regexp.Regexp{
	regexp.MustCompile(`(?i)exceed(?:ed|s)? your (?:organization|account|org)'?s? rate limit`),
}

func classifyClaudeError(message string, statusCode int) claudeErrorAction {
	lower := strings.ToLower(message)

	for _, kw := range gWhitelistKeywords {
		if strings.Contains(lower, kw) {
			return actionPassthrough
		}
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

	if statusCode == 402 {
		return actionReplaceOverloaded
	}
	for _, kw := range cKeywordsAlways {
		if strings.Contains(lower, kw) {
			return actionReplaceOverloaded
		}
	}
	if statusCode != 400 {
		for _, kw := range cKeywordsNon400 {
			if strings.Contains(lower, kw) {
				return actionReplaceOverloaded
			}
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

	if statusCode == 400 {
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

func NormalizeClaudeError(apiErr *types.NewAPIError, upstreamStatusCode int) *types.NewAPIError {
	if apiErr == nil {
		return apiErr
	}

	message := apiErr.Error()
	action := classifyClaudeError(message, upstreamStatusCode)

	switch action {
	case actionPassthrough:
		return apiErr
	case actionReplaceOverloaded:
		return types.WithClaudeError(types.ClaudeError{
			Type:    "overloaded_error",
			Message: "Overloaded",
		}, 529)
	case actionReplaceOverloaded429:
		return types.WithClaudeError(types.ClaudeError{
			Type:    "overloaded_error",
			Message: "Overloaded",
		}, 429)
	case actionReplaceInternalError:
		return types.WithClaudeError(types.ClaudeError{
			Type:    "api_error",
			Message: "Internal server error",
		}, apiErr.StatusCode)
	}
	return apiErr
}
