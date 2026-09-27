---
name: verify-ui
description: Check JellyTrim pages in a real browser with the Playwright MCP at phone and desktop widths: renders, no console errors, no horizontal overflow, keyboard focus visible, HTMX actions work.
argument-hint: "[page paths, default: all top-level pages]"
---

1. Make sure the app is running: `task dev` (local) or the dev stack (`/dev-stack`). JellyTrim is on `http://localhost:8080`; the dev Jellyfin is on `:8096`.
2. For each page in `$ARGUMENTS` (default: `/`, `/library`, `/policies`, `/queue`, `/history`, `/settings`):
   - Load it at 1440x900 and at 390x844 with the Playwright MCP.
   - Check the console for errors and failed requests.
   - Check `document.documentElement.scrollWidth <= window.innerWidth`.
   - Tab through it: focus must always be visible and in a sensible order.
   - Trigger the main HTMX action on the page and check the result.
   - Take a screenshot of each width into the scratchpad.
3. Report per page: ok, or each problem with the width and a fix.
