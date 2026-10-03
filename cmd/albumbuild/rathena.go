// albumbuild rathena.go — the rAthena slice of the album binder:
// everything the ragnaDataSDK serves for cards, rendered as
// TierAuthoritative lines. rAthena is the authoritative source for
// every number in the album (ADR-001); the wiki may only add
// narrative and curated lines around them.
//
// All transforms are deterministic: stable sorts with total-order
// tie-breaks, so the same pinned DB binds a byte-identical album.
package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/lenaxia/RagnaAlbum/album"
	ragnadata "github.com/lenaxia/ragnaDataSDK/pkg/ragnadata"
)

// rathenaSource is the SDK surface the binder consumes (narrow so
// tests stub it without a DB install; *ragnadata.SDK satisfies it).
type rathenaSource interface {
	SearchMonsters(query ragnadata.MonsterSearchQuery, mode ragnadata.FormulaMode) ([]*ragnadata.MonsterInfo, error)
	SearchItems(query ragnadata.ItemSearchQuery, mode ragnadata.FormulaMode) ([]*ragnadata.ItemInfo, error)
	GetMonsterByID(id int, mode ragnadata.FormulaMode) (*ragnadata.MonsterInfo, error)
	GetItemByID(id int, mode ragnadata.FormulaMode) (*ragnadata.ItemInfo, error)
	GetAllNPCs() []*ragnadata.NPCInfo
}

// rathenaFacts is the extracted, pre-join slice of the DB.
type rathenaFacts struct {
	Monsters []*ragnadata.MonsterInfo
	Items    []*ragnadata.ItemInfo
	NPCs     []*ragnadata.NPCInfo
}

// loadRathena pulls the full monster/item/NPC sets (empty queries =
// no filters, no limit — the SDK returns everything).
func loadRathena(src rathenaSource) (*rathenaFacts, error) {
	mode := ragnadata.FormulaModeRenewal
	mons, err := src.SearchMonsters(ragnadata.MonsterSearchQuery{}, mode)
	if err != nil {
		return nil, fmt.Errorf("albumbuild: monster query: %w", err)
	}
	items, err := src.SearchItems(ragnadata.ItemSearchQuery{}, mode)
	if err != nil {
		return nil, fmt.Errorf("albumbuild: item query: %w", err)
	}
	return &rathenaFacts{Monsters: mons, Items: items, NPCs: src.GetAllNPCs()}, nil
}

// monsterCard binds one monster's card. Authoritative lines: identity,
// drops, spawns — each omitted when empty (a card always keeps ≥1
// line; a monster without drops still has identity).
func monsterCard(m *ragnadata.MonsterInfo, itemNames map[int]string) album.Card {
	c := album.Card{
		ID:   fmt.Sprintf("%s:%d", album.KindMonster, m.ID),
		Kind: album.KindMonster,
		Name: m.Name,
	}
	if line := identityLine(m); line != "" {
		c.Lines = append(c.Lines, album.Line{Tier: album.TierAuthoritative, Text: line})
	}
	if line := dropsLine(m, itemNames); line != "" {
		c.Lines = append(c.Lines, album.Line{Tier: album.TierAuthoritative, Text: line})
	}
	if line := spawnsLine(m); line != "" {
		c.Lines = append(c.Lines, album.Line{Tier: album.TierAuthoritative, Text: line})
	}
	return c
}

func identityLine(m *ragnadata.MonsterInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Lv%d", m.Level)
	if m.Race != "" {
		fmt.Fprintf(&b, " %s", strings.ToLower(m.Race))
	}
	if m.Size != "" {
		fmt.Fprintf(&b, " (%s)", m.Size)
	}
	if m.Element != "" {
		fmt.Fprintf(&b, ", %s", elementLabel(m))
	}
	if m.IsBoss {
		b.WriteString(", boss")
	}
	return b.String()
}

func elementLabel(m *ragnadata.MonsterInfo) string {
	if m.ElementLevel > 1 {
		return fmt.Sprintf("%s%d", m.Element, m.ElementLevel)
	}
	return m.Element
}

