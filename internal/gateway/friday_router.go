package gateway

// Friday Router alignment: the pure scheduling decisions the separate
// Friday Router makes about one adapter reply. magpie's gateway is the
// Router's equivalent (friday_fallback.go plans the model family, and the
// candidate loop tries it), so the Router's remaining logic lives here:
// the SSE-frame classifier that decides retry vs. fatal before any content
// is sent, the per-model cooldown with its GPT and no-429 exceptions, and
// the input truncation that retries a too-long request under a smaller
// token budget. Wired into the candidate loop and the stream sniffer in
// gateway.go; these functions hold the decisions they need.

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// fridayModelCooldown is the Router's MODEL_COOLDOWN_SECS default: how
// long a cooled model is skipped.
const fridayModelCooldown = 60 * time.Second

// fridayNo429Cooldown are the models the Router does not cool on a 429
// (aws.claude-opus-4.8 and the GPT family); aws.claude-opus-4.8 still cools
// on a created=0 reply, and GPT models never cool.
var fridayNo429Cooldown = map[string]bool{"aws.claude-opus-4.8": true}

func init() {
	for _, m := range fridayGPT {
		fridayNo429Cooldown[m] = true
	}
}

// fridayCools is the process-local Friday model cooldown: filled when a
// model's 429 or a created=0 reply is seen, and read by planFriday, which
// skips cooling models.
var fridayCools fridayCooldowns

// fridayCooldowns holds Friday's per-model cooldowns, mirroring the
// Router's Scheduler.cooldowns.
type fridayCooldowns struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// cool sets a model's cooldown, unless it is a GPT model or — outside a
// created=0 reply — one of the models a 429 does not cool.
func (c *fridayCooldowns) cool(model string, created0 bool, now time.Time) {
	if slices.Contains(fridayGPT, model) || (!created0 && fridayNo429Cooldown[model]) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.until == nil {
		c.until = map[string]time.Time{}
	}
	if !c.until[model].After(now) {
		c.until[model] = now.Add(fridayModelCooldown)
	}
}

// cooled reports whether a model is inside its cooldown.
func (c *fridayCooldowns) cooled(model string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.until[model].After(now)
}

// Friday Router's request throttle: at most fridayMaxConcurrent upstream
// requests in flight, and at least fridaySendInterval between their sends,
// matching the Router's MAX_CONCURRENT_FRIDAY and MIN_SEND_INTERVAL_SECS
// defaults (2 and 0.5s). The semaphore is shared process-wide across all
// Friday requests, not per key or account.
const fridayMaxConcurrent = 2

var (
	fridaySendInterval = 500 * time.Millisecond
	fridaySem          = make(chan struct{}, fridayMaxConcurrent)
	fridaySendMu       sync.Mutex
	fridayLastSend     time.Time
)

// fridayThrottle waits for a Friday upstream slot and for the Router's
// minimum send interval. It returns a function that frees the slot once the
// attempt is over, or an error when ctx is cancelled first.
func fridayThrottle(ctx context.Context) (func(), error) {
	select {
	case fridaySem <- struct{}{}:
	case <-ctx.Done():
		return func() {}, ctx.Err()
	}
	release := func() { <-fridaySem }

	fridaySendMu.Lock()
	if wait := fridaySendInterval - time.Since(fridayLastSend); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			fridaySendMu.Unlock()
			release()
			return func() {}, ctx.Err()
		}
	}
	fridayLastSend = time.Now()
	fridaySendMu.Unlock()
	return release, nil
}

