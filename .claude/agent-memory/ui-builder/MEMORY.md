# ui-builder memory

This file is committed to a public repository. Never record personal paths, hostnames, IP addresses, email addresses, API keys or details of anyone's private infrastructure or media library.

Curate this file: keep it under 150 lines, one fact per bullet, date each entry (YYYY-MM-DD), and replace an outdated fact rather than adding a contradicting one.

- 2026-09-27: templ trims leading spaces in element text; write `{ " jobs" }` when a space must survive.
- 2026-09-27: inside a templ component, `ctx` already has its children cleared; to pass children on, wrap them in another component.
- 2026-09-27: headless Chrome clamps `--window-size` to at least 500px wide; set phone widths with CDP device metrics (Emulation.setDeviceMetricsOverride) instead.
- 2026-09-27: a native `<progress>` masked with `mask-size: calc((100% + 2px) / 20)` gives 20 even meter segments.
- 2026-09-27: htmx does not set `aria-busy`; components.css styles `.btn.htmx-request` and `form.htmx-request .btn[type=submit]` as busy.