// allDrops merges regular and MVP drops (both are drop-table facts).
func allDrops(m *ragnadata.MonsterInfo) []ragnadata.ItemDrop {
	out := make([]ragnadata.ItemDrop, 0, len(m.Drops)+len(m.MvpDrops))
	out = append(out, m.Drops...)
	out = append(out, m.MvpDrops...)
	return out
}

type dropRow struct {
	name string
	rate float64
}

// selectDrops applies the top-5-plus-card rule: the 5 highest-rate
// drops (rate desc, name asc) plus the alphabetically-first card when
// one exists outside the top 5 — cards are the conversationally
// salient drop even at 0.2%. Cap album.MaxLinesPerCard overall.
func selectDrops(m *ragnadata.MonsterInfo, itemNames map[int]string) []dropRow {
	rows := make([]dropRow, 0, len(m.Drops)+len(m.MvpDrops))
	for _, d := range allDrops(m) {
		name := d.ItemName
		if clean, ok := itemNames[d.ItemID]; ok && clean != "" {
			name = clean // DB display name, never the Aegis spelling
		}
		if name == "" || isWeirdName(name) {
			continue
		}
		rows = append(rows, dropRow{name: name, rate: d.Rate})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].rate != rows[j].rate {
			return rows[i].rate > rows[j].rate
		}
		return rows[i].name < rows[j].name
	})
	top := rows
	if len(top) > 5 {
		top = top[:5]
	}
	if len(rows) > 5 {
		for i := 5; i < len(rows); i++ {
			if strings.HasSuffix(rows[i].name, " Card") {
				top = append(top, rows[i])
				break
			}
		}
	}
	if len(top) > 6 {
		top = top[:6]
	}
	return top
}

func dropsLine(m *ragnadata.MonsterInfo, itemNames map[int]string) string {
	top := selectDrops(m, itemNames)
	if len(top) == 0 {
		return ""
	}
	parts := make([]string, 0, len(top))
	for _, r := range top {
		parts = append(parts, fmt.Sprintf("%s %s", r.name, formatPct(r.rate)))
	}
	return "drops " + strings.Join(parts, ", ")
}

// formatPct renders a DB rate (0.0-1.0) as a percent string with up
// to two decimals, trailing zeros trimmed ("70%", "0.2%").
func formatPct(rate float64) string {
	pct := rate * 100
	s := fmt.Sprintf("%.2f", pct)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		s = "0"
	}
	return s + "%"
}

// spawnMapList lists distinct spawn map names, sorted, capped.
func spawnMapList(m *ragnadata.MonsterInfo) []string {
	seen := make(map[string]bool, len(m.SpawnLocations))
	for _, s := range m.SpawnLocations {
		if s.Map != "" {
			seen[s.Map] = true
		}
	}
	maps := make([]string, 0, len(seen))
	for name := range seen {
		maps = append(maps, name)
	}
	sort.Strings(maps)
	if len(maps) > maxSpawnMaps {
		maps = maps[:maxSpawnMaps]
	}
	return maps
}

const maxSpawnMaps = 6

func spawnsLine(m *ragnadata.MonsterInfo) string {
	maps := spawnMapList(m)
	if len(maps) == 0 {
		return ""
	}
	total := distinctSpawnMaps(m)
	line := "spawns " + strings.Join(maps, ", ")
	if total > len(maps) {
		line += fmt.Sprintf(" (+%d more)", total-len(maps))
	}
	return line
}

func distinctSpawnMaps(m *ragnadata.MonsterInfo) int {
	seen := make(map[string]bool, len(m.SpawnLocations))
	for _, s := range m.SpawnLocations {
		if s.Map != "" {
			seen[s.Map] = true
		}
	}
	return len(seen)
}

