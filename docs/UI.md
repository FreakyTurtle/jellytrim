# JellyTrim UI

This is JellyTrim's design system and page specification. It is for anyone building or reviewing pages. It covers the visual language, the design tokens, the shared components and what each page must do.

The pages are built milestone by milestone (see `docs/MILESTONES.md`). Until a page ships, its section here is the specification it is built against. The implementation lives in:

- `internal/web/static/css/tokens.css`: the tokens in this document, as CSS custom properties.
- `internal/web/static/css/`: the rest of the hand-written CSS, one file per component group.
- `internal/web/views/components.templ`: the shared components.
- `internal/web/views/`: one templ file per page.

The coding rules for this area are in `.claude/rules/templ-ui.md`.

## Visual language

**Swiss-inspired, playful industrial minimalism.** Two references shape it:

- **The International Typographic Style.** Clear grids, strong type, flush-left text, purposeful whitespace, and asymmetry where it helps the reader.
- **Industrial product design** in the spirit of small hardware synthesisers and lab instruments. Panels with printed equipment labels, status lamps, chunky switches, segmented meters, and big numeric read-outs.

In practice:

- A warm off-white page, near-black ink and one signal-orange accent.
- Strong 1px and 2px borders. No shadows, no gradients, almost no radius.
- Bold numbers in tabular figures. Compact uppercase labels.
- IBM Plex Sans for words, IBM Plex Mono for anything technical (codecs, sizes, paths, commands).
- Controls that feel like hardware: they travel 1px when pressed and click into place.
- Decoration only when it carries meaning. A lamp is lit because something is on.

It must not look like:

- a Bootstrap admin template or a generic SaaS dashboard (rounded cards, soft shadows, pastel charts);
- a gamer UI (dark neon, glow, angled panels);
- a Plex or Jellyfin clone (dark background, poster walls as the main layout);
- a Tailwind template (grey-100 cards, rounded-xl, indigo buttons).

Posters are small and supporting. The data is the hero.

## Tokens

All tokens are CSS custom properties on `:root` in `tokens.css`. Components use only these names, never raw values. This keeps a future dark theme to one block of overrides.

### Colour

| Token | Value | Use | Contrast on `--bg` |
|---|---|---|---|
| `--bg` | `#F4F1EA` | Page background (warm off-white) | |
| `--surface` | `#FBFAF6` | Panels, inputs, table rows | |
| `--surface-sunk` | `#ECE8DF` | Wells, disabled controls, placeholders, code blocks | |
| `--ink` | `#141414` | Body text, headings, icons | 16.3:1 |
| `--ink-2` | `#55524B` | Secondary text, hints, captions | 6.9:1 |
| `--rule` | `#141414` | Borders and dividers that define structure | |
| `--rule-soft` | `#CFC9BC` | Row dividers and quiet separators inside a panel | decorative only |
| `--accent` | `#FF4F00` | Signal orange: fills, lamps, meters, the active nav marker, focus ring | 2.9:1 (never text) |
| `--accent-hover` | `#FF6B2B` | Primary button hover fill | |
| `--accent-ink` | `#B33A00` | Accent-coloured text and links | 5.3:1 |
| `--on-accent` | `#141414` | Text and icons on an `--accent` fill | 5.6:1 on accent |

Status colours. Each has a pale tint for backgrounds (callouts, row highlights). The status colour is safe as text on `--bg`, `--surface` and its own tint.

| Token | Value | Tint token | Tint value | Contrast on `--bg` / tint |
|---|---|---|---|---|
| `--ok` | `#1E7A3C` | `--ok-tint` | `#E5EFE5` | 4.8:1 / 4.6:1 |
| `--warn` | `#8A5C00` | `--warn-tint` | `#F7EDD3` | 5.2:1 / 5.0:1 |
| `--bad` | `#B42318` | `--bad-tint` | `#F9E3DF` | 5.8:1 / 5.4:1 |
| `--info` | `#2450B2` | `--info-tint` | `#E6ECF7` | 6.5:1 / 6.2:1 |

Rules:

- `--accent` is never used for text on `--bg` or `--surface`. It fails contrast at small sizes. Use `--accent-ink` for orange text.
- An `--accent` fill always has a 1px or 2px `--rule` border, so its edge meets the 3:1 contrast needed for controls.
- Status text is not placed on `--surface-sunk` (`--ok` drops to 4.4:1 there). Put it on `--bg`, `--surface` or its tint.
- `--warn` is darker than a typical amber (`#9A6700` gives only 4.3:1 on `--bg`). Keep it at `#8A5C00` or darker.

### Typography

Fonts are self-hosted woff2 files in `internal/web/static/vendor/fonts/`, under the SIL Open Font Licence. No font CDN.

| Token | Family | Weights |
|---|---|---|
| `--font-sans` | `"IBM Plex Sans", system-ui, sans-serif` | 400, 500, 700 |
| `--font-mono` | `"IBM Plex Mono", ui-monospace, monospace` | 400, 500 |

Each `@font-face` uses `font-display: swap`.

| Token | Size | Use | Style |
|---|---|---|---|
| `--text-label` | 0.75rem | Labels, panel tabs, table headers, badges | Uppercase, 0.08em tracking, weight 500 |
| `--text-sm` | 0.875rem | Hints, captions, dense tables | |
| `--text-body` | 1rem | Body text, controls | Line height 1.5 |
| `--text-lg` | 1.25rem | Panel titles, list item titles | Line height 1.3 |
| `--text-xl` | 1.5rem | Section headings | Line height 1.25 |
| `--text-2xl` | 2rem | Page titles | Weight 700, line height 1.1 |
| `--text-3xl` | 3rem | Secondary metrics, wizard step numbers | Weight 700, line height 1 |
| `--text-metric` | 4.5rem | Dashboard metrics | Weight 700, tabular figures, -0.03em tracking, line height 0.95 |

- Every number that can change (sizes, counts, percentages, times) uses `font-variant-numeric: tabular-nums`, so digits do not jump as they update.
- Mono is for technical values: codecs, containers, resolutions, sizes in tables, paths, languages, commands, versions.
- Text is flush left. Nothing is centred except a single empty-state message.
- Line length for prose is at most 70 characters (`max-inline-size: 70ch`).

### Space

A 4px base.

| Token | Value |
|---|---|
| `--space-1` | 4px |
| `--space-2` | 8px |
| `--space-3` | 12px |
| `--space-4` | 16px |
| `--space-5` | 24px |
| `--space-6` | 32px |
| `--space-7` | 48px |
| `--space-8` | 64px |

### Layout

| Token | Value | Use |
|---|---|---|
| `--grid-columns` | 12 | Page grid |
| `--gutter` | 24px | Gap between columns and between panels |
| `--max-width` | 1440px | Widest content area |
| `--rail-width` | 200px | Left navigation rail |

Breakpoints (CSS cannot read custom properties in media queries, so these are fixed values):

- **Below 720px:** one column, bottom navigation bar, tables become cards, 16px page margin.
- **720px to 1079px:** the rail shows index numbers and short labels, panels take 6 or 12 columns.
- **1080px and above:** the full rail and the page layouts described below.

Pages define their own grid areas (for example `.dashboard__metrics`). There are no utility classes such as `.span-6` or `.mt-4`.

