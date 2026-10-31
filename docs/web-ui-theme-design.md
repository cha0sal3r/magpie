# Web UI theme and visual design

This document records the design used by magpie-friday's embedded Web UI. The runtime contract remains in [GUI app shell](subsystems/gui-shell.md).

## Goals

1. Keep dense gateway information calm and scannable instead of turning every state into decoration.
2. Change appearance immediately, without a light/dark flash, while preserving the reader's choice across the window, quick panel, browser UI and plugin pages.
3. Make light, dark and system modes equally complete. System is an intent, not a one-time copy of the current OS colour.
4. Give community plugin pages the same semantic surfaces without exposing core CSS or requiring them to copy colours.
5. Keep text, focus, controls and charts understandable without relying on colour alone.

## Theme model

The saved setting has exactly three values:

| Value | Meaning |
| --- | --- |
| `system` | Follow `prefers-color-scheme`; change live when the OS changes. |
| `light` | Keep the light palette regardless of the OS. |
| `dark` | Keep the dark palette regardless of the OS. |

`boot.js` provides the saved intent, then `theme.js` applies `data-theme`, colour-blind mode and text size before styles paint. CSS assigns the matching `color-scheme`. The normal settings path later goes through `applyPrefs`, which cross-fades colours, retints the native panel and Windows title bar, and notifies framed plugin pages. `?theme=light|dark` is a preview override and never changes the saved setting.

Do not persist the currently resolved light/dark value for System mode: doing that would stop the app following a later OS change. A failed settings read keeps the last known appearance; it does not invent a new preference.

## Semantic tokens

Components consume roles, never theme-specific colour literals (the three Appearance swatches are a documented, static preview exception):

- Surfaces: `--bg`, `--bg-paint`, `--card`, `--card-2`, `--pill`, `--pill-hover`
- Content: `--fg`, `--fg-2`, `--muted`, `--faint`, `--ctl-fg`
- Structure: `--line`, `--line-2`, `--shadow`, `--glass-edge`, `--glow`
- Action: `--accent`, `--accent-2`, `--accent-3`, `--accent-soft`, `--accent-grad`, `--accent-fg`, `--sel`
- Status: `--green[-soft]`, `--red[-soft]`, `--amber[-soft]`
- Typography: `--font`, `--code`, with optional installed-font overrides
- Charts: `--c1` through `--c7`; colour-blind mode also differentiates series with line patterns

A component-specific token may be added only when the role is genuinely different across components. Light and dark values are defined together. Community plugin pages currently receive only the compact stable token set in `pluginpages.js`; expanding that bridge is a compatibility change and should be tested with plugin pages.

## Visual language

- **Surfaces:** a soft aurora canvas gives the app identity without putting colour behind every row. Lists, popovers and dialogs are translucent glass panels with a fine edge and restrained glow; rows remain calm inside the glass so dense data stays readable.
- **Accent:** the brand accent is an electric indigo → cyan gradient with a small magenta supporting hue. It is reserved for primary actions, focus, selection, new-state dots and the Appearance previews; content and navigation still use neutral ink so statuses do not fight the brand.
- **Buttons:** primary actions use the accent gradient and a short glow; secondary actions use glass pills; destructive actions use red only at the confirmation point. Hover changes surface/ink and press scales slightly. Keyboard focus always has a visible accent ring.
- **Typography:** platform UI fonts are the default, with native installed-family choices in desktop mode. The base is 13px/1.4; page titles and totals create hierarchy through size and weight, not extra colours. IDs, paths, keys and payloads use the code font.
- **Shape and spacing:** 5–8px radii for inline controls, 9–14px for cards and dialogs, a 4/8px spacing rhythm, and 44px minimum targets on coarse pointers.
- **Motion:** short, functional transitions use one easing curve. Theme changes cross-fade; reduced-motion disables nonessential movement.
- **Appearance control:** the three-choice segmented control includes a small surface/accent swatch for System, Light and Dark, while text remains the accessible label.

## Accessibility and compatibility

- Ordinary text aims for WCAG AA 4.5:1; large text 3:1. Focus rings, meaningful icons and control boundaries aim for 3:1 against adjacent colours.
- Status always combines colour with words, icon, dot, shape or line pattern.
- System/Light/Dark remain real buttons with `aria-pressed`; the existing segmented-control keyboard focus and visible focus ring are retained.
- `color-scheme` follows the effective palette so browser-native fields, pickers and scrollbars agree with custom controls.
- No native `<select>`, no coloured left-border state stripes, and clicks do not move the page.
- CSS must continue to work in the project's supported WebKit. New CSS features require the same fallback discipline as existing `color-mix` rules.

## Reference decisions

- [Primer Primitives](https://primer.style/product/primitives) demonstrates semantic colour primitives with automatic, light and dark modes and accessible variants. magpie-friday follows the same separation between semantic roles and mode values.
- [Outline v0.43.0](https://github.com/outline/outline/releases/tag/v0.43.0) shipped a System option that follows OS light/dark changes; magpie-friday likewise keeps System distinct from the resolved colour.
- [next-themes](https://github.com/pacocoursey/next-themes) documents pre-paint theme application, system preference, persistence and forced previews. magpie-friday implements those ideas without adopting its React dependency.
- [WCAG text contrast](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html), [non-text contrast](https://www.w3.org/WAI/WCAG22/Understanding/non-text-contrast.html), and MDN's [`color-scheme`](https://developer.mozilla.org/en-US/docs/Web/CSS/color-scheme) guide set the accessibility and native-control baseline.

## Verification

For a theme or shared visual-token change:

1. Run the theme/palette, Settings General, colour-blind chart, Omarchy, plugin-page and old-WebKit tests in Chromium and WebKit.
2. Run i18n key tests and the UI in English, Simplified Chinese, Traditional Chinese, Japanese and German, including a 440px viewport.
3. Run the whole GUI suite when a shared selector, token, API call or layout breakpoint changes.
4. Inspect light, dark and System under both emulated OS schemes; only System may change when the OS scheme changes.
5. Check first paint, a live switch, reload, reduced motion, keyboard focus, native form controls, title-bar/panel tint and plugin-frame tokens.
