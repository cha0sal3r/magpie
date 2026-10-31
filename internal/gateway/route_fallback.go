package gateway

import (
	"slices"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/cha0sal3r/magpie-friday/internal/provider"
)

// routeChain is the policy's model-family fallback order. An alias starts
// at its mapped primary; a concrete model falls through to the members after
// it in its own family's order, which may be newer or older models, but
// never on to another family: each family falls back within itself alone, so
// a series with a single member has no fallback beyond itself. A family that
// names no models is derived from avail, the provider's own model list, so a
// model the channel adds later falls back without an edit. Models the policy
// says do not take tools are omitted from a request carrying tools.
func routeChain(pol provider.RoutePolicy, avail []string, model string, tools bool) []string {
	primary := model
	if alias := pol.Family.Aliases[model]; alias != "" {
		primary = alias
	}
	bestPrefix, bestTarget := "", ""
	for prefix, target := range pol.Family.PrefixAliases {
		if strings.HasPrefix(model, prefix) && len(prefix) > len(bestPrefix) {
			bestPrefix, bestTarget = prefix, target
		}
	}
	if bestTarget != "" {
		primary = bestTarget
	}
	var out []string
	if fam := owningFamily(pol, primary); fam >= 0 {
		members := routeFamilyMembers(pol, fam, avail)
		if at := slices.Index(members, primary); at >= 0 {
			out = append(out, members[at:]...)
		} else {
			// the family holds the model by its name alone: asked as it is,
			// then the family's own order.
			out = append(out, primary)
			out = append(out, members...)
		}
	}
	if len(out) == 0 {
		out = []string{primary}
	}
	out = slices.Compact(out)
	if tools {
		out = slices.DeleteFunc(out, func(m string) bool { return slices.Contains(pol.Family.ToolExcluded, m) })
	}
	return out
}

// owningFamily is the index of the family a model belongs to: the one that
// matches it most specifically. An exact Models listing beats a name that is
// a substring of the model's id, and among substring matches the longest name
// wins (ties keep the family's list order); Exclude overrides both. -1 when
// none matches. Choosing the most specific family is what keeps a model's
// fallback inside its own family: a broader name ("claude") no longer claims
// a model a narrower one ("claude-opus") also matches.
func owningFamily(pol provider.RoutePolicy, model string) int {
	sub, subLen := -1, 0
	for i, fam := range pol.Family.Families {
		if slices.Contains(fam.Exclude, model) {
			continue
		}
		if slices.Contains(fam.Models, model) {
			return i
		}
		if strings.Contains(model, fam.Name) && len(fam.Name) > subLen {
			sub, subLen = i, len(fam.Name)
		}
	}
	return sub
}

// routeFamilyMembers is the family's members in fallback order: the listed
// models first, then every other model the provider serves that belongs to
// this family and no more-specific one, newest version first. A family that
// lists none is derived entirely from the provider's list, so a model the
// channel adds or removes is picked up without an edit to the file, and one
// family never takes another family's model.
func routeFamilyMembers(pol provider.RoutePolicy, fam int, avail []string) []string {
	out := slices.Clone(pol.Family.Families[fam].Models)
	var fresh []string
	for _, m := range avail {
		if slices.Contains(out, m) || owningFamily(pol, m) != fam {
			continue
		}
		fresh = append(fresh, m)
	}
	slices.SortStableFunc(fresh, provider.VersionDesc)
	return append(out, fresh...)
}

// routeTools checks top-level tools in the caller's API, before translation.
// A malformed body leaves this to the ordinary request parser to reject.
func routeTools(from provider.Protocol, body []byte) bool {
	if from == provider.Responses {
		body = liftAdditionalTools(body)
	}
	return gjson.GetBytes(body, "tools.#").Int() > 0
}

// routeAvailableIDs is the provider's model list to derive and filter the
// fallback against: the channel's full current list, or — before one has
// been fetched — the models the provider exposes, so the families still
// fall back.
func routeAvailableIDs(p provider.Provider) []string {
	var ids []string
	for _, m := range p.Available() {
		ids = append(ids, m.ID)
	}
	if len(ids) == 0 {
		ids = p.Models
	}
	return ids
}

// routeKnown keeps only the chain's models the provider serves, in the
// provider's current list — not the first 24 exposed to agent pickers. With
// no list at all, keep the family order, still excluding models known not
// to support tools.
func routeKnown(avail []string, chain []string) []string {
	known := make(map[string]bool, len(avail))
	for _, id := range avail {
		known[id] = true
	}
	if len(known) == 0 {
		return chain
	}
	return slices.DeleteFunc(slices.Clone(chain), func(m string) bool { return !known[m] })
}

// planRoute adds the policy's model-family fallbacks ahead of the user's
// own fallback models. The ordinary candidate loop still decides which
// failures permit a switch and never switches after the client sees answer
// bytes.
func (s *Server) planRoute(p provider.Provider, rt *routeRuntime, pol provider.RoutePolicy, model string, from provider.Protocol, body []byte) ([]candidate, planned) {
	avail := routeAvailableIDs(p)
	chain := routeKnown(avail, routeChain(pol, avail, model, routeTools(from, body)))
	// a model cooling after an upstream 429 is skipped, as the Router's
	// relay loop skips it, and the next sibling is asked first.
	chain = slices.DeleteFunc(chain, func(m string) bool { return rt.cooled(m, time.Now()) })
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
