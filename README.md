# RagnaAlbum

The fleet's Old Card Album: per-entity fact **cards** bound offline from the
pinned rAthena database (authoritative numbers) and the iRO Wiki archive
(narrative lore with provenance), served as one deterministic, immutable
**album** artifact for prompt injection.

- `album` — the runtime package: `Load(bytes)`, `Get(id) → Card`. Data-only;
  no source SDK imports, no runtime DB/network, no conversation concepts.
- `cmd/albumbuild` — the binder: walks ragnaDataSDK + the iroWikiScraper/sdk
  archive, binds cards (4–6 tier-labeled lines each), validates coverage,
  and enforces two-build determinism via `-check`.

Trust tiers travel in the data: `authoritative` / `curated` / `narrative` —
consumers must render narrative as hearsay, never firsthand fact. See
[docs/ADR-001-album-layer-contract.md](docs/ADR-001-album-layer-contract.md)
for the full layer contract and enforcement.

Build:

    go run ./cmd/albumbuild \
      -rathena-db <pinned rAthena db root> \
      -rathena-tag <pin tag> \
      -wiki-db <materialized irowiki SQLite> \
      -wiki-release <e.g. 2026.08> \
      -date <YYYY-MM-DD> \
      -out album/album.json -validate-coverage
