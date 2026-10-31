const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");

const source = fs.readFileSync(path.resolve(__dirname, "../assets/theme.js"), "utf8");
const css = fs.readFileSync(path.resolve(__dirname, "../assets/app.css"), "utf8");

function apply(saved, asked = "") {
  const values = new Map(asked ? [["theme", asked]] : []);
  const style = new Map();
  const root = { dataset: {}, style: { setProperty: (name, value) => style.set(name, value) } };
  const context = {
    URLSearchParams: class { get(name) { return name === "theme" ? values.get(name) || null : null; } },
    location: { search: asked ? `?theme=${asked}` : "" },
    document: { documentElement: root },
    window: { bootPrefs: { theme: saved, textSize: 110, web: false } },
  };
  vm.runInNewContext(source, context);
  return { root, style };
}

test("theme intent is applied before paint without resolving System into a saved colour", () => {
  const system = apply("system");
  assert.equal(system.root.dataset.theme, undefined);
  assert.equal(system.style.get("--zoom"), 1.1);

  assert.equal(apply("light").root.dataset.theme, "light");
  assert.equal(apply("dark").root.dataset.theme, "dark");
});

test("a URL theme previews over the saved intent without rewriting boot preferences", () => {
  const preview = apply("dark", "light");
  assert.equal(preview.root.dataset.theme, "light");
  assert.equal(preview.style.get("--zoom"), 1.1);
});

test("an unknown URL theme is ignored instead of becoming a fake theme", () => {
  const preview = apply("system", "bogus");
  assert.equal(preview.root.dataset.theme, undefined);
});

test("native controls follow explicit and system themes", () => {
  assert.match(css, /:root \{\n  color-scheme: light;/);
  assert.match(css, /:root\[data-theme="light"\] \{ color-scheme: light; \}/);
  assert.match(css, /:root\[data-theme="dark"\] \{\n  color-scheme: dark;/);
  assert.match(css, /@media \(prefers-color-scheme: dark\) \{\n  :root:not\(\[data-theme="light"\]\) \{\n    color-scheme: dark;/);
});
