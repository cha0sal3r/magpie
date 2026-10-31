package gui

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/cha0sal3r/magpie-friday/internal/catalog"
	"github.com/cha0sal3r/magpie-friday/internal/usage"
)

// firstPartyVendors are the models.dev providers whose list prices the Usage
// page's planner forecasts with: the same five the site's /prices reduces to
// (site/worker.js PRICE_PROVIDERS). A reseller's price for the same model is
// another question, its own margin in it, so only the vendor's own list price
// is here.
var firstPartyVendors = []string{"openai", "anthropic", "deepseek", "moonshotai", "zhipuai"}

// plannerModel is one model's base list price and what the reader's usage of
// the period would have cost at it.
type plannerModel struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Vendor     string  `json:"vendor"`     // models.dev provider id
	VendorName string  `json:"vendorName"` // its display name
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	Cost       float64 `json:"cost"`
}

// plannerJSON is what the Usage page's "another model" planner draws: the
// period's own totals, and each first-party model priced at them.
type plannerJSON struct {
	Period string         `json:"period"`
	Calls  int            `json:"calls"`
	Input  int            `json:"input"`
	Output int            `json:"output"`
	Cache  int            `json:"cacheRead"`
	Cost   float64        `json:"cost"`
	Models []plannerModel `json:"models"`
}

// planner is the Usage page's answer to "what would my usage cost on another
// model": the period's tokens priced at each first-party model's base list
// price, cheapest first. The price is the base, not a long-prompt tier, since
// a period's tokens summed over many requests reach no single request's tier
// (the site's /prices moves that boundary with a prompt size of its own).
func planner(p usage.Period) plannerJSON {
	s := usage.Summarize(p)
	out := plannerJSON{
		Period: string(p), Calls: s.Calls, Input: s.Input, Output: s.Output,
		Cache: s.CacheRead, Cost: s.Cost, Models: []plannerModel{},
	}
	for _, vid := range firstPartyVendors {
		vendor := catalog.ProviderName(vid)
		for _, m := range catalog.Provider(vid) {
			if m.Price == nil || m.Price.Input <= 0 {
				continue
			}
			// The reader's tokens, priced at this model's base list price.
			// The tier is the base, not a long-prompt one: a period's tokens
			// summed over many requests reach no single request's tier (the
			// site's /prices moves that boundary with a prompt size of its
			// own). A token of a kind this model doesn't bill is priced as
			// input, as the site's page does: a model with no cache-read
			// price doesn't cache, so a read there is a miss at input.
			base := *m.Price
			catalog.OneHourFor(m.ID, &base)
			read := base.CacheRead
			if read <= 0 {
				read = base.Input
			}
			write := base.CacheWrite
			if write <= 0 {
				write = base.Input
			}
			one := base.OneHour()
			if one <= 0 {
				one = write
			}
			cost := (float64(s.Input)*base.Input + float64(s.Output)*base.Output +
				float64(s.CacheRead)*read + float64(s.CacheWrite-s.CacheWrite1h)*write +
				float64(s.CacheWrite1h)*one) / 1e6
			out.Models = append(out.Models, plannerModel{
				ID: m.ID, Name: m.Name, Vendor: vid, VendorName: vendor,
				Input: base.Input, Output: base.Output, CacheRead: base.CacheRead,
				Cost: cost,
			})
		}
	}
	slices.SortFunc(out.Models, func(a, b plannerModel) int {
		if c := cmp.Compare(a.Cost, b.Cost); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

// plannerRoutes adds the Usage page's planner read to the routes the page
// already answers.
func plannerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/usage/planner", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, planner(periodOf(r.URL.Query().Get("period"))))
	})
}