### Shape, borders and focus

| Token | Value | Use |
|---|---|---|
| `--radius` | 2px | Buttons, inputs, badges, lamps |
| `--radius-panel` | 0 | Panels, tables, callouts |
| `--border` | 1px solid var(--rule) | Default border |
| `--border-strong` | 2px solid var(--rule) | Panel headers, primary buttons, emphasis |
| `--border-soft` | 1px solid var(--rule-soft) | Row dividers |
| `--focus-ring` | 2px solid var(--accent) | Focus outline, with `outline-offset: 2px` |

- No `box-shadow` anywhere. Depth comes from borders and fills.
- Focus uses `:focus-visible` on every interactive element. The ring is never removed without a replacement.

### Motion

| Token | Value |
|---|---|
| `--dur` | 120ms |
| `--dur-slow` | 240ms (the lamp pulse and meter fill only) |
| `--ease` | `cubic-bezier(0.2, 0, 0, 1)` |

- Motion is short and has a job: confirm a press, show progress, show a change of state.
- Nothing slides in, bounces or fades for decoration.
- Under `prefers-reduced-motion: reduce`, transitions drop to 0ms, the lamp stops pulsing (it stays lit) and pressed controls do not move.

### Dark mode

Not in the MVP. The tokens are semantic (`--bg`, `--ink`, `--surface`), not literal (`--cream`, `--black`), so a dark theme is one `@media (prefers-color-scheme: dark)` block that overrides them. Components must not use raw colour values, or that block will not reach them.

### Example

```css
:root {
  --bg: #F4F1EA;
  --surface: #FBFAF6;
  --surface-sunk: #ECE8DF;
  --ink: #141414;
  --ink-2: #55524B;
  --rule: #141414;
  --rule-soft: #CFC9BC;
  --accent: #FF4F00;
  --accent-hover: #FF6B2B;
  --accent-ink: #B33A00;
  --on-accent: #141414;
  --ok: #1E7A3C;    --ok-tint: #E5EFE5;
  --warn: #8A5C00;  --warn-tint: #F7EDD3;
  --bad: #B42318;   --bad-tint: #F9E3DF;
  --info: #2450B2;  --info-tint: #E6ECF7;
  /* type, space, layout, shape and motion tokens follow the tables above */
}
```

## App shell

Every page after setup uses the same shell.

```html
<body class="shell">
  <a class="skip-link" href="#main">Skip to content</a>
  <header class="topbar">
    <a class="wordmark" href="/">JELLYTRIM</a>
    <a class="dryrun" href="/settings#dry-run">
      <span class="lamp lamp--active-steady" aria-hidden="true"></span>
      <span class="dryrun__label">Dry Run</span>
    </a>
    <p class="topbar__sync">Synced 12 min ago</p>
  </header>
  <nav class="rail" aria-label="Main">
    <ol>
      <li><a href="/" aria-current="page"><span class="rail__index">01</span> Dashboard</a></li>
      <li><a href="/library"><span class="rail__index">02</span> Library</a></li>
      <!-- 03 Policies, 04 Queue, 05 History, 06 Settings -->
    </ol>
  </nav>
  <main id="main" class="page">...</main>
</body>
```

The page also declares a favicon (`internal/web/static/img/favicon.svg`).

**Top bar.** 56px tall, `--surface` fill, 2px `--rule` bottom border.

- **Wordmark:** `JELLYTRIM` in Plex Mono 500, uppercase, 0.12em tracking, preceded by a small 10px `--accent` square with an ink border (the "power" lamp).
- **Dry Run lamp:** when Dry Run is on, a lit `--accent` lamp and the words "Dry Run". When it is off, an unlit lamp and the word "Live". It links to the Dry Run setting. It is always visible, at every width.
- **Sync status:** "Synced 12 min ago", or "Syncing" with an active lamp, or "Jellyfin unreachable" with a bad lamp. Hidden below 720px (the Dashboard shows it).

**Left rail (720px and above).** `--bg` fill, 1px `--rule` right border. Items are numbered like channels on a device: `01 Dashboard`, `02 Library`, `03 Policies`, `04 Queue`, `05 History`, `06 Settings`. The index is Plex Mono in `--ink-2`.

- Current page: `aria-current="page"`, a 4px `--accent` bar on the left edge, label weight 700.
- Hover: `--surface-sunk` fill.
- The Queue item shows a count badge when jobs are waiting or running ("Queue 3").

**Bottom bar (below 720px).** Fixed to the bottom, 2px `--rule` top border, four equal cells: Dashboard, Library, Queue, More. "More" is a `<details>` that opens upwards with Policies, History and Settings. Each cell is at least 48px tall. Page content has bottom padding so nothing hides behind the bar.

**Page.** `.page` holds a page header (title in `--text-2xl`, an optional one-line description in `--ink-2`, and page actions on the right) and then the page grid.

## Components

Each component is a templ component in `components.templ`. Class names follow `.block`, `.block__element`, `.block--variant`.

### Panel

The main container. It looks like a module in an equipment rack, with a printed label in the top-left corner.

```html
<section class="panel" aria-labelledby="panel-storage">
  <header class="panel__head">
    <h2 class="panel__tab" id="panel-storage">Storage</h2>
    <div class="panel__actions"><!-- optional buttons or links --></div>
  </header>
  <div class="panel__body">...</div>
</section>
```

- `--surface` fill, `--border`, `--radius-panel`.
- `.panel__head`: 2px `--rule` bottom border, flex row, the tab on the left and actions on the right.
- `.panel__tab`: the equipment label. `--text-label` style, `--ink` fill, `--surface` text, padding `--space-1 --space-2`, sitting flush in the top-left corner. It is a real heading (`h2` or `h3`) so the page outline works.
- `.panel__body`: padding `--space-4` (below 720px) or `--space-5`.
- Variants: `.panel--emphasis` (2px border all round, used for the Dry Run setting and the running job), `.panel--sunk` (`--surface-sunk` fill, for secondary information).

### Metric

A big number with its unit and label.

```html
<div class="metric">
  <p class="metric__label">Optimisable</p>
  <p class="metric__value">
    <span class="metric__approx" aria-hidden="true">~</span><span class="visually-hidden">About </span>1.5<span class="metric__unit">TB</span>
  </p>
  <p class="metric__note">Estimate. If every current plan ran.</p>
</div>
```

- Label on top in `--text-label` style.
- Value in `--text-metric` (drops to `--text-3xl` below 720px), weight 700, tabular figures, tight tracking.
- Unit in Plex Mono 500 at `--text-lg`, aligned to the baseline, `--ink-2`.
- Note in `--text-sm`, `--ink-2`.
- An estimate always shows `~` before the number and the word "Estimate" in the note. The `~` is hidden from screen readers and replaced by "About".
- States: normal; loading (value shows `--` in `--ink-2`); unknown (value `--`, note says why, for example "Sync not run yet").

### Stat row

A definition list of label and value pairs, like a spec sheet.

```html
<dl class="stats">
  <div class="stats__row"><dt>Codec</dt><dd class="mono">H.264 (High)</dd></div>
  <div class="stats__row"><dt>Resolution</dt><dd class="mono">3840 × 1600</dd></div>
</dl>
```

