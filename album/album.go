// Package album is the fleet's Old Card Album: per-entity fact cards,
// bound offline by cmd/albumbuild from the pinned rAthena database
// and the materialized iRO Wiki archive, and served here as one
// deterministic, immutable artifact.
//
// The metaphor is load-bearing in exactly one direction: an album is
// a bound collection of cards. "Get" is a DETERMINISTIC lookup — no
// randomness, no rarity, no gacha semantics. Card quality is the
// semantic tier enum on every line (TierAuthoritative / TierCurated /
// TierNarrative), never a numeric trust score.
//
// Layer contract (ADR-001, docs/ADR-001-album-layer-contract.md):
//
//   - This package is DATA-ONLY. It imports neither ragnaDataSDK nor
//     iroWikiScraper — the only source of album content is the bytes
//     handed to Load (or the embedded release artifact). There is no
//     runtime database, file, or network access.
//   - It knows nothing about consumers: no persona/conversation/
//     generation concepts, no model or endpoint code. Consumers
//     (goKore's generation-side composition) translate cards into
//     their own vocabulary.
//   - Load refuses artifacts whose schema_version it does not
//     understand — a future album ships with a future loader, never
//     a guess.
//   - Builders are deterministic: the same pinned sources and flags
//     bind a byte-identical album (enforced by cmd/albumbuild's
//     two-build check).
package album

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// SchemaVersion is the album schema this loader understands.
const SchemaVersion = 1

// Card line caps (enforced by Validate; mirrored in the builder's
// selection rules).
const (
	MaxLinesPerCard = 6
	MaxNarrative    = 2
)

// Tier is a line's semantic provenance class.
//
//   - TierAuthoritative: numbers straight from the pinned rAthena DB
//     (via ragnaDataSDK): prices, drop tables, stats, spawn maps.
//   - TierCurated: structured non-lore derivatives of the wiki (map
//     display names, a place's related maps) — factual, but the wiki
//     is the source, not the game DB.
//   - TierNarrative: wiki lore sentences, capped, each carrying its
//     page title as Source.
type Tier string

const (
	TierAuthoritative Tier = "authoritative"
	TierCurated       Tier = "curated"
	TierNarrative     Tier = "narrative"
)

// ValidTier reports whether t is a known tier.
func ValidTier(t Tier) bool {
	switch t {
	case TierAuthoritative, TierCurated, TierNarrative:
		return true
	}
	return false
}

// Card kinds. IDs are "kind:key" strings:
// monster:<mobID>, item:<itemID>, map:<mapname>, npc:<map>:<name>,
// place:<pageTitle>.
const (
	KindMonster = "monster"
	KindItem    = "item"
	KindMap     = "map"
	KindNPC     = "npc"
	KindPlace   = "place"
)

// Line is one pre-rendered fact line on a card. Text is ready for
// verbatim use by consumers — the album never formats at read time,
// so consumer prompts cannot drift from the bound artifact.
type Line struct {
	Tier   Tier   `json:"t"`
	Text   string `json:"x"`
	Source string `json:"src,omitempty"` // narrative: wiki page title
}

// Card is one entity's fact card.
type Card struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Name  string `json:"n"`
	Lines []Line `json:"l"`
}

// Sources cites the album's inputs (ADR-001 traceability).
type Sources struct {
	RathenaTag  string `json:"rathena_tag"`
	RathenaPath string `json:"rathena_path"`
	WikiRelease string `json:"wiki_release"`
	WikiPath    string `json:"wiki_path"`
}

// Counts self-reports the album's shape; Validate cross-checks.
type Counts struct {
	Cards      int `json:"cards"`
	Monsters   int `json:"monsters"`
	Items      int `json:"items"`
	Maps       int `json:"maps"`
	NPCs       int `json:"npcs"`
	Places     int `json:"places"`
	Narrative  int `json:"narrative_lines"`
	Curated    int `json:"curated_lines"`
	Referenced struct {
		MonsterIDs int `json:"monster_ids"` // wiki lore refs seen at build
		ItemIDs    int `json:"item_ids"`
		MapRefs    int `json:"map_refs"`
	} `json:"wiki_referenced"`
}

