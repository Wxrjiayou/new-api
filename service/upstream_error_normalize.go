package service

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// upstreamOriginalErrorKey holds the upstream error NormalizeUpstreamError
// replaced, so ProcessChannelError can log it for admins and judge channel
// disabling on what the upstream actually returned.
const upstreamOriginalErrorKey constant.ContextKey = "upstream_original_error"

// upstreamOverloadedErrorKey holds the overloaded error NormalizeUpstreamError
// returned last. The relay sends Retry-After only when that error is the one
// the client receives, so a later attempt's own error or a success never
// carries it.
const upstreamOverloadedErrorKey constant.ContextKey = "upstream_overloaded_error"

// overloadedRetryAfterSeconds tells clients when to retry an overloaded error.
const overloadedRetryAfterSeconds = "5"

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

	// An upstream 401 or 404 is about the channel's key or model mapping, and
	// an upstream 500 about the upstream itself; the client can fix none of
	// them. A passed-on 401 makes Claude Code ask the user to log in again.
	switch statusCode {
	case http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError:
		return actionReplaceOverloaded
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
// SDK retries. Overloaded errors are sent with Retry-After, see
// UpstreamErrorRetryAfter. An upstream error passed on keeps its status code
// with the upstream identifiers redacted. The replaced error is kept for
// ProcessChannelError.
func NormalizeUpstreamError(c *gin.Context, info *relaycommon.RelayInfo, apiErr *types.NewAPIError) *types.NewAPIError {
	// Errors the gateway raises itself, such as the user's own quota running
	// out, are not upstream errors and must reach the client unchanged.
	if apiErr == nil || info == nil || apiErr.GetErrorType() == types.ErrorTypeNewAPIError {
		return apiErr
	}

	var normalized *types.NewAPIError
	overloaded := false
	status := apiErr.StatusCode
	switch {
	case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable:
		normalized = types.NewOpenAIError(errors.New("Overloaded"), types.ErrorCodeBadResponseStatusCode, status)
		overloaded = true
	case status == http.StatusNotFound && info.RelayMode == relayconstant.RelayModeToolsFetch:
		// The page the client asked /v1/tools/fetch for does not exist.
		normalized = redactUpstreamErrorMessage(apiErr)
	default:
		overloadedStatus := http.StatusServiceUnavailable
		if info.RelayFormat == types.RelayFormatClaude {
			overloadedStatus = 529
		}
		claudeRules := info.RelayFormat == types.RelayFormatClaude || (info.ChannelMeta != nil && IsClaudeUpstream(info.ApiType))
		switch classifyUpstreamError(apiErr.Error(), status, claudeRules) {
		case actionReplaceOverloaded:
			normalized = types.WithClaudeError(types.ClaudeError{Type: "overloaded_error", Message: "Overloaded"}, overloadedStatus)
			overloaded = true
		case actionReplaceOverloaded429:
			normalized = types.WithClaudeError(types.ClaudeError{Type: "overloaded_error", Message: "Overloaded"}, http.StatusTooManyRequests)
			overloaded = true
		case actionReplaceInternalError:
			normalized = types.WithClaudeError(types.ClaudeError{Type: "api_error", Message: "Internal server error"}, status)
		default:
			normalized = redactUpstreamErrorMessage(apiErr)
		}
	}
	if normalized == apiErr {
		return apiErr
	}
	common.SetContextKey(c, upstreamOriginalErrorKey, apiErr)
	if overloaded {
		common.SetContextKey(c, upstreamOverloadedErrorKey, normalized)
	}
	if info.ChannelMeta != nil && info.ChannelSetting.ForceBedrockFormat {
		normalized = wrapBedrockErrorFormat(normalized)
	}
	return normalized
}

// UpstreamErrorRetryAfter returns the Retry-After value for the error the
// client receives, or "" when it is not an overloaded error from
// NormalizeUpstreamError.
func UpstreamErrorRetryAfter(c *gin.Context, apiErr *types.NewAPIError) string {
	overloaded, ok := common.GetContextKeyType[*types.NewAPIError](c, upstreamOverloadedErrorKey)
	if !ok || overloaded == nil || overloaded != apiErr {
		return ""
	}
	return overloadedRetryAfterSeconds
}

// upstreamErrorRedactions remove what an upstream error the client still sees
// reveals about the upstream: relay request ids, cloud account and resource
// identifiers, and credentials. Upstream addresses are masked later, when the
// error is written, by kitutil.MaskSensitiveInfo.
var upstreamErrorRedactions = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	// Relays built on new-api append their own request id, which names the relay.
	{regexp.MustCompile(`\s*\(request id: [^)]*\)`), ""},
	{regexp.MustCompile(`arn:aws[\w-]*:[^\s"'),;]+`), "arn:***"},
	{regexp.MustCompile(`(?i)\b(account(?:[ _-]?id)?[\s:=#"']*)\d{12}\b`), "${1}***"},
	{regexp.MustCompile(`(?i)\b((?:request|trace|correlation)[ _-]?id["']?\s*[:=]\s*["']?)[\w.:-]{6,}`), "${1}***"},
	{regexp.MustCompile(`\breq_[A-Za-z0-9]{16,}\b`), "req_***"},
	{regexp.MustCompile(`(?i)/subscriptions/[0-9a-f-]{36}`), "/subscriptions/***"},
	{regexp.MustCompile(`(?i)/resourceGroups/[^/\s"']+`), "/resourceGroups/***"},
	{regexp.MustCompile(`\bprojects/[^/\s"']+`), "projects/***"},
	{regexp.MustCompile(`(?i)\bbearer\s+[\w.~+/=-]{8,}`), "Bearer ***"},
	{regexp.MustCompile(`\bsk-[\w-]{16,}`), "sk-***"},
}