- Two columns: `dt` in `--text-label` style and `--ink-2`, `dd` in body text. Technical values add `.mono`.
- `--border-soft` between rows. Row padding `--space-2` top and bottom.
- Below 720px the label sits above the value.
- An unknown value shows "Unknown" in `--ink-2`, never a blank.

### Status lamp

A small square LED. It always sits next to a word that says the same thing.

```html
<span class="status">
  <span class="lamp lamp--ok" aria-hidden="true"></span>
  <span class="status__text">Complete</span>
</span>
```

- 10px square (12px in the top bar), `--radius`, 1px `--rule` border.
- `lamp--ok`: `--ok` fill. `lamp--warn`: `--warn` fill. `lamp--bad`: `--bad` fill.
- `lamp--idle`: unlit, a solid `--rule-soft` square with a `--rule-soft` edge, like a dark LED lens. It is never hollow, so it cannot be mistaken for an unticked checkbox.
- `lamp--active`: `--accent` fill, pulsing between full and 40% opacity every 1.2s (`--dur-slow` easing). Steady under reduced motion.
- `lamp--active-steady`: `--accent`, not pulsing. Used for the Dry Run lamp.
- The lamp is decorative (`aria-hidden`). The word carries the meaning.

### Badge

A bordered chip for technical facts: codec, resolution, HDR type, container.

```html
<span class="badge">HEVC</span>
<span class="badge">2160p</span>
<span class="badge badge--hdr">HDR10</span>
```

- Plex Mono 500, `--text-label` size, uppercase, 1px `--rule` border, `--radius`, padding 2px 6px, `--surface` fill.
- `badge--hdr`: `--accent-ink` text and border.
- `badge--muted`: `--rule-soft` border, `--ink-2` text (for facts that do not affect the decision).
- `badge--count`: `--ink` fill, `--surface` text (for counts on nav items).
- Badges are not interactive. Filters use the segmented control or checkbox chips instead.

### Toggle switch

A chunky hardware switch with a real checkbox underneath.

```html
<label class="toggle">
  <input class="toggle__input" type="checkbox" role="switch" name="enabled" checked>
  <span class="toggle__track" aria-hidden="true"><span class="toggle__knob"></span></span>
  <span class="toggle__label">Enabled</span>
</label>
```

- The input is visually hidden but stays in the tab order and receives focus. The focus ring draws around the track.
- Track 44 × 24px, 2px `--rule` border, `--surface-sunk` fill when off, `--accent` fill when on.
- Knob 16 × 16px square, `--ink` fill. It moves across in `--dur` and settles with 1px of extra travel, so the switch "clicks".
- The track shows `ON` or `OFF` in 9px Plex Mono beside the knob, so the state is readable without colour.
- Disabled: `--rule-soft` border, `--ink-2` label, with a visible reason next to it.
- `role="switch"` is the one ARIA addition: it tells assistive technology the checkbox is an on/off switch.

### Segmented control

A radio group styled as a row of hardware buttons. Used for Quality, Resolution, Codec and similar single choices.

```html
<fieldset class="segmented">
  <legend class="segmented__legend">Quality</legend>
  <div class="segmented__options">
    <input type="radio" id="q-max" name="quality" value="maximum">
    <label for="q-max">Maximum</label>
    <input type="radio" id="q-high" name="quality" value="high" checked>
    <label for="q-high">High</label>
    <!-- Balanced, Space Saver -->
  </div>
  <p class="segmented__hint" id="quality-hint">Hard to tell apart from the original on a large screen.</p>
</fieldset>
```

- Native radios, visually hidden, so arrow keys move between options.
- Options share one 2px `--rule` outline, separated by 1px rules, `--radius` on the outer corners only.
- Unchecked: `--surface`. Hover: `--surface-sunk`. Checked: `--accent` fill, `--on-accent` text, weight 500, and the button sits 1px lower as if pressed in.
- Focus ring on the focused option's label.
- A disabled option (for example AV1 before it ships) shows `--ink-2` text and "(planned)".
- The hint below changes with the selection to describe the chosen option in plain words.

### Button

```html
<button class="btn btn--primary" type="submit">Save policy</button>
<button class="btn btn--secondary" type="button">Test connection</button>
<button class="btn btn--danger" type="button">Cancel job</button>
```

- Height 40px (48px below 720px), padding 0 `--space-4`, Plex Sans 500, `--radius`.
- **Primary:** `--accent` fill, `--on-accent` text, `--border-strong`. Hover: `--accent-hover`. Use one primary per view.
- **Secondary:** `--surface` fill, `--ink` text, `--border`. Hover: `--surface-sunk`.
- **Danger:** `--surface` fill, `--bad` text, 2px `--bad` border. Hover: `--bad-tint` fill. Used for Cancel, Delete, and turning off Dry Run.
- **Pressed** (`:active`): moves down 1px (`translate: 0 1px`). No movement under reduced motion.
- **Busy** (while an HTMX request runs, `.htmx-request`): the label stays, a small active lamp appears before it, and `aria-busy="true"` is set. The button is not disabled, so focus is not lost; repeat clicks are ignored with `hx-disabled-elt` on the form.
- **Disabled:** `--surface-sunk` fill, `--ink-2` text, `--rule-soft` border. A disabled button cannot take focus, so the reason is always visible text beside it, linked with `aria-describedby`. Never rely on a tooltip.
- Links that look like buttons use the same classes on `<a>`. Links in text are `--accent-ink`, underlined.

### Table

Dense, aligned, and readable at a glance.

```html
<table class="table">
  <caption class="visually-hidden">Waiting jobs</caption>
  <thead><tr><th scope="col">Title</th><th scope="col">Change</th><th scope="col" class="num">Est. saving</th></tr></thead>
  <tbody>
    <tr>
      <td data-label="Title">Example Film (2019)</td>
      <td data-label="Change" class="mono">2160p H.264 → 1080p HEVC</td>
      <td data-label="Est. saving" class="num mono">~ 31.2 GB</td>
    </tr>
  </tbody>
</table>
```

- Header: `--text-label` style, `--ink-2`, 2px `--rule` bottom border.
- Rows: 40px minimum, `--border-soft` between rows, `--surface` fill. Hover: `--surface-sunk`.
- Technical columns use `.mono`. Numeric columns use `.num` (right-aligned, tabular figures).
- A row that links somewhere has one real link in its first cell. The whole row is not clickable.
- Wide tables scroll inside their panel, never the page.
- **Below 720px** each row becomes a card: cells stack, and each cell shows its `data-label` as a small label. Changing `display` removes table semantics in some browsers, so collapsing tables add explicit `role` attributes (`table`, `rowgroup`, `row`, `columnheader`, `cell`). This is one of the few places ARIA is needed.

### Progress meter

Progress is shown as a segmented bar, like a VU meter.

```html
<div class="meter">
  <progress class="meter__bar" max="100" value="38" aria-describedby="job-42-progress">38%</progress>
  <p class="meter__readout mono" id="job-42-progress">38% · 2.4× · 12 min left</p>
</div>
```