// Album is the bound artifact.
type Album struct {
	SchemaVersion int     `json:"schema_version"`
	Description   string  `json:"description"`
	Sources       Sources `json:"sources"`
	Built         string  `json:"built"`
	Converter     string  `json:"converter"`
	SourceHash    string  `json:"source_hash"`
	Counts        Counts  `json:"counts"`
	Cards         []Card  `json:"cards"`

	// index/byName are lookup maps built by Load; unexported so the
	// artifact remains pure data.
	index  map[string]Card `json:"-"`
	byName map[string]Card `json:"-"`
}

//go:embed album.json
var embedded []byte

var (
	defaultOnce sync.Once
	defaultA    *Album
	defaultErr  error
)

// Default returns the album from the embedded release artifact
// (parsed once per process).
func Default() (*Album, error) {
	defaultOnce.Do(func() {
		defaultA, defaultErr = Load(embedded)
	})
	return defaultA, defaultErr
}

// EmbeddedBytes exposes the embedded artifact bytes (for consumers
// that embed the album file on their side and want the canonical
// release to compare against).
func EmbeddedBytes() []byte { return append([]byte(nil), embedded...) }

// Load decodes and validates album bytes. It refuses schema versions
// other than SchemaVersion — no best-effort parsing across versions.
func Load(raw []byte) (*Album, error) {
	var a Album
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("album: parse: %w", err)
	}
	if a.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("album: schema version %d, want %d — regenerate the album with a matching builder/loader pair", a.SchemaVersion, SchemaVersion)
	}
	if err := Validate(&a); err != nil {
		return nil, err
	}
	return indexAlbum(&a), nil
}

// Get returns the card for an entity ID ("monster:1007"). Fully
// deterministic: same album, same ID, same card, every call. Cards
// are shared values; consumers must not mutate them.
func (a *Album) Get(id string) (Card, bool) {
	if a == nil {
		return Card{}, false
	}
	c, ok := a.index[id]
	return c, ok
}

// MonsterCard is the typed convenience for Get("monster:<id>").
func (a *Album) MonsterCard(mobID int) (Card, bool) {
	return a.Get(fmt.Sprintf("%s:%d", KindMonster, mobID))
}

// ItemCard is the typed convenience for Get("item:<id>").
func (a *Album) ItemCard(itemID int) (Card, bool) {
	return a.Get(fmt.Sprintf("%s:%d", KindItem, itemID))
}

// MapCard is the typed convenience for Get("map:<mapname>").
func (a *Album) MapCard(mapName string) (Card, bool) { return a.Get(KindMap + ":" + mapName) }

// ByName resolves a card by entity display name (normalized:
// case- and whitespace-insensitive). When a monster and an item share
// a normalized name the monster wins — first registration order is
// kind-canonical (monster, item, map, npc, place), and the album is
// built in that order. ok=false is the caller's fail-open signal.
func (a *Album) ByName(name string) (Card, bool) {
	if a == nil {
		return Card{}, false
	}
	c, ok := a.byName[normalizeName(name)]
	return c, ok
}

// CardIDs lists all card IDs in album order (deterministic).
func (a *Album) CardIDs() []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.Cards))
	for _, c := range a.Cards {
		out = append(out, c.ID)
	}
	return out
}

