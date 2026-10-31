# The prices page: the workload arithmetic

Status: implemented in the working tree. Every chart is inline SVG built from
`/api/prices` at read time; no image file is drawn into the page. The head
still names the site's shared `/img/og.png` as its social preview, which only
a crawler fetches — the page itself never requests it.

`/prices` answers one question: given a budget and a token mix, how much work
does each model's list price buy? This note records the arithmetic, why the
shares are defined the way they are, what the page deliberately leaves out,
and how a standalone mockup (`outer/index.html`, not tracked) compares, so a
later change starts from the numbers rather than from the mockup's charts.

## What the page answers

Cost accounting runs forward: tokens in, dollars out. The page runs it
backward: dollars in, tokens out. Because billing is linear in tokens within
a tier, the inverse is one division — a blended price per million tokens,
then the budget over it:

```
h + m + o = 1                             shares of a token
unit      = h·read + m·write + o·output   $ per 1M tokens
total     = budget / unit                 millions of tokens
```

Every chart is that single number, sorted, decomposed or mapped:
[`blend`](../site/public/prices.html:343) computes it,
[`drawRank`](../site/public/prices.html:416) sorts it,
[`drawSplit`](../site/public/prices.html:437) shows its three parts,
[`drawMap`](../site/public/prices.html:462) places it against the two prices,
and [`drawWriteCost`](../site/public/prices.html:526) compares the write
settings; the table prints it per model.

## Why the shares are defined as they are

Two definitions look reasonable and disagree:

| Definition | hit | miss | out |
| --- | --- | --- | --- |
| hit as a share of all tokens | `hp` | `1-hp-op` | `op` |
| hit as a share of the prompt | `hp·(1-op)` | `(1-hp)·(1-op)` | `op` |

The page uses the second, and must: vendors report a hit rate over prompt
tokens, and so does magpie-friday's own ledger
([`Totals.HitRate`](../internal/usage/usage.go:647), "the share of the prompt
read from the cache"). The first definition cannot be seeded from real usage
without converting it, and it lets the "per 10M" unit change size: with the
first, `hit=100%, out=1%` prices a block of 10.1M tokens while the page still
calls it 10M, so a $100 headline of 500M disagrees with the 505M its own
parts add up to.

The two differ in capacity by the miss share they imply. For gpt-6.1-sol
($2 / $0.10 / $10), $100, 94.9% and 1%:

```
prompt share:    339.06M tokens
all-token share: 361.14M tokens   (6.51% more)
```

They agree only at `hit=0`, and the gap grows with the output share (15.7%
at 90% / 10%). The mockup uses the first definition.

## Tiers and cache writes

A request is billed whole at the tier its prompt reaches, cached and
cache-written tokens included — [`Price.At`](../internal/catalog/catalog.go:260)
in the app, [`tierAt`](../site/public/prices.html:325) on the page. The
"Prompt per request" menu moves that boundary (8K, 40K, 128K, 300K), so a
model with a 272K or 100K tier shows a different price on the same mix.

The price of a new prompt token depends on the write setting:

- not cached: the input price;
- 5 minutes: the cache-write price, or input where the vendor lists none;
- 1 hour: the 1-hour price, else the 5-minute one, else input.

A model with no cache-read price does not cache, so its prompt is all input.
A zero write price means the vendor charges no write fee (DeepSeek, GLM), so
writing costs the same as not writing. Where a vendor's write price equals
its input price — Kimi in the mockup's table — the same holds for that TTL.
Anthropic is the only vendor with a published 1-hour write (2× input), which
[`priceList`](../site/worker.js:416) derives the way
[`OneHourFor`](../internal/catalog/catalog.go:272) does in the app.

## Service tiers

The "Service tier" control has two stops: **Standard** and **Batch**. Batch
is a flat 50% discount on every token kind, applied only where the vendor
runs a batch API — OpenAI, Anthropic and GLM. That is a vendor-level fact, so
[`priceList`](../site/worker.js:416) ships one multiplier per vendor (its
`BATCH` map, a `batch` of 0.5 on each of their models and 0 elsewhere) rather
than a per-model table: there is no per-model variation to record, and a
per-model table is exactly what would drift. [`blend`](../site/public/prices.html:343)
applies the multiplier to every part of the price, so the three shares still
add to one unit. A model with no batch price — DeepSeek and Moonshot — keeps
its standard price under Batch and is tagged "no batch price".

Priority is left off on purpose. It is a rate-limit tier of an account (how
many requests it may make, not what each token costs), not a price, and none
of the five vendors publishes a per-token priority price. The mockup's "fast /
高峰" 2× tier is the same fiction. DeepSeek's discount is off-peak hours (a
schedule), so it is not a Batch price either; it stays in the notes.