- A native `<progress>` element, styled into 20 segments with 2px gaps (each segment is 5%).
- Filled segments are `--accent`, empty ones `--surface-sunk`, with a 1px `--rule` border round the whole bar. Height 16px.
- The segment in progress fills in steps of `--dur-slow`, so the meter moves in visible ticks rather than a smooth slide.
- Indeterminate (no `value`): segments light one after another left to right. Under reduced motion, it shows a static half-lit bar and the word "Working".
- The readout next to it gives the number in text. The meter never stands alone.

### Explanation list

Why a policy matched or an item was skipped. Each line has a symbol, a colour and words.

```html
<ul class="explain">
  <li class="explain__item explain__item--pass">
    <span class="explain__mark" aria-hidden="true">✓</span>
    <span class="visually-hidden">Met: </span>watched
  </li>
  <li class="explain__item explain__item--fail">
    <span class="explain__mark" aria-hidden="true">✗</span>
    <span class="visually-hidden">Not met: </span>is a favourite
  </li>
</ul>
```

- Plex Mono for the mark, body text for the reason. `--ok` for pass and `--bad` for fail.
- Reasons are short facts with the actual value: "last watched 143 days ago", "2160p is above 1080p", "bitrate unknown, so this condition does not match".
- A neutral line (for information only) uses `·` and `--ink-2`.

### Empty state

```html
<div class="empty">
  <p class="empty__count">0</p>
  <p class="empty__title">No jobs in the queue</p>
  <p class="empty__text">Jobs appear here when Dry Run is off and an enabled policy matches an item.</p>
  <a class="btn btn--secondary" href="/policies">Review policies</a>
</div>
```

- A large `0` in `--text-3xl` and Plex Mono, like a counter at rest.
- One heading, one sentence that says why it is empty, and at most one action.
- Inside a panel, left-aligned. It is the only place a single short line may be centred, on phones.

### Callout and error

A callout reports a problem, a warning or useful information. Errors say what happened and what to do, and keep the technical detail one click away.

```html
<div class="callout callout--bad" role="alert">
  <p class="callout__title"><span class="lamp lamp--bad" aria-hidden="true"></span> Encoding failed at 38%</p>
  <p class="callout__text">Intel QSV returned an encoder error. The original file is unchanged. Try again with software encoding, or check the hardware test in Settings.</p>
  <div class="callout__actions"><button class="btn btn--secondary">Retry with software</button></div>
  <details class="callout__details">
    <summary>Technical details</summary>
    <pre class="mono">...stderr tail...</pre>
  </details>
</div>
```

- `--border` on three sides and a 4px status-coloured bar on the left. Fill is the status tint.
- Variants: `callout--bad`, `callout--warn`, `callout--info`, `callout--ok`.
- `role="alert"` only for an error that appears after the user acted. Callouts present on page load have no role.
- The `<details>` block is closed by default. Its `<pre>` wraps long lines and has a Copy button.
- Callouts never contain the Jellyfin API key or any header value.

### Form field

```html
<div class="field">
  <label class="field__label" for="jf-url">Jellyfin address</label>
  <p class="field__hint" id="jf-url-hint">The address JellyTrim uses to reach Jellyfin, for example http://jellyfin:8096.</p>
  <input class="field__input mono" id="jf-url" name="url" type="url" aria-describedby="jf-url-hint" placeholder="http://jellyfin:8096">
  <p class="field__error" id="jf-url-error" hidden><span aria-hidden="true">✗</span> Enter an address starting with http:// or https://.</p>
</div>
```

- Label above the control, in body text weight 500 (not the uppercase label style, which is for read-only data).
- Input: 40px tall (48px below 720px), `--surface` fill, `--border`, `--radius`. Paths, URLs and keys use Plex Mono.
- Focus: the standard focus ring.
- Invalid: `aria-invalid="true"`, 2px `--bad` border, the error shown below with a `✗` and added to `aria-describedby`. Validation runs on submit and on blur, not on every key press.
- Inline sentence slots (in the policy editor) are a variant: `.field--slot`, an inline control with a 2px bottom border only.

### Dialog

A native `<dialog>` for confirmations that matter: turning off Dry Run, cancelling a running job, deleting a policy, restoring a backup.

- `--surface` fill, `--border-strong`, `--radius-panel`, a panel tab label at the top.
- Focus moves to the safe choice (Cancel) when it opens and returns to the trigger when it closes. Escape closes it.
- The confirming button names the action ("Turn off Dry Run"), never "OK" or "Yes".

### Schedule grid

A week of hour cells for the processing schedule (`ScheduleGrid` in `components.templ`). Each cell is a real checkbox.

```html
<fieldset class="schedule" data-schedule aria-describedby="h-schedule-hint">
  <legend class="schedule__legend">Active hours</legend>
  <p class="schedule__hint" id="h-schedule-hint">Tick the hours when encoding may run.</p>
  <div class="schedule__frame">
    <div class="schedule__grid">
      <span class="schedule__hour" style="--d: -1; --h: 1;" aria-hidden="true">01</span>
      <span class="schedule__day" style="--d: 0; --h: -1;"><span aria-hidden="true">Mon</span><span class="visually-hidden">Monday</span></span>
      <label class="schedule__cell" style="--d: 0; --h: 1;">
        <input class="schedule__input" type="checkbox" name="h" value="0-1" checked>
        <span class="schedule__box" aria-hidden="true"></span>
        <span class="visually-hidden">Monday 01:00 to 02:00</span>
      </label>
      <!-- 168 cells -->
    </div>
  </div>
</fieldset>
```

- Each ticked cell posts `h=<day>-<hour>`, day 0 being Monday. The server rejects any other value.
- Off: `--surface-sunk` fill with a `--rule-soft` edge, like an unlit lamp. On: `--accent` fill with an `--ink` edge, so on and off differ in edge as well as fill.
- Hour labels are Plex Mono; 00, 06, 12 and 18 are printed in `--ink`, the rest in `--ink-2`, like the long marks on a ruler. The current day and hour labels are inverted (ink fill), and the current cell carries a small ink square.
- **Layout.** A container query on `.schedule__frame` picks the orientation. From 46rem wide, days run down and hours across, with square cells. Narrower (phones, tablets, and the Settings column below about 1300px), the grid turns on its side: hours run down and days across, each cell at least 44px tall, and the day row sticks under the top bar while the page scrolls. The page never scrolls sideways. Every element carries `--d` and `--h` as inline custom properties, and CSS places it from them, so one list of cells serves both layouts.
- **Focus.** The accent ring would disappear against lit neighbours, so the grid draws a 2px `--ink` ring and raises the focused cell above the others.
- **JavaScript** (`app.js`, progressive): pressing on a cell flips it, and dragging with the mouse button held paints every cell it crosses to the same value. Touch is left alone so a finger can scroll; a tap toggles one cell. The grid is one tab stop: the arrow keys move between hours following the drawn orientation, and Home and End go to the ends of a day. Space toggles. Without JavaScript every cell is an ordinary tabbable checkbox, and the drag and arrow-key hint is hidden.

### Visually hidden

`.visually-hidden` hides text visually but keeps it for screen readers. Use it for extra context, such as "About" before an estimate, or "Met:" before an explanation line.

## Motion and playful details

These are the only playful touches. Each one also carries information.

