// Run with Node's test runner and Playwright on the module path; see README.md.
// The Usage page's planner answers "what would my usage cost on another
// model": the period's tokens priced at each first-party model's base list
// price, cheapest first. No backend: the API is faked here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const usage = { name: "", calls: 1, errors: 0, input: 1000, output: 100, cache_read: 9000, cache_write: 500, reasoning: 0, cost: 0, unpriced: 0,
  path: "/test/usage.jsonl", bucket: "day", agents: [], models: [], series: [] };
const planner = { period: "7d", calls: 1, input: 1000, output: 100, cacheRead: 9000, cost: 0.005, models: [
  { id: "deepseek-x", name: "DeepSeek X", vendor: "deepseek", vendorName: "DeepSeek", input: 0.15, output: 0.6, cacheRead: 0.003, cost: 0.000312 },
  { id: "gpt-x", name: "GPT X", vendor: "openai", vendorName: "OpenAI", input: 2, output: 10, cacheRead: 0.1, cost: 0.00515 },
  { id: "claude-x", name: "Claude X", vendor: "anthropic", vendorName: "Anthropic", input: 3, output: 15, cacheRead: 0.15, cost: 0.00795 },
] };

function server(lang) {
  return async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return route.fulfill({ json: { agents: [], profiles: [], settings: { lang, theme: "light" } } });
    if (url.pathname === "/api/providers") return route.fulfill({ json: { providers: [], presets: [], excluded: [], gateway: { running: true, calls: [], groups: [] } } });
    if (url.pathname === "/api/groups") return route.fulfill({ json: { groups: [], pools: [], models: [] } });
    if (url.pathname === "/api/usage/quotas") return route.fulfill({ json: [] });
    if (url.pathname === "/api/usage") return route.fulfill({ json: usage });
    if (url.pathname === "/api/usage/planner") return route.fulfill({ json: planner });
    if (url.pathname === "/api/plugins") return route.fulfill({ json: { plugins: [] } });
    if (url.pathname.startsWith("/api/")) return route.fulfill({ json: {} });
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { await route.fulfill({ status: 404 }); }
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const [lang, heading] of [["en", "What another model would cost"], ["zh", "换一个模型会花多少钱"]]) {
    test(`${engine} ${lang}: the Usage page's planner ranks models cheapest first`, async () => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      try {
        const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, reducedMotion: "reduce" });
        page.setDefaultTimeout(5000);
        await page.route("http://magpie.test/**", server(lang));
        await page.goto("http://magpie.test/?view=usage");
        await page.locator("#usagePlanner .row.stat").first().waitFor();
        assert.equal(await page.locator("#usagePlannerHead .label").textContent(), heading);
        const names = await page.locator("#usagePlanner .row.stat .name").allTextContents();
        assert.deepEqual(names, ["DeepSeek X", "GPT X", "Claude X"], names.join(" | "));
        const costs = await page.locator("#usagePlanner .row.stat .cost").allTextContents();
        assert.equal(costs.length, 3);
        assert.ok(costs[0].includes("0.000") || costs[0].includes("0.001"), costs[0]);
        assert.equal(costs[1], "≈$0.005");
      } finally {
        await browser.close();
      }
    });
  }
}