// Validate enforces the album contract: header counts match the
// cards, cards are in canonical order with unique IDs, kinds are
// known, every card carries 1..MaxLinesPerCard lines, every line has
// a valid tier and non-empty text, and narrative lines carry their
// page source.
func Validate(a *Album) error {
	if a.Counts.Cards != len(a.Cards) {
		return fmt.Errorf("album: header card count %d != %d cards", a.Counts.Cards, len(a.Cards))
	}
	if a.Counts.Narrative < 0 || a.Counts.Curated < 0 {
		return fmt.Errorf("album: negative line counts")
	}
	kindCounts := map[string]int{}
	seen := map[string]bool{}
	prev := ""
	narrative, curated := 0, 0
	for i, c := range a.Cards {
		switch {
		case c.ID == "":
			return fmt.Errorf("album: card %d: empty id", i)
		case c.Kind == "":
			return fmt.Errorf("album: card %d (%s): empty kind", i, c.ID)
		case kindOrder(c.Kind) == -1:
			return fmt.Errorf("album: card %d (%s): unknown kind %q", i, c.ID, c.Kind)
		case !strings.HasPrefix(c.ID, c.Kind+":"):
			return fmt.Errorf("album: card %d: id %q does not start with kind %q", i, c.ID, c.Kind)
		case c.Name == "":
			return fmt.Errorf("album: card %d (%s): empty name", i, c.ID)
		case len(c.Lines) == 0:
			return fmt.Errorf("album: card %s: no lines", c.ID)
		case len(c.Lines) > MaxLinesPerCard:
			return fmt.Errorf("album: card %s: %d lines exceeds cap %d", c.ID, len(c.Lines), MaxLinesPerCard)
		}
		if seen[c.ID] {
			return fmt.Errorf("album: card %d: duplicate id %q", i, c.ID)
		}
		seen[c.ID] = true
		key := kindKey(c)
		if prev != "" && key <= prev {
			return fmt.Errorf("album: card %d (%s) out of canonical order", i, c.ID)
		}
		prev = key
		kindCounts[c.Kind]++
		narrativeLines := 0
		for j, l := range c.Lines {
			switch {
			case !ValidTier(l.Tier):
				return fmt.Errorf("album: card %s line %d: unknown tier %q", c.ID, j, l.Tier)
			case l.Text == "":
				return fmt.Errorf("album: card %s line %d: empty text", c.ID, j)
			case l.Tier == TierNarrative && l.Source == "":
				return fmt.Errorf("album: card %s line %d: narrative without source page", c.ID, j)
			case l.Tier != TierNarrative && l.Source != "":
				return fmt.Errorf("album: card %s line %d: non-narrative line carries a source", c.ID, j)
			}
			if l.Tier == TierNarrative {
				narrative++
				narrativeLines++
			}
			if l.Tier == TierCurated {
				curated++
			}
		}
		if narrativeLines > MaxNarrative {
			return fmt.Errorf("album: card %s: %d narrative lines exceeds cap %d", c.ID, narrativeLines, MaxNarrative)
		}
	}
	if kindCounts[KindMonster] != a.Counts.Monsters ||
		kindCounts[KindItem] != a.Counts.Items ||
		kindCounts[KindMap] != a.Counts.Maps ||
		kindCounts[KindNPC] != a.Counts.NPCs ||
		kindCounts[KindPlace] != a.Counts.Places {
		return fmt.Errorf("album: header kind counts %+v disagree with cards %+v", a.Counts, kindCounts)
	}
	if narrative != a.Counts.Narrative || curated != a.Counts.Curated {
		return fmt.Errorf("album: header line counts (narrative=%d curated=%d) disagree with cards (narrative=%d curated=%d)",
			a.Counts.Narrative, a.Counts.Curated, narrative, curated)
	}
	return nil
}

// indexAlbum builds the lookup maps after Load-time validation.
// Called from Load; not exported (the album is read-only).
func indexAlbum(a *Album) *Album {
	a.index = make(map[string]Card, len(a.Cards))
	a.byName = make(map[string]Card, len(a.Cards))
	for _, c := range a.Cards {
		a.index[c.ID] = c
		if key := normalizeName(c.Name); key != "" {
			if _, exists := a.byName[key]; !exists {
				a.byName[key] = c
			}
		}
	}
	return a
}

func kindOrder(k string) int {
	switch k {
	case KindMonster:
		return 0
	case KindItem:
		return 1
	case KindMap:
		return 2
	case KindNPC:
		return 3
	case KindPlace:
		return 4
	}
	return -1
}

// kindKey is the canonical sort key: kind order, then ID.
func kindKey(c Card) string {
	return fmt.Sprintf("%d\x00%s", kindOrder(c.Kind), c.ID)
}

func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Join(strings.Fields(s), " ")
}

// SortCards orders cards canonically in place (builder-side helper;
// the loader re-checks the order on every Load).
func SortCards(cards []Card) {
	sort.Slice(cards, func(i, j int) bool {
		ki, kj := kindOrder(cards[i].Kind), kindOrder(cards[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return cards[i].ID < cards[j].ID
	})
}