func redactUpstreamErrorText(text string) string {
	for _, redaction := range upstreamErrorRedactions {
		text = redaction.pattern.ReplaceAllString(text, redaction.replacement)
	}
	return text
}

// redactUpstreamErrorMessage returns apiErr when nothing needs redacting, and
// otherwise a copy with the redacted message. The copy keeps the status code
// and retry options; the OpenAI error a client receives is built from
// RelayError, so its message is redacted too.
func redactUpstreamErrorMessage(apiErr *types.NewAPIError) *types.NewAPIError {
	message := apiErr.Error()
	redacted := *apiErr
	changed := false
	if redactedMessage := redactUpstreamErrorText(message); redactedMessage != message {
		redacted.Err = errors.New(redactedMessage)
		changed = true
	}
	switch relayErr := apiErr.RelayError.(type) {
	case types.OpenAIError:
		if redactedMessage := redactUpstreamErrorText(relayErr.Message); redactedMessage != relayErr.Message {
			relayErr.Message = redactedMessage
			redacted.RelayError = relayErr
			changed = true
		}
	case types.ClaudeError:
		if redactedMessage := redactUpstreamErrorText(relayErr.Message); redactedMessage != relayErr.Message {
			relayErr.Message = redactedMessage
			redacted.RelayError = relayErr
			changed = true
		}
	}
	if !changed {
		return apiErr
	}
	return &redacted
}

func bedrockExceptionType(status int) string {
	switch {
	case status == http.StatusBadRequest:
		return "ValidationException"
	case status == http.StatusForbidden || status == http.StatusUnauthorized:
		return "AccessDeniedException"
	case status == http.StatusNotFound:
		return "ResourceNotFoundException"
	case status == http.StatusTooManyRequests:
		return "ThrottlingException"
	case status >= 500:
		return "InternalServerException"
	default:
		return "ValidationException"
	}
}

func wrapBedrockErrorFormat(apiErr *types.NewAPIError) *types.NewAPIError {
	if apiErr == nil {
		return nil
	}
	requestID := uuid.New().String()
	exType := bedrockExceptionType(apiErr.StatusCode)
	msg := apiErr.Error()
	bedrockMsg := fmt.Sprintf("operation error Bedrock Runtime: InvokeModel, https response error StatusCode: %d, RequestID: %s, %s: %s",
		apiErr.StatusCode, requestID, exType, msg)
	wrapped := *apiErr
	wrapped.Err = errors.New(bedrockMsg)
	wrapped.RelayError = types.ClaudeError{Message: bedrockMsg}
	return &wrapped
}
