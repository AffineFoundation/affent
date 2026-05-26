# Browser Access Architecture

This document defines the target design for Affent web access. The goal is not
to add per-site fallbacks, but to make rendered web access a first-class runtime
subsystem that can inspect pages the way a careful human would: wait for useful
content, read visible information, notice missing or gated content, inspect
network-backed data when appropriate, interact with controls, and return compact
evidence instead of raw HTML.

## Problem

Current browser access is useful but too shallow for modern dashboards. A page
such as `taostats.io` can load a React shell, expose a useful title, show labels
like `Market Cap`, and still hide the actual metric values behind empty custom
elements, Turnstile overlays, canvas widgets, virtualized tables, or API-backed
state. If the tool returns that as ordinary verified page text, the agent may
hallucinate from labels or miss that the source was only partially accessible.

Affent needs a general browser-reading pipeline that is honest about evidence
quality and compact enough for small and medium models.

## Principles

- Treat browser access as evidence extraction, not HTML dumping.
- Separate acquisition, observation, extraction, interaction, and evidence
  reporting.
- Prefer visible rendered text, accessibility labels, page metadata, and
  bounded network/API evidence over raw DOM/HTML.
- Preserve raw artifacts for audit, but feed the model compact observations.
- Make blocked, gated, partial, loading, and discovery-only states explicit.
- Never solve CAPTCHAs, bypass paywalls, or hide that a source is unavailable.
- Optimize for repeatable evals: every browser capability needs deterministic
  fixtures before relying on live sites.

## Target Pipeline

### 1. Source Acquisition

Every page read should start with a source access record:

- `requested_url`
- `final_url`
- `accessed_at`
- `mode`: `direct_text`, `rendered_browser`, `rendered_network`, `visual`
- `status`: `verified`, `partial`, `blocked`, `loading`, `not_found`,
  `search_discovery`, `unverified_discovery`
- `diagnostics`: bounded machine-readable warnings

Direct HTTP remains useful for articles, docs, and static pages, but rendered
browser access should be the primary path for dashboards, apps, and pages marked
as JavaScript-required by search or direct fetch.

### 2. Rendered Observation

`browser_observe` should become the core page-reading primitive. It should
combine:

- Visible DOM text from `innerText`, not just selected tags.
- Accessibility tree names, roles, values, checked/expanded states.
- Metadata: title, canonical URL, description, OpenGraph/Twitter fields.
- Interactive refs for links, buttons, tabs, selects, inputs, menus, pagination.
- Shadow DOM and common custom elements.
- Diagnostics for empty metric widgets, loading skeletons, overlays, bot
  challenges, cookie walls, login walls, virtualized tables, and hidden content.
- Optional saved artifacts: raw DOM, screenshot, and network log.

The model should see a compact structured observation, for example:

```text
SourceAccess: browser_rendered_url=...; status=partial; snapshot_id=...
PAGE STATUS:
- partial: cloudflare_turnstile_visible
- partial: 3 empty metric widgets exposed labels without values
PAGE METADATA:
- title: 0.0631 · SN120 · Affine · taostats ...
VISIBLE TEXT:
- Market Cap
- 24hr Volume
INTERACTIVE:
[1] tab "Statistics"
[2] button "Connect Wallet"
NEXT:
- use browser_network for same-origin JSON/API responses, or mark missing fields unverified
```

Raw HTML should not be shown to the model unless explicitly requested. If stored,
it should be an artifact with a short preview and source hash.

### 3. Network Evidence

Modern dashboards often render from JSON/API requests. Browser sessions should
capture bounded same-origin `fetch`/XHR responses and expose them through a
separate evidence view:

- URL, method, status, content type, size, initiator frame.
- JSON key preview and selected value snippets.
- Relevance search over captured responses.
- Artifact path for full bounded response when safe.

This should not blindly dump all network traffic. The tool should filter to
same-origin or explicitly allowed domains, skip analytics/ads/fonts/images, cap
payloads, redact obvious secrets, and require the model to cite the response URL.

Recommended tool shape:

- `browser_network(query, max_results)`: search captured responses.
- `browser_network_read(ref_or_url, json_path?, max_bytes?)`: read a selected
  response or JSON subtree.

Initial implementation:

- Browser sessions capture bounded same-site XHR/fetch JSON/text responses from
  Chrome network events.
- Large JSON/text responses are truncated into bounded evidence entries rather
  than discarded solely because the full body is too large for model context.
- `browser_network` returns compact refs and previews only.
- `browser_network_read` returns bounded response bodies with
  `SourceAccess: browser_network_url=...; source_method=network_xhr_fetch`.
- Search/result previews are discovery aids; values should be cited only after
  reading a selected response.

For taostats-like pages, this lets the agent distinguish:

- visible page title says `SN120 · Affine`
- body text lacks metric values
- same-origin API may contain chart/config/subnet data
- if not found, metric fields remain unverified

### 4. Interaction Model

The browser should support human-like bounded interaction without making the
model improvise:

