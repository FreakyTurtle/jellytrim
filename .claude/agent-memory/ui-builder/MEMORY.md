# ui-builder memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.

- 2026-09-27: templ trims leading spaces in element text; write `{ " jobs" }` when a space must survive.
- 2026-09-27: inside a templ component, `ctx` already has its children cleared; to pass children on, wrap them in another component.
- 2026-09-27: headless Chrome clamps `--window-size` to at least 500px wide; set phone widths with CDP device metrics (Emulation.setDeviceMetricsOverride) instead.
- 2026-09-27: a native `<progress>` masked with `mask-size: calc((100% + 2px) / 20)` gives 20 even meter segments.
- 2026-09-27: htmx does not set `aria-busy`; components.css styles `.btn.htmx-request` and `form.htmx-request .btn[type=submit]` as busy.
- 2026-09-27: `details::details-content { content-visibility: visible }` keeps a `<details>` open on wide screens, so one element can be a folding filter panel on phones only.
- 2026-09-27: in collapsing tables every child of a cell becomes a grid item; wrap multi-part text ("~" plus a value) in one `<span>`.
- 2026-09-27: `ul[class]` in base.css beats a single-class selector on padding; use a two-class selector.
- 2026-09-27: gosec G710 flags `redirect()` when a caller passes a value derived from the request; redirect only to paths built from IDs looked up in the store.
- 2026-09-28: to stop HTMX polling closing an open confirm dialog, filter the trigger: `every 2s [!document.querySelector('#queue-live dialog[open]')]`.
- 2026-09-28: stop polling when idle by rendering the swapped fragment without `hx-trigger`; the outerHTML swap removes the old poller.
- 2026-09-28: announce polled changes once: the fragment's hx-get carries `?was=<signature>`, and the server adds an `hx-swap-oob="innerHTML"` element for a live region outside the fragment only when it changed.
- 2026-09-28: no-JS confirm dialogs: trigger is `<a href="#dialog-id" data-dialog-open>`, and `html:not(.js) .dialog:target` shows it (queue.css).
- 2026-09-28: web tests' `templEscape` escapes quotes, so check attribute markup with a raw strings.Contains.
- 2026-09-28: htmx 2 moves focus to an `autofocus` element in swapped content, and keeps focus on any element whose id survives the swap.
- 2026-09-28: `hx-trigger="input consume"` on a wrapper stops a parent form's own trigger (for example a live preview) firing as well.
- 2026-09-28: in a form with several submit buttons, put a visually hidden Save submit first so Enter saves.
- 2026-09-28: no-JS dialog fallback: link to `#dialog-id` and style `dialog:target { display: block; position: static }`.
- 2026-09-28: a form with per-row submit buttons (Remove) needs a visually hidden default submit first, or Enter in a text field presses the first row's Remove.
- 2026-09-28: a toggle carrying `data-dialog-open` opens the confirm dialog and app.js's preventDefault keeps it checked; without JS the form posts and the server asks instead (Settings Dry Run).
- 2026-09-28: HTMX does not swap 4xx responses by default; fragment endpoints (connection test, path check) answer 200 and put the error in a callout.
- 2026-09-28: `.metrics` is 2 columns on phones and 4 at 1080px; a 3-metric group needs a page override or an ink-filled empty cell shows.
- 2026-09-28: Chrome time inputs lose :focus-visible when focus moves to the minute part; draw the ring on :focus-within too.
- 2026-09-28: set a non-200 status with a helper that sets Content-Type before WriteHeader; s.render sets it too late.
