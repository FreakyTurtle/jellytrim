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
- 2026-09-28: templ accepts an inline component call in a cell (`<td>@ValueOr(x, "None")</td>`); `ValueOr` in components.templ is the shared never-blank cell.
- 2026-09-28: a phone-only 44px hit area for a text link: `display: flex; align-items: center; min-block-size: var(--touch-target)` inside the `max-width: 719.98px` query; desktop stays unchanged.
- 2026-09-28: auto mode refuses writing settings (such as dry_run) into a devseed config's database; to see a Dry-Run-off-only control, inject its rendered markup through CDP instead.
- 2026-09-28: the unlit lamp is a solid `--rule-soft` square with a matching edge; a light fill inside an ink border read as an unticked checkbox.
- 2026-09-28: a grid that must work at phone and desktop widths: place items from inline `--d`/`--h` custom properties and swap the orientation in a container query (ScheduleGrid); one DOM, no sideways scroll.
- 2026-09-28: an accent focus ring vanishes on accent-filled neighbours; the schedule grid uses a 2px ink outline and raises the cell with `:has(:focus-visible) { z-index }`.
- 2026-09-28: a grid of many checkboxes needs a roving tab stop (tabIndex 0/-1 plus arrow keys in app.js), or Tab takes 168 presses to leave it.
- 2026-09-28: drag-painting labels: preventDefault on pointerdown does not stop the following click, so swallow that click (detail > 0) or the label toggles the cell back.
- 2026-09-28: `time.Local.String()` is always "Local"; name the zone from `TZ` plus `now.Zone()` instead.
- 2026-09-28: the Playwright MCP may be absent; `require()` the cached playwright under ~/.npm/_npx with `executablePath` set to the installed Google Chrome.
- 2026-09-28: never write `cat > file` without a heredoc in Bash; it waits on stdin until the call times out.
- 2026-09-28: keep a heavy panel inside a polled outerHTML fragment with an `hx-preserve` slot (empty in the poll response) and let an inner body poll itself; after actions swap the body out of band (Queue Backlog).
- 2026-09-28: `hx-vals` on the polled container carries state (waiting list length) into every poll and child action; a child's own `hx-vals` wins (Show more).
- 2026-09-28: templ puts spaces round an interpolated value next to elements; raw test strings look like `6 <span class="metric__unit">`.
- 2026-09-28: a long folder path with no spaces widens the page at 390px; `.callout > p` and `.problems__text` now use `overflow-wrap: anywhere`.
- 2026-09-28: Metric prefixes "Estimate." unless the note already contains "Estimate"; write notes as "Estimate: ..." or "Estimate at ..." to avoid "Estimate. Of ...".
- 2026-09-28: the store has no running-jobs query; InterruptedJobs filtered by `Active()` avoids ActiveJobs, which loads every waiting job.
- 2026-09-28: to test SpaceHold, Start a queue with a fake SpaceFS (tiny Free) and Dry Run off; it holds the job without encoding anything.
- 2026-09-28: never put throwaway Go programs (seeders, harnesses) under the repo's tmp/ or dev/: `./...` includes them and lint fails. Put them in the session scratchpad, outside the module.