- **The Dry Run lamp.** A lit orange lamp in the top bar while Dry Run is on. It goes dark when JellyTrim is live.
- **Segmented progress.** Meters fill segment by segment, like an audio level meter.
- **The toggle click.** Switches travel a little past the end and settle back 1px.
- **Pressed buttons.** Buttons and segmented options move down 1px when pressed.
- **Tabular numbers.** Metrics and counters update in place without the digits shifting.
- **The running lamp.** An active job's lamp pulses slowly. Nothing else pulses.

Everything else is still. Under `prefers-reduced-motion: reduce`, all of the above keep their state but lose their movement.

## Pages

Every page has a unique `<title>` ("Library · JellyTrim"), one `h1`, and works as a full page load as well as through HTMX. Filters and searches are GET forms, so the URL reflects the view and the back button works.

### Setup wizard

Route: `/setup`. Shown on first run, and again if the Jellyfin connection is removed. It uses a reduced shell: the top bar only, no rail.

- A step strip at the top: `<ol class="steps">` with eight numbered cells (`01` to `08`) in Plex Mono. The current step has an `--accent` fill and `aria-current="step"`. Done steps show `✓`. Step names show beside the numbers at 1080px and above.
- One step per page. Each step saves when the user continues, so a refresh or a return visit resumes where they left off.
- Back (secondary) on the left, Continue (primary) on the right. Continue stays enabled; if something is missing, the page says what.

**1. Welcome.** What JellyTrim does, in three short points. A callout: "Dry Run is on. JellyTrim will not change any files until you turn it off." Button: "Start setup".

**2. Connect Jellyfin.**

- Fields: Jellyfin address (placeholder `http://jellyfin:8096`) and API key (`type="password"`, `autocomplete="off"`).
- A hint says how to create a key: "In Jellyfin, open Dashboard, then API Keys, and add a key named JellyTrim."
- "Test connection" runs as soon as both fields are filled (HTMX, on change) and on Continue. The result appears below with a lamp:
  - ok: "Connected to Jellyfin 12.0.1 on your-server-name."
  - bad: "JellyTrim could not reach http://jellyfin:8096. Check the address, and that both containers are on the same Docker network."
  - bad: "Jellyfin rejected this API key. Create a new key and paste it here."
  - warn: "Jellyfin is starting up. Trying again in 5 seconds."
- If the key or URL came from environment variables, the field is read-only and says "Set by JELLYTRIM_JELLYFIN_API_KEY".
- The key is never sent back to the browser, even after a failed test.

**3. Libraries and watched state.**

- A checklist of Jellyfin libraries: name, type (Movies, Shows), item count and folder locations (Plex Mono, `--ink-2`). Movie and show libraries are ticked by default. Other types are listed but cannot be selected, with the reason ("Music libraries are not supported").
- "Whose watched state counts?" A segmented control: "One user" or "Several users".
  - One user: a select of enabled Jellyfin users.
  - Several users: a checklist of users, then a segmented control "Any of them" or "All of them".
- A sentence under the choice restates it: "An item counts as watched when any of Alice, Sam has watched it."

**4. Path mappings.**

- One row per library location. The Jellyfin path is pre-filled from the library's locations and read-only (Plex Mono). The JellyTrim path is an input, pre-filled with the same path.
- The check runs as the user types (HTMX, 500ms after the last key) and shows a lamp and a sentence per row:
  - ok: "Found 1,204 of 1,204 files. Can write, hard-link and rename."
  - warn: "Found 1,180 of 1,204 files. 24 are missing. Show missing files."
  - bad: "Found 0 of 1,204 files. Is the media mounted at this path inside the JellyTrim container?"
  - bad: "Found all files, but cannot write here: permission denied. Check the user setting in your compose file."
  - warn: "Can write, but hard links are not supported here. Backups will use rename instead."
- "Add mapping" adds a free row for anything the defaults miss.
- The user can continue with warnings. Items under a bad mapping are skipped with the reason.

**5. Hardware.**

- A short intro: "JellyTrim tests each encoder with a short encode. It does not just trust what ffmpeg lists."
- The ffmpeg version, then a table: Encoder (Plex Mono), Codec, Device, Result (lamp and word: Works, Not available, Failed), Notes ("HDR metadata kept", "No /dev/dri device in the container").
- The test runs when the step opens, with an indeterminate meter per row. "Test again" re-runs it, posting to `/setup/hardware`.
- Software encoding (x265) always works, so this step never blocks setup.

**6. Default strategy.**

- Quality: segmented control (Maximum, High, Balanced, Space Saver). Default High. The hint describes the choice in viewing terms, never in encoder numbers.
- Codec: segmented control (HEVC, H.264, AV1 marked "(planned)" and disabled). Default HEVC.
- Encoder: a second segmented control, "Auto" (default) or "Software only". This is a coarser choice than a specific backend: it says whether JellyTrim may use a working hardware encoder at all, not which one.
- A note: "Policies can override these. Encoder settings are under Advanced in Settings."
- A second note: "Encoding runs at any time by default. You can limit it to certain hours in Settings." The schedule grid itself stays out of setup to keep it short.

**7. Starter policies.**

- Four cards, each with the policy sentence, an enable toggle and an Edit link:
  - **Protect favourites.** Enabled.
  - **Efficient encoding.** Disabled.
  - **Archive watched 4K.** Disabled.
  - **Space-saving television.** Disabled. Includes a library picker, since it needs to know which shows.
- A note: "Protect favourites is on. The others are created switched off. Turn them on after you have checked the Dry Run."
- Unticking a card does not omit the policy: it is still created, switched off, so it can be turned on later without going back to setup.

**8. Dry Run scan.**

- Button: "Start Dry Run scan".
- A segmented meter with the phase written beside it: Syncing with Jellyfin, Inspecting files, Evaluating policies. Counters tick up in tabular figures ("Inspected 612 of 1,204").
- When it finishes, the Dry Run summary (as on the Dashboard) and a primary button: "Go to Dashboard".
- The scan can be left running. Leaving the page does not stop it.

### Dashboard

Route: `/`. It answers five questions in this order: how much storage am I using, how much could JellyTrim save, what is being processed, what changed recently, and is anything wrong.

Layout at 1080px and above (12 columns):

| Row | Left | Right |
|---|---|---|
| 1 | Four metrics, 3 columns each | |
| 2 | Dry Run summary (7) | Now processing (5) |
| 3 | Recent activity (7) | Problems (5) |

Below 720px the metrics form a 2 × 2 grid and the panels stack: Problems (if any), Now processing, Dry Run summary, Recent activity.

**Metrics.** Each is a `.metric` inside one shared panel with 1px rules between them.

| Label | Value | Note |
|---|---|---|
| MEDIA STORAGE | Total size of managed items, e.g. `4.8 TB` | "1,432 items in 3 libraries" |
| JELLYTRIM SAVED | Space saved by completed jobs, e.g. `612 GB` | "From 57 files". Restored files are not counted |
| OPTIMISABLE | `~ 1.5 TB` | "Estimate. If every current plan ran" |
| QUEUED | Jobs waiting or running, e.g. `3` | "~ 94 GB to process", or "Paused" |

**Dry Run summary.** Panel tab: "Dry Run" when Dry Run is on, "Plan" when it is off. The body reads like a printout, in Plex Mono:

```
238 items evaluated

 91  already optimal
 87  would be converted H.264 → HEVC
 42  would be downscaled 4K → 1080p
  7  skipped (HDR or Dolby Vision)
 11  protected or excluded by policy

Current size          4.8 TB
Estimated after     ~ 3.3 TB
Estimated saving    ~ 1.5 TB
```

- Counts are right-aligned in a fixed column. Each line links to the Library with the matching filter.
- Estimated values carry `~` and the word "Estimated". A line under the block says: "Estimates are based on each file's bitrate and the chosen quality. Real results vary."
- It shows when the evaluation last ran, and a "Run again" button.

**Now processing.** The running job in compact form: title, `2160p H.264 → 1080p HEVC`, the segmented meter, the readout (percentage, speed, time left) and the encoder. Below it, "3 more waiting". In Dry Run: "Dry Run is on. Nothing will be processed." with a link to Settings. When the queue is empty: an empty state.

**Recent activity.** The last 10 finished jobs, newest first. Each entry:

```
Blade Runner 2049                                   2 h ago
2160p H.264 → 1080p HEVC
48.2 GB → 13.7 GB                        [ok] Saved 34.5 GB
```

The title in Plex Sans 500, the rest in Plex Mono. Failed and skipped entries show their lamp and the plain reason ("Skipped: the new file would have saved only 4%"). A link at the end: "All history".

**Problems.** A list of anything that needs attention, most serious first, each with a lamp, a sentence and an action:

- "Jellyfin unreachable since 14:02. Check that Jellyfin is running." [Settings]
- "24 files not found under /mnt/media/movies. Check the path mapping." [Path mappings]
- "2 jobs failed today." [History]
- "Intel QSV stopped working at the last test. JellyTrim is using software encoding." [Hardware]
- "Less than 50 GB free on /mnt/media. Encoding is paused until more space is free."
- "No processing hours are switched on, so nothing will be encoded." [Choose hours in Settings], when the processing schedule has no active hours.

With nothing to report: an ok lamp and "No problems."

### Library

Route: `/library`. Every managed item and what JellyTrim would do with it.

**Toolbar** (a GET form; results update through HTMX and the URL updates with `hx-push-url`):

- Search box (title, series or file name), 300ms after the last key.
- Library select ("All libraries" by default).
- Filter groups, as checkbox chips styled like the segmented control:
  - Decision: Needs optimisation, Already optimal, Protected, Skipped
  - Resolution: 4K, 1080p, 720p
  - Codec: H.264, HEVC, AV1
  - HDR
  - Watched: Watched, Unwatched
- Chips in the same group widen the results (or); different groups narrow them (and).
- A result count in tabular figures: "Showing 1 to 100 of 1,432". "Clear filters" when any are set.
- Below 720px the filters collapse into a `<details>` labelled "Filters (2)".

**Results table:**

| Column | Content |
|---|---|
| (artwork) | Poster at 40 × 60px from `/img/{id}`, `loading="lazy"`, `alt=""` (the title is next to it). No artwork: a `--surface-sunk` box with the first letter in Plex Mono |
| Title | Link to the item. Films: "Title (Year)". Episodes: "Series · S02E05 · Title" |
| Library | Library name |
| Format | Badges: resolution, codec, HDR type |
| Size | `48.2 GB` (Plex Mono, right-aligned) |
| Decision | Lamp and word: "Would convert", "Optimal", "Protected", "Skipped" |
| Est. saving | `~ 34.5 GB`. Without an estimate: "Unknown" for an item JellyTrim plans to optimise, "Not planned" for any other, in `--ink-2` |

- Sorted by title by default; Size and Est. saving headers sort (links, with `aria-sort` on the active header).
- 100 rows per page with Previous and Next.
- Empty result: "No items match these filters." with "Clear filters".
- Before the first sync: an empty state that links to the sync button.

### Item detail

Route: `/library/{id}`. Everything JellyTrim knows about one item, and exactly why it would or would not change it.

Header: poster (120px wide), title, year or episode code, library, and badges for the current format. Actions on the right: "Optimise now" (primary) and "Inspect again" (secondary).

Sections, as panels in this order (two columns at 1080px and above: Jellyfin and Source on the left, Policy, Proposed and Estimate on the right):

- **Jellyfin.** A stat row: type, library, series and season, date added, watched (per counted user: "Alice: watched 12 Mar 2026. Sam: not watched"), last watched, play count, favourite, tags, collections, Jellyfin path (Plex Mono).
- **Source.** A stat row: local path, container, size, duration, video codec and profile, resolution, bit depth, frame rate, video bitrate (with its source: "from the stream", "estimated from file size"), HDR type. Then a **streams table** for audio and subtitles:

  | # | Type | Language | Codec | Channels | Title | Flags |
  |---|---|---|---|---|---|---|
  | 1 | Audio | eng | truehd | 7.1 | Dolby TrueHD | Default |
  | 5 | Subtitle | eng | subrip | | Forced | Forced |

  Flags are words (Default, Forced, Hearing impaired, Commentary), not icons. An empty Title or Flags cell shows "None" in `--ink-2`, and an unknown codec, channel layout or language "Unknown". Attachments (fonts) and cover art are listed below the table as a count.

- **Policy.** The decision as a heading: "Matches Archive watched 4K". Then the explanation list. Then "Also matched: Efficient encoding (lower in the list, so it does not apply)." For a skip, the heading is "Skipped" and the list gives each reason with `✗`, for example "Dolby Vision profile 5 cannot be converted without losing its colour information."
- **Proposed.** A stat row: resolution ("1080p, from 2160p"), codec (HEVC), quality (High), encoder ("Auto: Intel QSV"), audio ("Keep all 3 tracks"), subtitles ("Keep all 5"), HDR ("Keep HDR10 and its metadata").
- **Estimated size.** A range in large type: "~ 11 to 16 GB", and "saves ~ 32 to 37 GB". A horizontal bar compares the current size (solid `--ink`) with the estimated range (hatched `--accent`). The panel says what the estimate is based on.

**Optimise now:**

- In Dry Run, the button is disabled and a sentence beside it says why: "Dry Run is on, so JellyTrim will not change files. Turn off Dry Run in Settings to optimise this item."
- If the item is skipped or protected, the button is absent and the Policy section explains why.
- Otherwise it first opens a dialog: "Optimise <title> now? JellyTrim will replace the file after checking the result. The original is kept as a backup for 7 days." (with 0 backup days: "until Jellyfin has picked up the change"). Buttons: "Not now" (focused) and "Optimise now" (primary). Without JavaScript the button is a link that shows the dialog through `:target`. Confirming adds the item to the front of the queue, ahead of jobs the scheduler queued automatically, and shows "Added to the queue" with a link.
- If the user restored this item's original from History, the page shows "Left alone at your request" instead of the usual actions, with a secondary "Allow changes again" button that lets JellyTrim consider the item once more.

Below the panels, a short history of jobs for this item, if any.

### Policies

Route: `/policies`. The ordered list of policies, and the editor.

**List:**