// itemCard binds one item's card. Authoritative line: type + NPC
// prices + slots/equip level, omitted pieces when absent.
func itemCard(it *ragnadata.ItemInfo) album.Card {
	c := album.Card{
		ID:   fmt.Sprintf("%s:%d", album.KindItem, it.ID),
		Kind: album.KindItem,
		Name: it.Name,
	}
	if line := itemLine(it); line != "" {
		c.Lines = append(c.Lines, album.Line{Tier: album.TierAuthoritative, Text: line})
	}
	return c
}

func itemLine(it *ragnadata.ItemInfo) string {
	var b strings.Builder
	if it.Type != "" {
		b.WriteString(it.Type)
	}
	switch {
	case it.Buy > 0 && it.Sell > 0:
		sep := "; "
		if b.Len() == 0 {
			sep = ""
		}
		fmt.Fprintf(&b, "%sNPC buy %dz, sell %dz", sep, it.Buy, it.Sell)
	case it.Buy > 0:
		sep := "; "
		if b.Len() == 0 {
			sep = ""
		}
		fmt.Fprintf(&b, "%sNPC buy %dz", sep, it.Buy)
	case it.Sell > 0:
		sep := "; "
		if b.Len() == 0 {
			sep = ""
		}
		fmt.Fprintf(&b, "%sNPC sell %dz", sep, it.Sell)
	}
	equip := it.EquipLevelMin
	if equip < 0 {
		equip = 0
	}
	if it.Slots > 0 || equip > 0 {
		var parts []string
		if it.Slots > 0 {
			parts = append(parts, fmt.Sprintf("%d slots", it.Slots))
		}
		if equip > 0 {
			parts = append(parts, fmt.Sprintf("equip Lv%d", equip))
		}
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(strings.Join(parts, ", "))
	}
	return b.String()
}

// itemIncluded is the conversational-relevant inclusion rule: an item
// gets a card when the DB gives it a price, some monster drops it, or
// the wiki narrative pass claimed it. Never-priced, never-dropped,
// never-discussed items are dead weight no consumer can look up.
func itemIncluded(it *ragnadata.ItemInfo, dropped map[int]bool, loreItems map[int]bool) bool {
	return it.Buy > 0 || it.Sell > 0 || dropped[it.ID] || loreItems[it.ID]
}

// itemNameIndex builds id→clean display name for drop joining.
func itemNameIndex(items []*ragnadata.ItemInfo) map[int]string {
	idx := make(map[int]string, len(items))
	for _, it := range items {
		if it.ID > 0 && it.Name != "" {
			idx[it.ID] = it.Name
		}
	}
	return idx
}

// droppedItemIDs indexes every item some monster drops.
func droppedItemIDs(mons []*ragnadata.MonsterInfo) map[int]bool {
	out := map[int]bool{}
	for _, m := range mons {
		if m == nil {
			continue
		}
		for _, d := range allDrops(m) {
			out[d.ItemID] = true
		}
	}
	return out
}