// parseFridayFrame splits one SSE frame into its event name and data body,
// mirroring the Router's parse_frame.
func parseFridayFrame(frame []byte) (string, map[string]any) {
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

// classifyFridayFrame returns (outcome, cooldown, appError), mirroring the
// Router's classify: "retry" moves to the next model while nothing of the
// reply has been sent, "fatal" rejects the request outright, and
// "content"/"done"/"skip" pass it through.
func classifyFridayFrame(frame []byte) (outcome string, cooldown bool, appError bool) {
	event, body := parseFridayFrame(frame)
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
		code := fridayErrCode(err)
		isLimit := fridayNumEq(code, 429) || fmt.Sprint(code) == "429" || strings.Contains(text, "429")
		errType, _ := err["type"].(string)
		temporary := isLimit || errType == "overloaded_error" || errType == "server_error" ||
			containsAnyFold(text, "empty", "unavailable", "timeout", "created=0", "500", "502", "503")
		if temporary || fridayNumEq(code, 500) || fridayNumEq(code, 502) || fridayNumEq(code, 503) || fridayNumEq(code, 504) {
			return "retry", isLimit, true
		}
		return "fatal", isLimit, true
	}
	if created, _ := body["created"].(float64); created == 0 && fridayFalsy(body["id"]) {
		return "retry", false, true
	}
	delta, finishReason := fridayChoiceDelta(body)
	text := fmt.Sprint(delta["content"])
	if strings.HasPrefix(text, "[Error]") {
		lower := strings.ToLower(text)
		temporary := containsAnyFold(lower, "429", "rate", "unavailable", "暂时不可用", "下线", "timeout", "500", "502", "503")
		if temporary {
			return "retry", strings.Contains(text, "429"), true
		}
		return "fatal", strings.Contains(text, "429"), true
	}
	if fridayDeltaSays(delta) || fridayTruthy(delta["tool_calls"]) {
		return "content", false, false
	}
	if finishReason != nil {
		return "done", false, false
	}
	return "skip", false, false
}

// fridayErrCode is the first of status, status_code or code the error
// names, as the Router reads them.
func fridayErrCode(err map[string]any) any {
	for _, k := range []string{"status", "status_code", "code"} {
		if v, ok := err[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// fridayChoiceDelta reads the first choice's delta and finish_reason.
func fridayChoiceDelta(body map[string]any) (map[string]any, any) {
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

// fridayDeltaSays is whether a delta carries any of the content fields,
// as the Router's `delta.get(field) is not None` checks.
func fridayDeltaSays(delta map[string]any) bool {
	for _, f := range []string{"content", "refusal", "reasoning", "reasoning_content", "audio"} {
		if v, ok := delta[f]; ok && v != nil {
			return true
		}
	}
	return false
}

// estimateFridayTokens is the Router's conservative 4 chars/token count of
// the request's messages.
func estimateFridayTokens(req map[string]any) int {
	return len(compactJSON(fridayMessages(req))) / 4
}

// truncateFridayMessages drops whole conversation turns from the front —
// keeping the system/developer prefix and always starting a kept turn at a
// user message — until the request fits the token budget. It returns nil
// when there is nothing to drop or too few messages to drop safely.
func truncateFridayMessages(req map[string]any, budget int) map[string]any {
	msgs := fridayMessages(req)
	if len(msgs) < 3 {
		return nil
	}
	prefixEnd := 0
	for prefixEnd < len(msgs) {
		role := fridayRole(msgs[prefixEnd])
		if role != "system" && role != "developer" {
			break
		}
		prefixEnd++
	}
	conversation := slices.Clone(msgs[prefixEnd:])
	original := len(conversation)
	for len(conversation) > 1 && len(compactJSON(append(slices.Clone(msgs[:prefixEnd]), conversation...)))/4 > budget {
		conversation = conversation[1:]
		for len(conversation) > 1 && fridayRole(conversation[0]) != "user" {
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

// fridayMessages is the request's messages list, empty when it is not one.
func fridayMessages(req map[string]any) []any {
	msgs, _ := req["messages"].([]any)
	return msgs
}

// fridayPreTruncate is the Router's first truncation: a request the Router
// estimates over 180000 tokens is sent pre-truncated to a 100000 budget, so
// a huge conversation isn't refused by every model it is tried on. It
// returns the body unchanged when it is under the bar or not truncatable.
func fridayPreTruncate(body []byte) []byte {
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	if estimateFridayTokens(req) <= 180000 {
		return body
	}
	if reduced := truncateFridayMessages(req, 100000); reduced != nil {
		if b, err := json.Marshal(reduced); err == nil {
			return b
		}
	}
	return body
}

// fridayRole is a message's role, "" when it has none.
func fridayRole(m any) string {
	mm, _ := m.(map[string]any)
	role, _ := mm["role"].(string)
	return role
}

// fridayNumEq is whether a decoded JSON value equals the integer n.
func fridayNumEq(v any, n int) bool {
	switch x := v.(type) {
	case float64:
		return x == float64(n)
	case int:
		return x == n
	}
	return false
}

// fridayFalsy is the Router's "not" on a decoded JSON value: a missing,
// empty, zero or false field is falsy.
func fridayFalsy(v any) bool {
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

// fridayTruthy is the Router's truthiness: a non-empty list or map, or a
// non-zero scalar.
func fridayTruthy(v any) bool {
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
