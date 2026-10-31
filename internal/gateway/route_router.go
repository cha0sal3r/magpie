package gateway

// Routing-strategy logic: the pure scheduling decisions made about one
// upstream reply, driven by a provider.RoutePolicy so any provider can tune
// them. route.go holds the per-provider cooldown and throttle state;
// route_fallback.go plans the model family. What remains here is the
// SSE-frame classifier that decides retry vs. fatal before any content is
// sent, and the input truncation that retries a too-long request under a
// smaller token budget.

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cha0sal3r/magpie-friday/internal/provider"
)

// parseRouteFrame splits one SSE frame into its event name and data body.
func parseRouteFrame(frame []byte) (string, map[string]any) {
	event := ""
	var data []string
	for _, line := range strings.Split(string(frame), "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	payload := strings.Join(data, "\n")
	if strings.TrimSpace(payload) == "[DONE]" {
		return "done", nil
	}
	var body any
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return event, nil
	}
	m, ok := body.(map[string]any)
	if !ok {
		return event, nil
	}
	if _, hasErr := m["error"]; hasErr {
		return "error", m
	}
	if event != "" {
		return event, m
	}
	if obj, ok := m["object"].(string); ok {
		return obj, m
	}
	return "", m
}

// classifyRouteFrame returns (outcome, cooldown, appError): "retry" moves
// to the next model while nothing of the reply has been sent, "fatal"
// rejects the request outright, and "content"/"done"/"skip" pass it
// through. A 429 always retries and cools; the policy's own error types,
// keywords and status codes decide the rest.
func classifyRouteFrame(pol provider.RoutePolicy, frame []byte) (outcome string, cooldown bool, appError bool) {
	event, body := parseRouteFrame(frame)
	if event == "done" {
		return "done", false, false
	}
	if body == nil {
		return "skip", false, false
	}
	if event == "error" {
		err, _ := body["error"].(map[string]any)
		if err == nil {
			err = map[string]any{}
		}
		text := fmt.Sprint(err["message"])
		code := routeErrCode(err)
		isLimit := routeNumEq(code, 429) || fmt.Sprint(code) == "429" || strings.Contains(text, "429")
		errType, _ := err["type"].(string)
		temporary := isLimit || slices.Contains(pol.Classify.RetryErrorTypes, errType) ||
			containsAnyFold(text, pol.Classify.RetryKeywords...)
		if temporary || routeCodeIn(pol.Classify.RetryStatusCodes, code) {
			return "retry", isLimit, true
		}
		return "fatal", isLimit, true
	}
	if created, _ := body["created"].(float64); created == 0 && routeFalsy(body["id"]) {
		return "retry", false, true
	}
	delta, finishReason := routeChoiceDelta(body)
	text := fmt.Sprint(delta["content"])
	if strings.HasPrefix(text, "[Error]") {
		lower := strings.ToLower(text)
		temporary := containsAnyFold(lower, pol.Classify.ChunkErrorRetryKeywords...)
		if temporary {
			return "retry", strings.Contains(text, "429"), true
		}
		return "fatal", strings.Contains(text, "429"), true
	}
	if routeDeltaSays(delta) || routeTruthy(delta["tool_calls"]) {
		return "content", false, false
	}
	if finishReason != nil {
		return "done", false, false
	}
	return "skip", false, false
}

// routeCodeIn is whether a decoded JSON code equals one of the integers.
func routeCodeIn(codes []int, code any) bool {
	for _, n := range codes {
		if routeNumEq(code, n) || fmt.Sprint(code) == fmt.Sprint(n) {
			return true
		}
	}
	return false
}

