# RagnaAlbum — repo context (consumer-owned)

RagnaAlbum ("the fleet's Old Card Album") is a Go module providing per-entity
fact cards for prompt injection, bound offline from the pinned rAthena DB
(authoritative tier) and the iRO Wiki archive (narrative tier, provenance-tagged).

Layout:
- `album/` — the runtime package: Card/Album types, Load/Get (deterministic,
  data-only; embeds `album/album.json`), schema-version refusal, tier enum.
- `cmd/albumbuild/` — the offline binder (rathena.go, wiki.go, assemble.go):
  joins both sources, caps/selects lines deterministically, validates
  coverage, `-check` enforces byte-identical rebuilds.
- `docs/ADR-001-album-layer-contract.md` — the layer contract: boundaries,
  trust tiers, forbidden dependencies. Changes touching boundaries MUST
  update the ADR.

Invariants (test-enforced): the album package imports no source SDKs; cards
are canonically sorted; every line carries a tier; builders are
deterministic; Load refuses unknown schema versions.
