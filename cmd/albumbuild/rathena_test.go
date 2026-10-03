package main

import (
	"strings"
	"testing"

	"github.com/lenaxia/RagnaAlbum/album"
	ragnadata "github.com/lenaxia/ragnaDataSDK/pkg/ragnadata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func monsterFixture() *ragnadata.MonsterInfo {
	return &ragnadata.MonsterInfo{
		ID: 1007, Name: "Fabre", Level: 6, Race: "Insect", Size: "Small",
		Element: "Earth", ElementLevel: 1,
		Drops: []ragnadata.ItemDrop{
			{ItemID: 914, ItemName: "Fluff", Rate: 0.70},
			{ItemID: 7101, ItemName: "Green_Herb", Rate: 0.30},
			{ItemID: 949, ItemName: "Feather", Rate: 0.10},
			{ItemID: 705, ItemName: "Clover", Rate: 0.10},
			{ItemID: 601, ItemName: "Wing_Of_Fly", Rate: 0.05},
			{ItemID: 1502, ItemName: "Club_", Rate: 0.008},
			{ItemID: 4048, ItemName: "Fabre_Card", Rate: 0.002},
		},
		SpawnLocations: []ragnadata.SpawnInfo{
			{Map: "prt_fild08", Amount: 40},
			{Map: "prt_fild07", Amount: 30},
			{Map: "prt_fild01", Amount: 20},
		},
	}
}

func itemNamesFixture() map[int]string {
	return map[int]string{
		914: "Fluff", 7101: "Green Herb", 949: "Feather", 705: "Clover",
		601: "Wing of Fly", 1502: "Club", 4048: "Fabre Card",
	}
}

func lineTexts(c album.Card) []string {
	out := make([]string, 0, len(c.Lines))
	for _, l := range c.Lines {
		out = append(out, l.Text)
	}
	return out
}

func TestMonsterCard_TierLabeledLines(t *testing.T) {
	card := monsterCard(monsterFixture(), itemNamesFixture())

	assert.Equal(t, "monster:1007", card.ID)
	assert.Equal(t, album.KindMonster, card.Kind)
	assert.Equal(t, "Fabre", card.Name)
	require.Len(t, card.Lines, 3)
	for _, l := range card.Lines {
		assert.Equal(t, album.TierAuthoritative, l.Tier, "rathena lines are authoritative")
		assert.Empty(t, l.Source)
	}

	assert.Equal(t, "Lv6 insect (Small), Earth", card.Lines[0].Text)
	// Top 5 by rate with name-asc tie-break, then the card rescue;
	// Aegis spellings never render.
	assert.Equal(t,
		"drops Fluff 70%, Green Herb 30%, Clover 10%, Feather 10%, Wing of Fly 5%, Fabre Card 0.2%",
		card.Lines[1].Text)
	assert.Equal(t, "spawns prt_fild01, prt_fild07, prt_fild08", card.Lines[2].Text)
}

func TestMonsterCard_SpawnCapCountsMore(t *testing.T) {
	m := monsterFixture()
	for i := 0; i < 10; i++ {
		m.SpawnLocations = append(m.SpawnLocations, ragnadata.SpawnInfo{Map: "map_" + string(rune('a'+i)), Amount: 1})
	}
	line := spawnsLine(m)
	require.True(t, strings.HasPrefix(line, "spawns "))
	require.Contains(t, line, "(+7 more)", "13 distinct maps − 6 listed = 7 more")
	listed := strings.TrimPrefix(strings.SplitN(line, " (+", 2)[0], "spawns ")
	require.Len(t, strings.Split(listed, ", "), maxSpawnMaps)
	assert.True(t, strings.HasPrefix(listed, "map_"), "capped list stays sorted")
}

func TestMonsterCard_BossAndElementLevel(t *testing.T) {
	m := &ragnadata.MonsterInfo{ID: 1039, Name: "Baphomet", Level: 81, Race: "Demon", Size: "Large", Element: "Dark", ElementLevel: 2, IsBoss: true}
	card := monsterCard(m, nil)
	require.Len(t, card.Lines, 1)
	assert.Equal(t, "Lv81 demon (Large), Dark2, boss", card.Lines[0].Text)
}

func TestFormatPct(t *testing.T) {
	assert.Equal(t, "70%", formatPct(0.70))
	assert.Equal(t, "0.2%", formatPct(0.002))
	assert.Equal(t, "5%", formatPct(0.05))
	assert.Equal(t, "100%", formatPct(1.0))
	assert.Equal(t, "0.03%", formatPct(0.0003))
}

func TestItemCard_Lines(t *testing.T) {
	card := itemCard(&ragnadata.ItemInfo{ID: 501, Name: "Red Potion", Type: "Healing", Buy: 10, Sell: 5})
	assert.Equal(t, "item:501", card.ID)
	require.Len(t, card.Lines, 1)
	assert.Equal(t, "Healing; NPC buy 10z, sell 5z", card.Lines[0].Text)

	bow := itemCard(&ragnadata.ItemInfo{ID: 1710, Name: "Hunter Bow", Type: "Weapon", Buy: 44000, Slots: 3, EquipLevelMin: 1})
	assert.Equal(t, "Weapon; NPC buy 44000z; 3 slots, equip Lv1", bow.Lines[0].Text)

	sellOnly := itemCard(&ragnadata.ItemInfo{ID: 914, Name: "Fluff", Sell: 4})
	assert.Equal(t, "NPC sell 4z", sellOnly.Lines[0].Text)

	noPrice := itemCard(&ragnadata.ItemInfo{ID: 2, Name: "Odd", Type: "Etc"})
	require.Len(t, noPrice.Lines, 1)
	assert.Equal(t, "Etc", noPrice.Lines[0].Text)

	negEquip := itemCard(&ragnadata.ItemInfo{ID: 3, Name: "X", EquipLevelMin: -3})
	assert.Empty(t, negEquip.Lines, "no facts at all → empty card (bind skips it)")
}

