# ADR-001: The RagnaAlbum layer contract

Status: Accepted · 2026-10-02

## Context

goKore's conversation grounding needs per-entity facts (prices, drops, stats,
lore) at prompt-composition time. The facts come from two sources with
different trust models: the pinned rAthena database (authoritative numbers)
and the iRO Wiki archive (narrative prose requiring hedged framing). Multiple
consumers may need the same join. The join must not become a runtime graph
database inside the fleet's hot path.

## Decision

RagnaAlbum is the fleet's Old Card Album: a **build-time join, served as an
immutable artifact**. `cmd/albumbuild` walks both sources, binds one Card per
entity (ID-keyed, kind monster/item/map/npc/place, 4–6 tier-labeled lines),
and ships the result as versioned JSON. Runtime is `album.Load(bytes)` +
`album.Get(id)` — deterministic O(1) lookup. No runtime database, file, or
network access; no traversal API (traversal happens at build time; deep
constrained queries belong to ragnaDataSDK's existing runtime surface).

## Layer boundaries (enforced, not conventional)

This module:
- **May import** (build-time only, `cmd/albumbuild`): `ragnaDataSDK`,
  `iroWikiScraper/sdk`. The `album` package imports **neither** — its only
  input is the bytes handed to `Load`.
- **Owns**: the entity card format, schema versioning, trust-tier assignment,
  line caps, deterministic selection, and prompt-shaped line rendering.
- **Must never contain**: conversation semantics (participants, purpose,
  persona, generation), consumer identifiers, model/endpoint code, runtime
  source access, randomness (the album metaphor carries no gacha semantics).

Consumers:
- **May**: embed the artifact, call `Load`/`Get`, render cards under their own
  injection policy and caps.
- **Must not**: parse the artifact JSON directly (bypassing `Load`), mutate
  artifacts, or re-join sources ad hoc.

Sources (ragnaDataSDK, iroWikiScraper):
- Never import this module. Data flows one direction:
  sources → albumbuild → artifact → consumer.

## Trust tiers

Every line carries exactly one tier; consumers must render accordingly:

| Tier | Source | Rendering contract |
|---|---|---|
| `authoritative` | rAthena DB via SDK | Quotable as fact |
| `curated` | Owner-curated SDK-side wiki derivatives | Quotable as fact |
| `narrative` | Wiki prose (page provenance attached) | Must be framed as hearsay/lore, never firsthand fact |

Tier is data, not convention. No numeric trust scores; no rarity semantics.

## Determinism & versioning

- Same pinned sources + flags → byte-identical album (`-check` mode enforces
  this against the checked-in artifact; CI runs it).
- `schema_version` refuses unknown futures. Load never guesses.
- Provenance header records source tags/paths, build date, converter id, and
  a source hash. Regeneration cadence: per rAthena bump and per monthly wiki
  release, via PR (the diff IS the review).

## Alternatives considered

- **Merge the two source repos**: rejected — different languages, release
  cadences, and trust models; merging flattens the tier boundary.
- **Runtime graph DB / traversal API**: rejected — the hot path needs
  microsecond deterministic lookups; data is static per release; pre-rendered
  lines keep every claim reviewable in a PR diff.
- **Conversation-level composition here**: rejected — which entities a
  conversation needs is game design and stays with the consumer's curator.
