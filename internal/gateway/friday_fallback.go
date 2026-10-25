package gateway

import (
	"slices"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/provider"
)

var (
	fridayOpus   = []string{"claude-opus-5", "aws.claude-opus-4.8", "aws.claude-opus-4.7", "aws.claude-opus-4.6"}
	fridaySonnet = []string{"claude-sonnet-5", "aws.claude-sonnet-4.6", "aws.claude-sonnet-4.5"}
	fridayGPT    = []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-luna", "gpt-5.6-terra", "gpt-5.5"}
	fridayKimi   = []string{"kimi-k3", "kimi-k2.7-code-highspeed", "kimi-k2.7-code", "kimi-k2.6", "kimi-k2.6-baidu", "kimi-k2.5-baidu", "kimi-0902-preview"}
)

var fridayAliases = map[string]string{
	"gpt":               "gpt-6-astra",
	"kimi":              "kimi-k3",
	"claude-opus-4-8":   "aws.claude-opus-4.8",
	"claude-opus-4-7":   "aws.claude-opus-4.8",
	"claude-opus-4-6":   "aws.claude-opus-4.8",
	"claude-sonnet-4-6": "aws.claude-sonnet-4.6",
	"claude-sonnet-4-5": "aws.claude-sonnet-4.5",
	"claude-haiku-4-5":  "gpt-4o-mini",
}

// fridayChain is Friday Router's model-family order. An alias starts at its
// mapped primary; a concrete model only falls through to lower-priority
// siblings, never upgrades. Models known not to support tools are omitted from
// a request carrying tools.
func fridayChain(model string, tools bool) []string {
	primary := model
	if alias := fridayAliases[model]; alias != "" {
		primary = alias
	}
	if strings.HasPrefix(model, "claude-haiku") {
		primary = "gpt-4o-mini"
	}
	var family []string
	switch {
	case strings.Contains(primary, "opus"):
		family = fridayOpus
	case strings.Contains(primary, "sonnet"):
		family = fridaySonnet
	case strings.Contains(primary, "gpt"):
		family = fridayGPT
	case slices.Contains(fridayKimi, primary):
		family = fridayKimi
	}
	if at := slices.Index(family, primary); at >= 0 {
		family = family[at+1:]
	}
	out := append([]string{primary}, family...)
	out = slices.Compact(out)
	if tools {
		out = slices.DeleteFunc(out, func(m string) bool { return m == "gpt-6-astra" || m == "gpt-6.1-sol" })
	}
	return out
}

// fridayTools checks top-level tools in the caller's API, before translation.
// A malformed body leaves this to the ordinary request parser to reject.
func fridayTools(from provider.Protocol, body []byte) bool {
	if from == provider.Responses {
		body = liftAdditionalTools(body)
	}
	return gjson.GetBytes(body, "tools.#").Int() > 0
}

// fridayKnown uses the provider's current model list, not the first 24
// exposed to agent pickers. When no list is available yet, keep the
// family order, still excluding models known not to support tools.
func fridayKnown(p provider.Provider, chain []string) []string {
	known := map[string]bool{}
	for _, m := range p.Available() {
		known[m.ID] = true
	}
	if len(known) == 0 {
		return chain
	}
	return slices.DeleteFunc(slices.Clone(chain), func(m string) bool { return !known[m] })
}

// planFriday adds Friday's model-family fallbacks ahead of the user's own
// fallback models. The ordinary candidate loop still decides which failures
// permit a switch and never switches after the client sees answer bytes.
func (s *Server) planFriday(p provider.Provider, model string, from provider.Protocol, body []byte) ([]candidate, planned) {
	chain := fridayKnown(p, fridayChain(model, fridayTools(from, body)))
	// a model cooling after an upstream 429 is skipped, as the Router's
	// relay loop skips it, and the next sibling is asked first.
	chain = slices.DeleteFunc(chain, func(m string) bool { return fridayCools.cooled(m, time.Now()) })
	if len(chain) == 0 {
		return nil, planned{}
	}
	original := slices.Clone(p.Fallback)
	p.Fallback = make([]string, 0, len(chain)+len(original))
	for _, m := range chain[1:] {
		p.Fallback = append(p.Fallback, p.ID+"/"+m)
	}
	p.Fallback = append(p.Fallback, original...)
	return s.plan(p, chain[0], from)
}
