package gateway

import (
	"slices"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/provider"
)

// routeChain is the policy's model-family fallback order. An alias starts
// at its mapped primary; a concrete model only falls through to
// lower-priority siblings, never upgrades. A family that names no models is
// derived from avail, the provider's own model list, so a model the channel
// adds later falls back without an edit. Models the policy says do not take
// tools are omitted from a request carrying tools.
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
	var family []string
	for _, fam := range pol.Family.Families {
		if routeFamilyHas(fam, primary) {
			family = routeFamilyMembers(fam, avail)
			break
		}
	}
	if at := slices.Index(family, primary); at >= 0 {
		family = family[at+1:]
	}
	out := append([]string{primary}, family...)
	out = slices.Compact(out)
	if tools {
		out = slices.DeleteFunc(out, func(m string) bool { return slices.Contains(pol.Family.ToolExcluded, m) })
	}
	return out
}

// routeFamilyHas is whether a model belongs to the family: its id contains
// the family's name, unless the family names it in Exclude. A model the
// family lists explicitly still counts when its id does not contain the name.
func routeFamilyHas(fam provider.RouteFamilyList, model string) bool {
	if slices.Contains(fam.Exclude, model) {
		return false
	}
	return slices.Contains(fam.Models, model) || strings.Contains(model, fam.Name)
}

// routeFamilyMembers is the family's members in fallback order: the listed
// models first, then every other model the provider serves whose id contains
// the family's name, newest version first. A family that lists none is
// derived entirely from the provider's list, so a model the channel adds or
// removes is picked up without an edit to the file.
func routeFamilyMembers(fam provider.RouteFamilyList, avail []string) []string {
	out := slices.Clone(fam.Models)
	var fresh []string
	for _, m := range avail {
		if strings.Contains(m, fam.Name) && !slices.Contains(fam.Models, m) && !slices.Contains(fam.Exclude, m) {
			fresh = append(fresh, m)
		}
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
