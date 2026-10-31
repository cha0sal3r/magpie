package provider

import "encoding/json"

// routeDoc is the comment header written above a routing-strategy file so the
// JSON is self-documenting. The file is JSON with comments: lines starting
// with // (and /* ... */ blocks) are ignored when it is read.
const routeDoc = `// Routing strategy for this provider. JSON with comments: lines starting
// with // (and /* ... */ blocks) are ignored when the file is read. Every
// field except "enabled" is optional; an omitted field takes its default.
//
// enabled                 true to run the strategy at all.
//
// family.aliases          model alias -> the concrete model it starts at
//                         ("gpt" -> "gpt-6-astra").
// family.prefixAliases    model-id prefix -> concrete model
//                         ("claude-haiku" -> "gpt-4o-mini"), checked after
//                         aliases.
// family.families         the model families, in matching order. A family is
//                         {"name": ..., "models": [...], "exclude": [...]}.
//                         models lists its known models in fallback order; a
//                         family that lists none is derived from the
//                         provider's own list (id contains the name, newest
//                         version first), and exclude drops models whose id
//                         contains the name but are not members. A model
//                         falls through its own family only, never climbing
//                         to a newer one and never on to another family.
// family.toolExcluded     models not sent a request that carries tools.
//
// cooldown.seconds        how long a failed model sits out (default 60).
// cooldown.no429          models a 429 does not cool (created=0 still does).
// cooldown.neverCool      models never cooled.
//
// throttle.maxConcurrent  at most this many upstream requests in flight.
// throttle.sendIntervalMs minimum milliseconds between sends.
//
// classify.retryErrorTypes          error "type" values that retry.
// classify.retryKeywords            keywords in an error message that retry.
// classify.retryStatusCodes         error status codes that retry.
// classify.chunkErrorRetryKeywords  keywords in the model's own [Error] text.
//
// preTruncate.triggerTokens   estimate at/above which a request is trimmed.
// preTruncate.budgetTokens    the budget the request is trimmed to.
// preTruncate.charsPerToken   conservative chars-per-token estimate.`

// RouteJSON is a policy as the routing file keeps it: the comment header
// above the normalized JSON. The editor shows it, and SaveRoute writes it.
func RouteJSON(r RoutePolicy) []byte {
	b, err := json.MarshalIndent(normalizeRoute(r), "", "  ")
	if err != nil {
		return nil
	}
	out := append([]byte(routeDoc), '\n')
	out = append(out, b...)
	out = append(out, '\n')
	return out
}