// npcCleanName strips the "#<instance>" suffix and whitespace.
func npcCleanName(name string) string {
	if i := strings.IndexByte(name, '#'); i >= 0 {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}

// npcRow is one deduplicated conversational NPC.
type npcRow struct {
	Name string
	Map  string
	Shop bool // type=shop in the source; otherwise kafra-named
}

// npcRows selects the conversational NPC subset: shop staff and
// kafras — the staff small talk can name. Deduplicated by (map,
// cleaned name), sorted by (map, name).
func npcRows(npcs []*ragnadata.NPCInfo) []npcRow {
	type key struct{ mapName, name string }
	seen := map[key]bool{}
	out := make([]npcRow, 0, 256)
	for _, n := range npcs {
		if n == nil || n.Map == "" {
			continue
		}
		name := npcCleanName(n.Name)
		if name == "" || isWeirdName(name) {
			continue
		}
		isShop := n.Type == "shop"
		isKafra := strings.HasPrefix(name, "Kafra")
		if !isShop && !isKafra {
			continue
		}
		k := key{n.Map, name}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, npcRow{Name: name, Map: n.Map, Shop: isShop})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Map != out[j].Map {
			return out[i].Map < out[j].Map
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// npcCards binds NPC cards from the deduplicated rows; one
// authoritative line states the role and map.
func npcCards(rows []npcRow) []album.Card {
	out := make([]album.Card, 0, len(rows))
	for _, r := range rows {
		role := "kafra service"
		if r.Shop {
			role = "shop staff"
		}
		out = append(out, album.Card{
			ID:   album.KindNPC + ":" + r.Map + ":" + r.Name,
			Kind: album.KindNPC,
			Name: r.Name,
			Lines: []album.Line{{
				Tier: album.TierAuthoritative,
				Text: fmt.Sprintf("%s in %s (rAthena NPC)", role, r.Map),
			}},
		})
	}
	return out
}

// mapCards binds one card per map that spawns monsters or hosts an
// included NPC. Authoritative: top monsters by total spawn count
// (count desc, name asc, cap 4) and the shop count. Curated: the
// wiki's display name, when the wiki has one.
type mapLineFacts struct {
	topMonsters []string
	shops       int
}

func mapCard(mapName string, facts mapLineFacts, wikiDisplay string) (album.Card, bool) {
	c := album.Card{
		ID:   album.KindMap + ":" + mapName,
		Kind: album.KindMap,
		Name: wikiDisplay,
	}
	if c.Name == "" {
		c.Name = mapName
	}
	var b strings.Builder
	if len(facts.topMonsters) > 0 {
		b.WriteString("monsters: " + strings.Join(facts.topMonsters, ", "))
	}
	if facts.shops > 0 {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		if facts.shops == 1 {
			b.WriteString("1 shop")
		} else {
			fmt.Fprintf(&b, "%d shops", facts.shops)
		}
	}
	if b.Len() > 0 {
		c.Lines = append(c.Lines, album.Line{Tier: album.TierAuthoritative, Text: b.String()})
	}
	if wikiDisplay != "" && wikiDisplay != mapName {
		c.Lines = append(c.Lines, album.Line{Tier: album.TierCurated, Text: "wiki name: " + wikiDisplay})
	}
	return c, len(c.Lines) > 0
}

// mapFacts aggregates spawn counts and shop counts per map. Every map
// with spawns or an included NPC gets an entry.
func mapFacts(mons []*ragnadata.MonsterInfo, npcs []npcRow) map[string]mapLineFacts {
	spawnCount := map[string]map[string]int{}
	for _, m := range mons {
		if m == nil {
			continue
		}
		for _, s := range m.SpawnLocations {
			if s.Map == "" || s.Amount <= 0 {
				continue
			}
			if spawnCount[s.Map] == nil {
				spawnCount[s.Map] = map[string]int{}
			}
			spawnCount[s.Map][m.Name] += s.Amount
		}
	}
	shops := map[string]int{}
	for _, r := range npcs {
		if r.Shop {
			shops[r.Map]++
		}
	}
	out := map[string]mapLineFacts{}
	for mapName, counts := range spawnCount {
		type row struct {
			name  string
			count int
		}
		rows := make([]row, 0, len(counts))
		for name, count := range counts {
			rows = append(rows, row{name, count})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].count != rows[j].count {
				return rows[i].count > rows[j].count
			}
			return rows[i].name < rows[j].name
		})
		if len(rows) > maxMapMonsters {
			rows = rows[:maxMapMonsters]
		}
		f := mapLineFacts{shops: shops[mapName]}
		for _, r := range rows {
			f.topMonsters = append(f.topMonsters, r.name)
		}
		out[mapName] = f
	}
	for mapName, n := range shops {
		if _, ok := out[mapName]; !ok {
			out[mapName] = mapLineFacts{shops: n} // NPC-hosting map without spawns
		}
	}
	return out
}

const maxMapMonsters = 4

// isWeirdName rejects names a consumer's entity checks could never
// admit (instance suffixes, raw Aegis spellings).
func isWeirdName(name string) bool {
	if len(name) > 40 || len(name) < 2 {
		return true
	}
	if strings.Contains(name, "#") {
		return true
	}
	if strings.HasPrefix(name, "_") || strings.HasSuffix(name, "_") {
		return true
	}
	return false
}
