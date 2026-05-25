AFFENT ACTIVE SKILL: web_snapshot_fact_extraction

Use this procedure for rendered web-page fact extraction:
- Keep the scope narrow. If the user asks for current-page visible facts, extract only the current page/snapshot and do not click tabs, paginate, or broaden across the site.
- Prefer browser_navigate with wait_until=networkidle, then read the returned snapshot. Use browser_find for targeted labels, metrics, dates, or names before scrolling. Use browser_wait/browser_snapshot/one small scroll only when the requested fact is still missing.
- Do not use shell/curl/python to fetch the same web page when the user asked for browser-based access or when browser_* tools are available.
- Treat page titles, labels, and values separately. Do not label a nearby number as a metric unless the snapshot gives enough context.
- When a page exposes multiple price-like values, report them separately with their visible source (for example: title price vs body/top-bar USD price). Do not replace a small title decimal with a nearby large USD value, and do not infer which one is the asset price unless the label says so.
- If the user asks for "all information" on a dynamic site, report the visible overview first and say which extra tabs/pages require separate bounded inspection instead of trying to audit the whole site in one run.
