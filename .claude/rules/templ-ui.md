---
paths:
  - "internal/web/**"
---

# Web UI (templ, HTMX, CSS)

Read `docs/UI.md` for the design system. Summary of the rules:

- **Handlers are thin.** Parse the request, call a service, render a view. No business decisions in handlers or templates. View models are built in the handler from domain results.
- **templ** components live in `internal/web/views/`. Shared components (panel, metric, stat row, status lamp, toggle, segmented control, table, badge) are in `components.templ`. Use them; add new shared pieces there.
- **HTMX** for partial updates. Fragment handlers return only the fragment. Mutations are `POST` (or `DELETE`/`PUT` via `hx-*`) and respond with the updated fragment or an `HX-Redirect`. Pages still work as full page loads.
- **CSS** is hand-written in `internal/web/static/css/` using the custom properties (tokens) in `tokens.css`. No utility frameworks, no inline styles except CSS custom property values. Class names are plain and component-based (`.panel`, `.metric`, `.metric__value`).
- **JavaScript** only where HTMX and CSS cannot do it, in `internal/web/static/js/app.js`. No build step, no packages.
- **Vendored files** (`static/vendor/`: htmx, fonts) are never edited. Update by replacing with an upstream release and recording the version and licence.
- **Accessibility.** Semantic elements, `<label>` for every control, visible focus (`:focus-visible` ring from tokens), contrast of at least 4.5:1 for text, ARIA only when HTML cannot express it. Status is never shown by colour alone: pair colour with a word or icon shape.
- **Copy.** Plain British English. No emojis. No em dashes. The normal UI never shows CRF, QP, ICQ or FFmpeg flags; those belong under Advanced or in technical details.
- **Secrets.** Never render the Jellyfin API key, even masked with its real length. Settings shows "Saved" and an empty field that replaces it only when filled. Artwork goes through `/img/{id}` so the browser never talks to Jellyfin.
- **Security.** All state-changing requests are protected by `http.CrossOriginProtection`. Never trust form values for file paths: look items up by ID.
- After editing `.templ` files, run `task generate`.