## What the page leaves out

- **Priority tiers.** Priority is an account's rate-limit tier, not a price
  per token; DeepSeek's off-peak discount is a schedule, not a tier. Neither
  has a number on the page (see Service tiers).
- **Subscription plans, tool, network and storage fees, negotiated prices.**
- **Tokenizer differences.** The same text is roughly 30% more tokens on
  Claude 4.7 and later; equal unit prices are not equal cost per request.

## Data

[`/api/prices`](../site/worker.js:387) reduces models.dev's catalog to the
five first-party vendors' text models that have a price, with the parts
`catalog.Price` holds: input, output, cache read, a 5-minute write, a 1-hour
write and context tiers. Resellers are left out: their price for the same
model is another question, with its own margin.

The app prices the same models from the same catalog
([`Price`](../internal/catalog/catalog.go:143),
[`CostSplit`](../internal/catalog/catalog.go:289)), so the page's number and
the Usage page's number should agree for the same mix. They are still
different questions: the page is a forecast at list prices, the Usage page
prices what was actually spent, custom prices and price rates included.

## The in-app planner

The Usage page's Overview now carries the question the website page can't
answer, because it doesn't know the reader: "what would my usage cost on
another model?" [`/api/usage/planner`](../internal/gui/planner.go) reads the
period's own totals and prices them at each first-party model's base list
price — the same five vendors, the same reduction — cheapest first. The price
is the base tier, since a period's tokens summed over many requests reach no
single request's long-prompt tier, and a token kind a model doesn't bill is
priced as input (a model with no cache-read price doesn't cache). The
comparison the planner draws is a forecast at list prices, not the effective
price a user's custom override would have charged.

## The mockup

`outer/index.html` is a single static file with the same core: budget, hit
rate, output share and a write mode, five vendors, hard-coded prices read on
one date. What it adds:

- **A pricing map**: input price against output price on log axes, bubble
  size the capacity. Both prices already ship in `/api/prices`.
- **A write-mode comparison**: each vendor's flagship at no write, short TTL
  and long TTL. `cacheWrite` and `cacheWrite1h` already ship.
- **A service-tier switch** (standard, batch, priority/fast). The batch 50%
  is real; the "fast / 高峰" 2× tier is not — no vendor prices priority per
  token, so the page keeps only Standard and Batch.
- **Vendor caveats** as prose: peak-hour pricing, which models accept fast
  mode, prompt-length tiers, tokenizer drift.

What it lacks: live prices (24 models hard-coded, and the hero says 22),
translations, light and dark themes, URL state, prompt-size tiers, and a
data source that stays current.

## Options

| Option | Work | Risk |
| --- | --- | --- |
| The map and the write comparison | Done: front-end only; both inputs already in `/api/prices` | Low |
| The vendor caveats | Done: the page's five notes | Claims need checking against the catalog; the mockup's "v4-pro bills at Flash prices" contradicts the catalog's own $0.66/$1.98 |
| Add service tiers | Done: Standard and Batch only; Batch is a vendor-level 50% (OpenAI, Anthropic, GLM) in `priceList`'s `BATCH` map, not a per-model table | Priority has no per-token price to record; DeepSeek's off-peak is a schedule, left in the notes |
| An in-app planner in the Usage page | Done: `/api/usage/planner` prices the period's tokens at each first-party model's base list price, drawn as a section in the Usage Overview, four languages, a Go test and a browser test | The real differentiator: the user's own hit rate and mix, the app's effective prices |

Recommended order: everything is in now — the map, the write comparison and
the caveats were already there; service tiers are Standard and Batch; and the
in-app planner answers "what would my usage cost on another model" from the
reader's own usage, which the page alone could not.