- A sentence at the top: "JellyTrim checks policies from top to bottom. The first enabled policy that matches an item decides what happens to it."
- An ordered list (`<ol>`). Each row:
  - position in Plex Mono (`01`)
  - name (link to the editor) and a `PROTECT` badge for protect policies, plus an `Off` badge when the policy is disabled
  - the policy sentence, clamped to two lines (`-webkit-line-clamp: 2`), with the full text on the editor page
  - enable toggle
  - Move up and Move down buttons (`aria-label="Move Archive watched 4K up"`); the first row has no Up, the last no Down
- The list does not show a per-policy match count or estimated saving, and does not warn when a Protect policy sits below a Convert policy that matches the same items (both are planned; for now, check order and match counts through the editor's preview).
- Reordering and toggling post through HTMX and swap the list. Focus stays on the button that was pressed, and a polite status message says "Archive watched 4K moved to position 2."
- "New policy" (primary) in the page header.

**Editor:** route `/policies/{id}`. The editor is built as three labelled sections, "In", "When" and "Then", rather than the one inline sentence sketched in earlier drafts of this document. Each section holds the same controls the sentence would:

> **In** [Movies ▾]. **When** [watched ▾] and [last watched ▾] [more than ▾] [90] days ago and [resolution ▾] [above ▾] [1080p ▾] [+ Add condition]. **Then** convert to [1080p ▾] [HEVC ▾] at [High ▾] quality. Keep all audio and subtitles.

- Each bracket is an inline slot (`.field--slot`): a native `<select>` or `<input>` with a visually hidden label ("Scope", "Condition 2", "Days"), so each section also reads correctly with a screen reader.
- Conditions join with "and". Each condition has a remove button (`✗`, `aria-label="Remove condition: last watched"`).
- "+ Add condition" adds a new condition slot and moves focus to it.
- Watched conditions follow the watched-state setting ("watched by any of Alice, Sam").
- The action is a segmented control: "Convert" or "Protect (never change)". Protect replaces the "convert to" part with "never change these items."
- "More options" (`<details>`): one combined HDR checkbox, "Allow HDR10+ and Dolby Vision to be reduced to HDR10", off by default, with one sentence on what is lost. There is no separate opt-in per HDR type.
- Name field above the sections. Enable toggle, Save (primary) and Delete (danger, with a dialog titled with the policy's name: `Delete "Protect favourites"?`) below them. The list's Delete buttons use the same dialog.

**Live preview** (a panel beside the editor at 1080px and above, below it on smaller screens). It updates 300ms after any change, through HTMX:

- "Matches 42 items." Then "36 would be converted, 6 would be skipped (HDR or Dolby Vision)."
- "6 of these are already decided by a higher policy." with the policy names.
- "Estimated saving ~ 1.1 TB." with the Estimate label.
- The first 10 matching items with their change, linking to each item.
- The preview is a simulation. It never changes anything.

**Protect, explained.** A callout on the editor when Protect is chosen: "Protect means JellyTrim never changes matching items. Because the first matching policy wins, put Protect policies at the top of the list." The list itself does not yet warn when a Protect policy sits below a Convert policy that matches the same items; check the order by eye, or with each policy's preview.

### Queue

Route: `/queue`. What is running, what is waiting, and control over both.

**Header.** A status line with a lamp: "Running. 1 job at a time." or "Paused." or "Outside the processing schedule. Encoding starts again Tuesday 01:00." or "Dry Run is on. Nothing is processed." A Pause queue / Resume queue button (one button whose label changes). Pausing lets the running job finish; the button says so.

**Processing schedule line.** When the schedule is not "any time", a strip attached under the status line says "Processing schedule: nights (01:00 to 07:00). Next active: Tuesday 01:00." (or "Active now, until Tuesday 07:00."), with a "Change the schedule" link to `/settings#schedule`. It refreshes with the rest of the polled fragment.

**Running job** (`.panel--emphasis`):

- Title and a link to the item.
- The change in Plex Mono: `2160p H.264 → 1080p HEVC · High`.
- A stage strip: Analysing › Encoding › Validating › Replacing, with the current stage lit and done stages ticked.
- The segmented meter and the readout: `38% · 2.4× · 12 min left`. Elapsed time. Projected size: "~ 13.9 GB (saving ~ 34 GB)".
- The encoder, named in plain words ("Intel QSV", not the ffmpeg encoder name), and the policy that chose the job.
- "Cancel" (danger), with a dialog: "Cancel this job? The original file is kept. The partial file is deleted."
- The panel refreshes every 2 seconds through HTMX polling (`hx-trigger="every 2s"`), except while a confirmation dialog is open inside it. When nothing is running or waiting, the server leaves out the polling attributes on the next swap, so the browser simply stops asking.
- A polite live region announces stage changes and completion only, never every percentage.

**Waiting.** A table: position, title, change, estimated saving, policy, and a Cancel button per row. A job that was stopped and queued again shows why under its title in `--ink-2`: "Stopped because the processing schedule ended. It will start again in the next active hour; the original is unchanged." or "Interrupted by a restart; it will run again." Empty: "Nothing waiting." There is no "Cancel all waiting" action; cancel jobs one at a time.

**Failed in the last hour.** A panel below the waiting table lists jobs that failed in the last hour, each with a "Retry" button and a link to History. It is not shown when nothing has failed recently.

**Statuses.** Every job status has a lamp and a word:

| Status | Lamp | Meaning |
|---|---|---|
| Waiting | idle | In the queue |
| Analysing | active | Checking the file and planning the encode |
| Encoding | active | Writing the new file next to the original |
| Validating | active | Checking the new file against the plan |
| Replacing | active | Swapping the new file in and keeping a backup |
| Complete | ok | Replaced; the saving is recorded |
| Skipped | warn | Stopped safely without changes; the reason is recorded |
| Failed | bad | Something went wrong; the original is unchanged |
| Cancelled | idle | The user cancelled it before it finished; the original is unchanged |
| Needs attention | warn | Stopped in a state that needs a person to look at it; not active, but it blocks new jobs for its item and file until resolved |

"Optimise now" on an item's page, and any other manually triggered job, is placed at the front of the waiting jobs, ahead of jobs the scheduler queued automatically.

### History

Route: `/history`. Every finished job.

- A "Needs attention" panel at the top lists jobs that stopped in a state a person needs to look at (status Needs attention), when there are any. It says: "These jobs stopped in a state you need to look at. The original is safe; each job says where."
- Filters: status (All, Complete, Skipped, Failed, Cancelled, Needs attention), library, and search. GET form, as on the Library page.
- A table: finished (date and time), title, change (Plex Mono), size before → after, saving, status (lamp and word), reason (for skipped and failed: one plain sentence).
- Each row links to a job detail page, which shows:
  - **Checks and warnings.** The validation checks (pass or fail, as an explanation list), and any warnings such as "Could not set the file's group to match the original."
  - **Technical details:** the exact ffmpeg command (Plex Mono, wrapped, with a Copy button), the last lines of ffmpeg's error output, and the encoder.
  - **Skipped jobs** that set an item aside show where: "Kept at `<path>` until `<date>`."
  - **Restore original** (danger button style, since it deletes the converted file) while the backup exists: "Backup kept until 4 Oct 2026." A dialog confirms: "Restore the original file? The converted file is deleted and Jellyfin is asked to rescan." After a restore the row status reads "Restored".
  - **Retry**, for a failed, skipped or cancelled job. It is the primary button for a failed job and a secondary one otherwise.
- The command and error output never include the Jellyfin API key.

### Settings

Route: `/settings`. One page with sections. At 1080px and above an in-page contents list sits on the left and stays in view; each section has an anchor (`#jellyfin`, `#dry-run`).

Each section is its own form and saves on its own. After saving, the section shows an ok lamp and "Saved at 14:02". Unsaved changes show "Not saved yet" in `--warn` beside the Save button.

Saving **Libraries**, **Watched state** or **Path mappings** starts a full sync, because it changes which items or files JellyTrim reads. Saving **Dry Run**, **Saving and backups** or **Advanced** only re-evaluates the existing items against the policies; it does not sync with Jellyfin again.

- **Jellyfin.** Address, and the API key. The key field is always empty. Beside it: "An API key is saved. Enter a new key only to replace it." "Test connection" as in setup. Values set by environment variables are read-only and say which variable set them.
- **Libraries.** The library checklist from setup.
- **Watched state.** Whose watched state counts, as in setup.
- **Path mappings.** The mapping rows and live check from setup.
- **Dry Run** (`.panel--emphasis`, with an accent left bar while on). A large toggle, twice the normal size, labelled "Dry Run". Text: "While Dry Run is on, JellyTrim evaluates everything and changes nothing." Turning it off opens a dialog: "Turn off Dry Run? JellyTrim will start replacing files that match enabled policies. Originals are kept as backups for 7 days." Buttons: Cancel (focused) and "Turn off Dry Run" (danger). Turning it on needs no confirmation.
- **Sync** (`#sync`). Sync with Jellyfin: every [6] hours, or daily at [time] (leave the time empty to use the interval). "Sync now" and the time of the last sync.
- **Processing schedule** (`#schedule`, posts to `/settings/schedule-hours`). Two sentences: "JellyTrim only encodes in the hours you switch on. When an hour ends, a running encode stops and starts again from the beginning in the next active hour. The original is never touched." and "Times are in Europe/London (BST) (the server's time zone; set TZ in your compose file to change it)." The zone is the `TZ` variable with the current abbreviation, or just the abbreviation ("UTC") when `TZ` is not set.
  - A stat block: **Schedule** "Nights (01:00 to 07:00). 42 hours a week." and **Now** with a lamp: "Active now, until Tuesday 07:00" (ok) or "Next active: Tuesday 01:00" (idle).
  - **Presets**, small submit buttons that replace the grid and save at once: Any time, Nights (01:00 to 07:00), Nights, and all weekend, Outside 17:00 to 23:00. The one matching the saved grid is held in (`.btn--pressed`, with ", in use" for screen readers).
  - The **schedule grid** (see Components), then "Save schedule". A hidden first submit makes Enter save rather than press a preset.
  - With no hours on, a warn callout: "No hours are active, so nothing will be encoded." The Dashboard lists the same as a problem, "No processing hours are switched on", linking here.
  - Saving clears the old daily window settings and wakes the queue, so a change applies at once: a running encode stops if its hour is now off.
- **Concurrency.** Jobs at a time, 1 to 4. Hint: "More than one job at a time is only faster with a hardware encoder."
- **Saving and backups.** Minimum saving (replace a file only if it saves at least [10] %) and how many days to keep replaced originals as backups ([7] by default), in one section.
- **Hardware.** The capabilities table from setup step 5, with the device name and ffmpeg version, the time of the last test, and "Test again" (runs with a meter per row).
- **HDR.** Read-only summary of how JellyTrim treats each HDR type, linking to the policy option "Allow HDR10+ and Dolby Vision to be reduced to HDR10", which is set per policy (off by default).
- **Processing.** "Queue matching items automatically" toggle (on by default; only applies when Dry Run is off). Validation: "Full decode check" (default) or "Sampled decode check (faster)".
- **Advanced** (a closed `<details>`). The only place encoder numbers appear:
  - per encoder, the quality value used for each tier (for example the x265 CRF for Maximum, High, Balanced, Space Saver), editable, with the default shown beside each;
  - the encoder speed preset;
  - output bit depth: 10-bit (default) or match the source;
  - "Reset to defaults".

## Accessibility

- Semantic HTML first: `header`, `nav`, `main`, `section` with headings, `table` for tables, `button` for actions, `a` for navigation, `fieldset` and `legend` for groups.
- Every page works with the keyboard alone, in a logical order. A skip link is the first focusable element.
- Every control has a visible `<label>` (or a visually hidden one in the policy sentence). Placeholders are examples, never labels.
- Text contrast is at least 4.5:1, and 3:1 for large text and control edges. The token tables above give the ratios.
- The focus ring (2px `--accent`, 2px offset) is visible on every interactive element.
- ARIA only where HTML cannot express it: `role="switch"` on toggles, `aria-current`, `aria-sort`, `aria-describedby`, roles on collapsing tables, and live regions for queue and save status.
- Status is never shown by colour alone. Lamps sit next to words; explanation lines have `✓` or `✗` and a hidden "Met" or "Not met"; toggles show ON and OFF.
- HTMX swaps keep focus where the user was, or move it to the new content's heading. Polling never moves focus.
- Touch targets are at least 44 × 44px below 720px. This includes a text link that is the only way into an item (Library titles, Dashboard activity and problem examples): below 720px it becomes a block at least 44px tall.
- Pages are usable at 200% zoom and at 320px wide without sideways scrolling of the page.

## Copy

- Plain British English. Short sentences, active voice, common words. "Optimise", "convert", "colour".
- Name things the way users do: "film" or "episode", "watched", "favourite", "4K", "1080p", "HEVC".
- Never show CRF, QP, ICQ, global quality or FFmpeg flags in the normal UI. They belong in Settings, Advanced, and in History's technical details.
- Estimates always say so: `~` before the number and "Estimate" or "Estimated" nearby.
- An error says what happened, then what to do, and keeps the technical detail in an expandable section. For example: "Encoding failed at 38%. Intel QSV returned an encoder error." with Technical details below.
- Say what did not happen when it matters: "The original file is unchanged."
- Buttons name their action: "Save policy", "Turn off Dry Run", "Restore original". Not "OK", "Submit" or "Yes".
- Sizes use decimal units with one decimal place: `48.2 GB`, `1.5 TB`. Counts use thousands separators: `1,432`.
- Dates are "4 Oct 2026". Times are 24-hour: "14:02". Recent times are relative: "12 min ago".
- Capitalise "Dry Run" as a proper name. Sentence case everywhere else, except the uppercase label style, which is applied by CSS (the source text stays in sentence case).
- No emojis, no exclamation marks, no em dashes.

## Checklist for a new page

- Uses the shell, one `h1`, a unique `<title>`.
- Uses shared components; any new shared piece is added to `components.templ` and to this document.
- Only tokens, no raw colours or sizes.
- Works at 360px, 768px and 1280px, with no page-level sideways scroll.
- Keyboard-only walkthrough done; focus always visible.
- Status shown with a word as well as colour.
- Estimates labelled. No encoder numbers outside Advanced and technical details.
- Checked with `/verify-ui` and `/design-review`.
