# 0007. HDR10 and HLG are transcoded; dynamic HDR and Dolby Vision need opt-in or are skipped

Date: 2026-09-27
Status: Accepted

## Context
Most 4K films are HDR, and archiving watched 4K films is JellyTrim's main use case. HDR10 and HLG can be preserved through a re-encode: they need 10-bit output, the right colour tags and, for HDR10, the static mastering display and content light level metadata. HDR10+ dynamic metadata and Dolby Vision layers are much harder to carry through, and Dolby Vision profile 5 has no HDR10 fallback at all. Getting any of this wrong produces washed-out or discoloured video.

## Decision
- SDR, HDR10 and HLG are transcoded, with the colour information carried over explicitly and checked on the output.
- HDR10+ and Dolby Vision with an HDR10 base layer (profiles 7 and 8.1) are skipped by default. A per-policy option allows reducing them to HDR10.
- Dolby Vision without a usable base layer (profile 5 and others) and any unclear case are always skipped, with the facts that triggered the skip.
- An encoder is used for HDR only if its hardware probe proves it preserves HDR metadata.
- JellyTrim never tone-maps HDR to SDR.

## Consequences
- The main use case works for most real 4K libraries.
- Validation must check colour metadata, not just codec and resolution.
- Some files are skipped, always with an explanation.