- `browser_click(ref)`, `browser_type(ref, text)`, `browser_select(ref, value)`.
- `browser_scroll(direction, amount)` with fresh snapshot after the scroll.
- `browser_find(query)` searches the current rendered text and captured visible
  refs.
- Tab/pagination controls should be highlighted as interaction candidates.
- Stale refs and hidden/covered elements must return actionable failure kinds.

A page-reading recipe should guide models:

1. Open page.
2. Observe.
3. If partial/loading, wait once or search network evidence.
4. If tabbed, click only the relevant tab.
5. If values remain unavailable, report the gap.

### 5. Visual Layer

Some dashboards expose values only in canvas/SVG or screenshot-rendered widgets.
Affent should add an optional visual layer:

- Screenshot artifact by default, not inline base64.
- Lightweight OCR/layout extraction when enabled.
- Vision-model path only when configured, with strict token/image budget.
- Output as structured visible text boxes with coordinates and confidence.

This is optional because many small models cannot use images directly, but the
artifact is still valuable for human/WebUI debugging.

### 6. Evidence Contract

Every extracted claim should carry:

- `claim`
- `value`
- `source_url`
- `source_method`: DOM, accessibility, metadata, network, OCR, user-provided
- `source_ref`: snapshot id, network ref, DOM ref, screenshot region, or artifact
- `status`: verified, partial, blocked, inferred, unverified
- `confidence`

The final answer should cite only verified or explicitly partial evidence. A
title-only page is weak evidence for identity, not evidence for all metrics.

## Tool Surface

The current tools can evolve without exposing a large confusing surface:

- Keep `browser_navigate`, but make it return `browser_observe` output.
- Keep `browser_snapshot` as an explicit refresh/observe.
- Keep `browser_find`, but search over visible text plus accessibility text.
- Add `browser_network` and `browser_network_read`.
- Keep `browser_screenshot` artifact-first.
- Optionally add `browser_extract` later as a high-level typed extractor that
  returns claims and evidence for a user objective.

Avoid a generic “dump HTML” tool. If raw HTML is needed for debugging, store it
as an artifact and show only a bounded preview.

## Runtime State

Browser sessions should be tied to Affent sessions:

- Per-session cookies/cache/local storage, isolated between sessions.
- Optional persistent profile for long-running authenticated workflows.
- Network log, snapshots, screenshots, and DOM artifacts under the session
  artifact directory.
- Snapshot reads should wait only a short bounded window for pending network
  observer body reads to settle, so JavaScript dashboards expose captured
  XHR/fetch refs without making browser tools feel stuck.
- WebUI should show page status, current URL, screenshots, network evidence,
  and source-access warnings.

## Eval Plan

Add deterministic browser fixtures before relying on live sites:

- Static article: direct text path should win.
- React hydration: values appear after delayed XHR.
- Empty custom metric widget: report partial, do not hallucinate value.
- Virtualized table: scroll/find recovers offscreen row.
- Shadow DOM component: observe sees shadow text.
- Canvas/SVG metric: visual layer or explicit unverified gap.
- Search result page: discovery-only status.
- 404 page: not-found discovery-only status.
- Cloudflare/Turnstile fixture: blocked status with no verified SourceAccess.
- API-backed dashboard: network evidence recovers values with cited response URL.

Live regression cases such as taostats should be separate from deterministic CI:
use them as smoke tests and record source status, not as hard pass/fail unit
tests.

## Implementation Roadmap

### Phase 1: Honest Observation

- Replace tag-only DOM extraction with a richer observe model using visible
  `innerText`, accessibility names, metadata, diagnostics, and refs.
- Mark challenge overlays, login walls, cookie walls, empty dynamic metric
  widgets, and loading skeletons as partial/blocked states.
- Ensure `browser_navigate` and `web_fetch` rendered fallback propagate those
  states instead of marking poor snapshots as verified evidence.

### Phase 2: Network Evidence

- Capture bounded same-origin XHR/fetch responses during browser sessions.
- Add `browser_network` and `browser_network_read`.
- Keep network capture asynchronous, but make snapshot formatting wait for the
  capture queue to become briefly idle before reporting available network refs.
- Store full safe responses as artifacts and give the model compact previews.
- Add eval fixtures for API-backed dashboards.

### Phase 3: Interaction Recipes

- Add browser-reading workflow guidance for tabbed dashboards, pagination,
  scrolling, and search-within-page.
- Add loop guards for repeated page opens, repeated no-match finds, and repeated
  broad scrolling.
- Add WebUI rendering for page status and network evidence.

### Phase 4: Visual Evidence

- Add screenshot artifact capture as a default debug artifact on partial pages.
- Add optional OCR/vision extraction behind configuration.
- Add eval fixtures for canvas/SVG-only metrics.

## Non-Goals

- No CAPTCHA solving.
- No paywall bypassing.
- No credential scraping.
- No unrestricted raw HTML dumping into model context.
- No site-specific taostats/coingecko/etc. special-case scraper in core runtime.
