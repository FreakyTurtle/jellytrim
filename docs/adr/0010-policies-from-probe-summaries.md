# 0010. Policies are evaluated from stored probe summaries

Date: 2026-09-28
Status: Accepted

## Context
Every sync, policy change and policy-editor preview evaluates the whole library. Parsing every file's full ffprobe output each time took 15 seconds and about 4 GB of memory at 100,000 items.

## Decision
Policy conditions read `policy.Facts` (codec, width, height, video bitrate, HDR class, size), built from summary columns stored beside each probe. The full ffprobe output is parsed only for items whose winning policy would optimise them, because the plan needs every stream, and only in batches of 500. The policy preview counts matches exactly from the summaries and estimates the outcome split and savings from an evenly spaced sample of at most 1,000 items, saying so in the UI.

## Consequences
- Evaluation is roughly linear in library size and uses little memory.
- The summary columns and the functions that fill and read them (`fillSummary`, `factsFromSummary`) must stay in step with `media.Parse`. `TestSummaryGivesTheParsedFacts` checks this against every fixture, and the bulk evaluation is compared with a parse-everything reference in tests.
- A preview over more than 1,000 decided items is an estimate, labelled as one.