func TestItemIncluded(t *testing.T) {
	dropped := map[int]bool{914: true}
	lore := map[int]bool{9999: true}
	assert.True(t, itemIncluded(&ragnadata.ItemInfo{ID: 1, Buy: 10}, dropped, lore))
	assert.True(t, itemIncluded(&ragnadata.ItemInfo{ID: 2, Sell: 1}, dropped, lore))
	assert.True(t, itemIncluded(&ragnadata.ItemInfo{ID: 914}, dropped, lore), "dropped items enter")
	assert.True(t, itemIncluded(&ragnadata.ItemInfo{ID: 9999}, dropped, lore), "wiki-claimed items enter")
	assert.False(t, itemIncluded(&ragnadata.ItemInfo{ID: 3}, dropped, lore), "unpriced/undropped/unmentioned stay out")
}

func TestNPCRows_ShopsAndKafrasOnly(t *testing.T) {
	rows := npcRows([]*ragnadata.NPCInfo{
		{Name: "Tool Dealer#4", Map: "prontera", Type: "shop"},
		{Name: "Tool Dealer#9", Map: "prontera", Type: "shop"}, // same map+cleaned name → dedup
		{Name: "Kafra Employee", Map: "prontera", Type: "script"},
		{Name: "Mysterious Wanderer", Map: "prontera", Type: "script"}, // not shop/kafra → excluded
		{Name: "Tool Dealer", Map: "geffen", Type: "shop"},
		{Name: "", Map: "geffen", Type: "shop"},
		{Name: "Kafra Employee", Map: "alberta", Type: "script"},
	})
	require.Len(t, rows, 4)
	// Sorted by (map, name).
	assert.Equal(t, "Kafra Employee", rows[0].Name) // alberta
	assert.Equal(t, "geffen", rows[1].Map)
	assert.Equal(t, "prontera", rows[2].Map)
	assert.Equal(t, "Kafra Employee", rows[2].Name) // prontera: Kafra then Tool Dealer
	assert.Equal(t, "Tool Dealer", rows[3].Name)
	assert.True(t, rows[1].Shop)
	assert.False(t, rows[0].Shop, "kafra is not type=shop")

	cards := npcCards(rows)
	require.Len(t, cards, 4)
	assert.Equal(t, "npc:prontera:Tool Dealer", cards[3].ID)
	assert.Equal(t, "shop staff in prontera (rAthena NPC)", cards[3].Lines[0].Text)
	assert.Equal(t, "kafra service in alberta (rAthena NPC)", cards[0].Lines[0].Text)
}

func TestMapFactsAndCard(t *testing.T) {
	m := monsterFixture()
	rows := []npcRow{{Name: "Tool Dealer", Map: "prt_fild08", Shop: true}, {Name: "Kafra Employee", Map: "prontera", Shop: false}}
	facts := mapFacts([]*ragnadata.MonsterInfo{m}, rows)

	prt08, ok := facts["prt_fild08"]
	require.True(t, ok)
	assert.Equal(t, []string{"Fabre"}, prt08.topMonsters)
	assert.Equal(t, 1, prt08.shops)

	card, ok := mapCard("prt_fild08", prt08, "Prontera Field 8")
	require.True(t, ok)
	assert.Equal(t, "map:prt_fild08", card.ID)
	assert.Equal(t, "Prontera Field 8", card.Name)
	require.Len(t, card.Lines, 2)
	assert.Equal(t, album.TierAuthoritative, card.Lines[0].Tier)
	assert.Equal(t, "monsters: Fabre; 1 shop", card.Lines[0].Text)
	assert.Equal(t, album.TierCurated, card.Lines[1].Tier)
	assert.Equal(t, "wiki name: Prontera Field 8", card.Lines[1].Text)

	// Map without a wiki display name falls back to the map name and
	// drops the curated line.
	bare, ok := mapCard("prt_fild01", facts["prt_fild01"], "")
	require.True(t, ok)
	assert.Equal(t, "prt_fild01", bare.Name)
	require.Len(t, bare.Lines, 1)
}

func TestMapFacts_TopMonstersOrdering(t *testing.T) {
	mons := []*ragnadata.MonsterInfo{
		{ID: 1, Name: "Poring", SpawnLocations: []ragnadata.SpawnInfo{{Map: "field", Amount: 50}}},
		{ID: 2, Name: "Fabre", SpawnLocations: []ragnadata.SpawnInfo{{Map: "field", Amount: 30}, {Map: "field", Amount: 30}}},
		{ID: 3, Name: "Lunatic", SpawnLocations: []ragnadata.SpawnInfo{{Map: "field", Amount: 30}}},
		{ID: 4, Name: "Willow", SpawnLocations: []ragnadata.SpawnInfo{{Map: "field", Amount: 20}}},
		{ID: 5, Name: "Zombie", SpawnLocations: []ragnadata.SpawnInfo{{Map: "field", Amount: 10}}},
	}
	facts := mapFacts(mons, nil)
	// Fabre's two spawn entries aggregate (60) → first; ties (Poring
	// excluded) break by name.
	assert.Equal(t, []string{"Fabre", "Poring", "Lunatic", "Willow"}, facts["field"].topMonsters)
}

func TestIsWeirdName(t *testing.T) {
	assert.True(t, isWeirdName("A#1"))
	assert.True(t, isWeirdName("_Leading"))
	assert.True(t, isWeirdName("Trailing_"))
	assert.True(t, isWeirdName(""))
	assert.False(t, isWeirdName("Fabre"))
}
