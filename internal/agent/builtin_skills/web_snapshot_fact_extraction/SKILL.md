AFFENT ACTIVE SKILL: web_snapshot_fact_extraction

Use this procedure for rendered web-page fact extraction:
- Keep the scope narrow. If the user asks for current-page visible facts, extract only the current page/snapshot and do not click tabs, paginate, or broaden across the site.
- Prefer browser_navigate with wait_until=networkidle, then read the returned snapshot. Use browser_find for targeted labels, metrics, dates, or names before scrolling. If the snapshot reports partial dynamic content, empty metric widgets, or labels without values, use browser_network to find same-site XHR/fetch responses and browser_network_read before citing hidden JSON/text values. Use browser_wait/browser_snapshot/one small scroll only when the requested fact is still missing.
- If the snapshot is a search result page, treat snippets as discovery only. Open the 1-3 highest-value visible result URLs (official, primary, metrics, docs, or source repositories) before refining the query, and do not cite snippets as verified facts.
- On dynamic dashboards or detail pages, search for the missing field labels first. For market/status pages, use compact queries like `price market cap FDV volume supply TVL`, `24h 7d volume market cap`, or `validators miners stake emission` before scrolling or clicking tabs. Do not keep searching only the entity name once the page identity is already confirmed.
- Do not cite browser_network previews as final evidence. Read the selected ref with browser_network_read and cite the returned browser_network_url/source_method.
- Do not use shell/curl/python to fetch the same web page when the user asked for browser-based access or when browser_* tools are available.
- Treat page titles, labels, and values separately. Do not label a nearby number as a metric unless the snapshot gives enough context.
- When a page exposes multiple price-like values, report them separately with their visible source (for example: title price vs body/top-bar USD price). Do not replace a small title decimal with a nearby large USD value, and do not infer which one is the asset price unless the label says so.
- When extracting metrics, preserve the exact visible numeric string and unit. Do not round, normalize, or backfill missing precision from memory; if the page is noisy, verify the row label/ticker/id before trusting the number.
- If the user asks for "all information" on a dynamic site, report the visible overview first and say which extra tabs/pages require separate bounded inspection instead of trying to audit the whole site in one run.