// routeErrCode is the first of status, status_code or code the error
// names, as the Router reads them.
func routeErrCode(err map[string]any) any {
	for _, k := range []string{"status", "status_code", "code"} {
		if v, ok := err[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// routeChoiceDelta reads the first choice's delta and finish_reason.
func routeChoiceDelta(body map[string]any) (map[string]any, any) {
	choices, _ := body["choices"].([]any)
	var choice map[string]any
	if len(choices) > 0 {
		choice, _ = choices[0].(map[string]any)
	}
	if choice == nil {
		return map[string]any{}, nil
	}
	delta, _ := choice["delta"].(map[string]any)
	if delta == nil {
		delta = map[string]any{}
	}
	return delta, choice["finish_reason"]
}

// routeDeltaSays is whether a delta carries any of the content fields.
func routeDeltaSays(delta map[string]any) bool {
	for _, f := range []string{"content", "refusal", "reasoning", "reasoning_content", "audio"} {
		if v, ok := delta[f]; ok && v != nil {
			return true
		}
	}
	return false
}

// estimateRouteTokens is the conservative chars-per-token count of the
// request's messages, from the policy.
func estimateRouteTokens(pol provider.RoutePolicy, req map[string]any) int {
	return len(compactJSON(routeMessages(req))) / pol.PreTruncate.CharsPerToken
}

// truncateRouteMessages drops whole conversation turns from the front —
// keeping the system/developer prefix and always starting a kept turn at a
// user message — until the request fits the token budget. It returns nil
// when there is nothing to drop or too few messages to drop safely.
func truncateRouteMessages(pol provider.RoutePolicy, req map[string]any, budget int) map[string]any {
	msgs := routeMessages(req)
	if len(msgs) < 3 {
		return nil
	}
	prefixEnd := 0
	for prefixEnd < len(msgs) {
		role := routeRole(msgs[prefixEnd])
		if role != "system" && role != "developer" {
			break
		}
		prefixEnd++
	}
	conversation := slices.Clone(msgs[prefixEnd:])
	original := len(conversation)
	for len(conversation) > 1 && len(compactJSON(append(slices.Clone(msgs[:prefixEnd]), conversation...)))/pol.PreTruncate.CharsPerToken > budget {
		conversation = conversation[1:]
		for len(conversation) > 1 && routeRole(conversation[0]) != "user" {
			conversation = conversation[1:]
		}
	}
	if len(conversation) == original {
		return nil
	}
	out := make(map[string]any, len(req)+1)
	for k, v := range req {
		out[k] = v
	}
	out["messages"] = append(slices.Clone(msgs[:prefixEnd]), conversation...)
	return out
}

// routeMessages is the request's messages list, empty when it is not one.
func routeMessages(req map[string]any) []any {
	msgs, _ := req["messages"].([]any)
	return msgs
}

// routePreTruncate is the first truncation: a request the policy estimates
// at or over its trigger is sent pre-truncated to its budget, so a huge
// conversation isn't refused by every model it is tried on. It returns the
// body unchanged when it is under the bar or not truncatable.
func routePreTruncate(pol provider.RoutePolicy, body []byte) []byte {
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	if estimateRouteTokens(pol, req) <= pol.PreTruncate.TriggerTokens {
		return body
	}
	if reduced := truncateRouteMessages(pol, req, pol.PreTruncate.BudgetTokens); reduced != nil {
		if b, err := json.Marshal(reduced); err == nil {
			return b
		}
	}
	return body
}

// routeRole is a message's role, "" when it has none.
func routeRole(m any) string {
	mm, _ := m.(map[string]any)
	role, _ := mm["role"].(string)
	return role
}

// routeNumEq is whether a decoded JSON value equals the integer n.
func routeNumEq(v any, n int) bool {
	switch x := v.(type) {
	case float64:
		return x == float64(n)
	case int:
		return x == n
	}
	return false
}

// routeFalsy is the Router's "not" on a decoded JSON value.
func routeFalsy(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case float64:
		return x == 0
	case bool:
		return !x
	}
	return false
}

// routeTruthy is the Router's truthiness.
func routeTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// containsAnyFold is whether any needle appears in the lower-cased text.
func containsAnyFold(text string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(text, n) {
			return true
		}
	}
	return false
}

// compactJSON is a value as one line of JSON, unicode kept and HTML not
// escaped, matching the Router's json.dumps(..., ensure_ascii=False).
func compactJSON(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}
