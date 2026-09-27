---
name: design-review
description: Judge JellyTrim's UI against docs/UI.md and accessibility basics, with screenshots at three widths. Used by the ux-reviewer agent; can also be run directly.
argument-hint: "[page paths]"
---

Review the pages in `$ARGUMENTS` (default: all top-level pages) against `docs/UI.md`.

## Capture

With the Playwright MCP, screenshot each page at 390, 834 and 1440 pixels wide into the scratchpad. Also load each page with JavaScript disabled to see what still works.

## Mechanical checks

- No horizontal overflow at any width.
- Body text contrast at least 4.5:1, large text 3:1. Check the accent colour on the background.
- Tap targets at least 44x44 CSS pixels on the 390 width.
- Every form control has a visible label. Every image has alt text or `alt=""`.
- Heading levels in order. One `<h1>` per page.
- Focus visible on every interactive element.

## Design judgement

Score each from 1 to 5, with one sentence of evidence:

1. **Grid and alignment.** Do edges line up? Is spacing from the scale?
2. **Hierarchy.** Is the most important number or action obvious within two seconds?
3. **Typography.** Sans for prose, mono for technical values, uppercase compact labels, big metrics.
4. **Colour discipline.** One accent, status colours only for status, no decoration.
5. **Industrial character.** Do controls feel physical and precise? Is there personality without clutter?
6. **Clarity of copy.** Plain words, no jargon in the normal UI, errors that say what to do.
7. **Not generic.** Would anyone mistake it for a Bootstrap or Tailwind template? It should not be possible.

## Output

Findings grouped as Broken, Off-brand, Polish, each with page, width, problem and fix. Then the scores.
