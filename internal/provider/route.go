package provider

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/tidwall/jsonc"
	"github.com/cha0sal3r/magpie-friday/internal/appdir"
)

// RoutePolicy is a per-provider routing strategy: the model-family
// fallback, per-model cooldown, upstream throttle, reply classification and
// pre-truncation, enabled and tuned per provider. It is kept as one JSON
// file per provider (routes/<provider-id>.json), editable by hand or in the
// web UI.
//
// A zero field means "the default" (see DefaultRoute); the file may name
// only the fields it changes.
type RoutePolicy struct {
	Enabled bool `json:"enabled"`
	// Family plans the model-family fallback order.
	Family RouteFamily `json:"family,omitempty"`
	// Cooldown cools a model that a 429 or a created=0 reply failed.
	Cooldown RouteCooldown `json:"cooldown,omitempty"`
	// Throttle bounds upstream concurrency and the gap between sends.
	Throttle RouteThrottle `json:"throttle,omitempty"`
	// Classify decides retry vs fatal from an SSE frame before any content.
	Classify RouteClassify `json:"classify,omitempty"`
	// PreTruncate trims an oversized request before it is sent.
	PreTruncate RoutePreTruncate `json:"preTruncate,omitempty"`
}

// RouteFamily is the model-family fallback plan: an alias starts at its
// mapped model, and a concrete model falls through to lower-priority
// siblings of its own family only — never up and never on to another
// family, each family falling back within itself alone — so a series with a
// single member has no fallback beyond itself.
type RouteFamily struct {
	// Aliases maps a model alias to the concrete model it starts at
	// ("gpt" → "gpt-6-astra").
	Aliases map[string]string `json:"aliases,omitempty"`
	// PrefixAliases maps a model-id prefix to the concrete model it starts
	// at (e.g. "claude-haiku" → "gpt-4o-mini"), checked after Aliases.
	PrefixAliases map[string]string `json:"prefixAliases,omitempty"`
	// Families lists the model families in matching order. A family lists
	// its models, or names none and is derived from the provider's own
	// model list, so the file stays valid as the channel's models change.
	Families []RouteFamilyList `json:"families,omitempty"`
	// ToolExcluded are models omitted from a request that carries tools.
	ToolExcluded []string `json:"toolExcluded,omitempty"`
}

// RouteFamilyList is one model family. Models is the family's known models
// in priority order; a family that names none is derived entirely from the
// provider's own list. Models the provider serves that the list does not
// name are appended past the listed ones, newest version first, so a model
// the channel adds or removes is picked up without an edit.
type RouteFamilyList struct {
	Name   string   `json:"name"`
	Models []string `json:"models,omitempty"`
	// Exclude are models whose id contains Name but are not in the family
	// (e.g. "kimi-unlisted").
	Exclude []string `json:"exclude,omitempty"`
}

// RouteCooldown cools a model that failed, so the next request skips it
// until the cooldown ends.
type RouteCooldown struct {
	// Seconds is how long a cooled model is skipped.
	Seconds int `json:"seconds"`
	// No429 are models a 429 does not cool; a created=0 reply still cools
	// them (e.g. aws.claude-opus-4.8).
	No429 []string `json:"no429,omitempty"`
	// NeverCool are models never cooled, by either reply (e.g. a GPT
	// family).
	NeverCool []string `json:"neverCool,omitempty"`
}

// RouteThrottle bounds upstream requests.
type RouteThrottle struct {
	// MaxConcurrent is how many upstream requests may be in flight at once,
	// across the provider.
	MaxConcurrent int `json:"maxConcurrent"`
	// SendIntervalMs is the minimum gap between sending upstream requests.
	SendIntervalMs int `json:"sendIntervalMs"`
}

// RouteClassify decides, from an SSE frame before any content reaches the
// client, whether to retry on the next model or fail the request.
type RouteClassify struct {
	// RetryErrorTypes are error "type" values that retry
	// ("overloaded_error", "server_error").
	RetryErrorTypes []string `json:"retryErrorTypes,omitempty"`
	// RetryKeywords, in an error message, retry.
	RetryKeywords []string `json:"retryKeywords,omitempty"`
	// RetryStatusCodes are error status codes that retry (500, 502, 503,
	// 504). A 429 always retries and cools, whatever these say.
	RetryStatusCodes []int `json:"retryStatusCodes,omitempty"`
	// ChunkErrorRetryKeywords, in a model's own "[Error] …" text, retry.
	ChunkErrorRetryKeywords []string `json:"chunkErrorRetryKeywords,omitempty"`
}

// RoutePreTruncate trims an oversized request before it is sent, so a huge
// conversation is not refused by every model it is tried on.
type RoutePreTruncate struct {
	// TriggerTokens is the estimate at or above which the request is
	// trimmed.
	TriggerTokens int `json:"triggerTokens"`
	// BudgetTokens is the estimate the request is trimmed to fit.
	BudgetTokens int `json:"budgetTokens"`
	// CharsPerToken is the conservative character-per-token estimate.
	CharsPerToken int `json:"charsPerToken"`
}

// DefaultRoute is the routing strategy's generic factory default: the
// mechanism settings (cooldown length, throttle, reply classification,
// pre-truncation) any provider may take. It names no model family, so it
// stays valid however a provider's model list changes; a provider's
// model-family plan lives in its own route file.
func DefaultRoute() RoutePolicy {
	return RoutePolicy{
		Enabled: true,
		Cooldown: RouteCooldown{
			Seconds: 60,
		},
		Throttle: RouteThrottle{MaxConcurrent: 2, SendIntervalMs: 500},
		Classify: RouteClassify{
			RetryErrorTypes:         []string{"overloaded_error", "server_error"},
			RetryKeywords:           []string{"empty", "unavailable", "timeout", "created=0", "500", "502", "503"},
			RetryStatusCodes:        []int{500, 502, 503, 504},
			ChunkErrorRetryKeywords: []string{"429", "rate", "unavailable", "暂时不可用", "下线", "timeout", "500", "502", "503"},
		},
		PreTruncate: RoutePreTruncate{TriggerTokens: 180000, BudgetTokens: 100000, CharsPerToken: 4},
	}
}

