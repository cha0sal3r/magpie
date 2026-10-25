package provider

import (
	"slices"
	"strings"
)

// familyStem is a model id with its version suffix stripped to the family
// name: "gpt-4o" → "gpt", "claude-opus-5" → "claude-opus",
// "kimi-k2.7-code" → "kimi", "deepseek-v4-pro" → "deepseek". A model id with
// no digit is its own family.
func familyStem(id string) string {
	idx := strings.IndexFunc(id, func(r rune) bool { return r >= '0' && r <= '9' })
	if idx < 0 {
		return id
	}
	stem := strings.TrimRight(id[:idx], "-._/:")
	// a single-letter version marker ("deepseek-v4", "kimi-k3") is not part
	// of the family name.
	for _, mk := range "vkrx" {
		if suffix := "-" + string(mk); strings.HasSuffix(stem, suffix) {
			stem = strings.TrimRight(strings.TrimSuffix(stem, suffix), "-._/:")
			break
		}
	}
	if stem == "" {
		return id
	}
	return stem
}

// GenerateFallbackFamilies turns a provider's model list into a basic family
// fallback plan: one listless family per family stem, newest family first. A
// listless family derives its members from the provider's current list (id
// contains the name, newest version first), so the plan stays valid as the
// channel adds and removes models. It is a starting point the user fine-tunes
// in the editor.
func GenerateFallbackFamilies(models []string) []RouteFamilyList {
	type fam struct{ max [3]int }
	byStem := map[string]*fam{}
	var order []string
	for _, m := range models {
		stem := familyStem(m)
		if len(stem) < 2 {
			continue // a one-letter name over-matches every model containing it
		}
		f, ok := byStem[stem]
		if !ok {
			f = &fam{}
			byStem[stem] = f
			order = append(order, stem)
		}
		if v := modelVersion(m); versionCompare(v, f.max) > 0 {
			f.max = v
		}
	}
	slices.SortStableFunc(order, func(a, b string) int {
		return -versionCompare(byStem[a].max, byStem[b].max)
	})
	out := make([]RouteFamilyList, 0, len(order))
	for _, stem := range order {
		out = append(out, RouteFamilyList{Name: stem})
	}
	return out
}