// RouteDir is the folder routing-strategy files live in.
func RouteDir() string { return filepath.Join(appdir.Config(), "routes") }

// RoutePath is the routing-strategy file for one provider.
func RoutePath(id string) string { return filepath.Join(RouteDir(), id+".json") }

// LoadRoute reads a provider's routing strategy, normalized with defaults.
// ok is false when no file names it; otherwise the policy is returned with
// Enabled as the file keeps it (present but switched off still has ok).
func LoadRoute(id string) (RoutePolicy, bool) {
	p := RoutePath(id)
	b, err := os.ReadFile(p)
	if err != nil {
		return RoutePolicy{}, false
	}
	var r RoutePolicy
	if json.Unmarshal(jsonc.ToJSON(b), &r) != nil {
		return RoutePolicy{}, false
	}
	return normalizeRoute(r), true
}

// RouteRaw is a provider's routing-strategy file as its bytes, for the
// editor; nil when the file does not exist.
func RouteRaw(id string) []byte {
	b, err := os.ReadFile(RoutePath(id))
	if err != nil {
		return nil
	}
	return b
}

// SaveRoute writes a provider's routing strategy, normalized with defaults.
// An empty policy (Enabled false, nothing set) removes the file, so a
// provider switched off keeps no routing file at all.
func SaveRoute(id string, r RoutePolicy) error {
	if !r.Enabled && routeZero(r) {
		return RemoveRoute(id)
	}
	return writePrivate(RoutePath(id), RouteJSON(r))
}

// RemoveRoute deletes a provider's routing-strategy file.
func RemoveRoute(id string) error {
	err := os.Remove(RoutePath(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// RouteOf is the routing strategy a provider runs: its own file when it has
// one, else none. The second result is whether a strategy applies at all.
func RouteOf(p Provider) (RoutePolicy, bool) {
	return LoadRoute(p.ID)
}

// routeZero is whether a policy has no setting of its own — nothing that
// would be lost by removing its file.
func routeZero(r RoutePolicy) bool {
	return len(r.Family.Aliases) == 0 && len(r.Family.PrefixAliases) == 0 &&
		len(r.Family.Families) == 0 && len(r.Family.ToolExcluded) == 0 &&
		r.Cooldown.Seconds == 0 && len(r.Cooldown.No429) == 0 && len(r.Cooldown.NeverCool) == 0 &&
		r.Throttle.MaxConcurrent == 0 && r.Throttle.SendIntervalMs == 0 &&
		len(r.Classify.RetryErrorTypes) == 0 && len(r.Classify.RetryKeywords) == 0 &&
		len(r.Classify.RetryStatusCodes) == 0 && len(r.Classify.ChunkErrorRetryKeywords) == 0 &&
		r.PreTruncate.TriggerTokens == 0 && r.PreTruncate.BudgetTokens == 0 && r.PreTruncate.CharsPerToken == 0
}

// normalizeRoute fills a policy's zero fields with the defaults, so a file
// that names only what it changes behaves as the rest.
func normalizeRoute(r RoutePolicy) RoutePolicy {
	d := DefaultRoute()
	if r.Family.Aliases == nil {
		r.Family.Aliases = d.Family.Aliases
	}
	if r.Family.PrefixAliases == nil {
		r.Family.PrefixAliases = d.Family.PrefixAliases
	}
	if r.Family.Families == nil {
		r.Family.Families = d.Family.Families
	}
	if r.Family.ToolExcluded == nil {
		r.Family.ToolExcluded = d.Family.ToolExcluded
	}
	if r.Cooldown.Seconds == 0 {
		r.Cooldown.Seconds = d.Cooldown.Seconds
	}
	if r.Cooldown.No429 == nil {
		r.Cooldown.No429 = d.Cooldown.No429
	}
	if r.Cooldown.NeverCool == nil {
		r.Cooldown.NeverCool = d.Cooldown.NeverCool
	}
	if r.Throttle.MaxConcurrent == 0 {
		r.Throttle.MaxConcurrent = d.Throttle.MaxConcurrent
	}
	if r.Throttle.SendIntervalMs == 0 {
		r.Throttle.SendIntervalMs = d.Throttle.SendIntervalMs
	}
	if r.Classify.RetryErrorTypes == nil {
		r.Classify.RetryErrorTypes = d.Classify.RetryErrorTypes
	}
	if r.Classify.RetryKeywords == nil {
		r.Classify.RetryKeywords = d.Classify.RetryKeywords
	}
	if r.Classify.RetryStatusCodes == nil {
		r.Classify.RetryStatusCodes = d.Classify.RetryStatusCodes
	}
	if r.Classify.ChunkErrorRetryKeywords == nil {
		r.Classify.ChunkErrorRetryKeywords = d.Classify.ChunkErrorRetryKeywords
	}
	if r.PreTruncate.TriggerTokens == 0 {
		r.PreTruncate.TriggerTokens = d.PreTruncate.TriggerTokens
	}
	if r.PreTruncate.BudgetTokens == 0 {
		r.PreTruncate.BudgetTokens = d.PreTruncate.BudgetTokens
	}
	if r.PreTruncate.CharsPerToken == 0 {
		r.PreTruncate.CharsPerToken = d.PreTruncate.CharsPerToken
	}
	return r
}
