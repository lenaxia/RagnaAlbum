package main

import (
	"bytes"
	"testing"

	"github.com/lenaxia/RagnaAlbum/album"
	ragnadata "github.com/lenaxia/ragnaDataSDK/pkg/ragnadata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bindFixture(t *testing.T) *album.Album {
	t.Helper()
	rf := &rathenaFacts{
		Monsters: []*ragnadata.MonsterInfo{monsterFixture(), {ID: 1002, Name: "Poring", Level: 1}},
		Items: []*ragnadata.ItemInfo{
			{ID: 501, Name: "Red Potion", Type: "Healing", Buy: 10, Sell: 5},
			{ID: 999, Name: "Steel", Buy: 2000},
			{ID: 4048, Name: "Fabre Card"},
			{ID: 9999, Name: "Lore Only"},
			{ID: 60000, Name: "Void Item"},
		},
		NPCs: []*ragnadata.NPCInfo{{Name: "Tool Dealer", Map: "prontera", Type: "shop"}},
	}
	wf := newWikiFacts()
	wf.MonsterLore[1007] = []loreCandidate{{sentence: "Fabre lore of legend.", pageTitle: "Page A", strong: 2, length: 50}}
	wf.ItemLore[9999] = []loreCandidate{{sentence: "Lore-only item of legend.", pageTitle: "Page B", strong: 2, length: 50}}
	wf.MapDisplays["prt_fild08"] = "Prontera Field 8"
	wf.PlacePages["Prontera Field 8"] = &placePage{
		title: "Prontera Field 8", pageID: 9, maps: map[string]bool{"prt_fild08": true},
		candidates: []loreCandidate{{sentence: "The fields around Prontera of legend.", pageTitle: "Prontera Field 8", strong: 2, length: 60}},
	}
	wf.Referenced = referencedCounts{MonsterIDs: 5, ItemIDs: 7, MapRefs: 3}

	a, err := bind(rf, wf, albumHeader{
		description: "test album",
		sources:     album.Sources{RathenaTag: "tag", WikiRelease: "rel"},
		built:       "2026-10-02", converter: "albumbuild-1", sourceHash: "abc123",
	})
	require.NoError(t, err)
	// Exercise the loaded (indexed) album, exactly as consumers see it.
	raw, err := marshalAlbum(a)
	require.NoError(t, err)
	loaded, err := album.Load(raw)
	require.NoError(t, err)
	return loaded
}

func TestBind_JoinsCountsAndOrder(t *testing.T) {
	a := bindFixture(t)

	// Canonical card order: monsters (by id), then items, maps, npcs,
	// places — each kind-group id-sorted.
	require.Greater(t, len(a.Cards), 0)
	var kinds []string
	ids := map[string]bool{}
	for _, c := range a.Cards {
		kinds = append(kinds, c.Kind)
		assert.False(t, ids[c.ID], "duplicate card id "+c.ID)
		ids[c.ID] = true
	}
	// Kind blocks are contiguous and ordered.
	rank := map[string]int{album.KindMonster: 0, album.KindItem: 1, album.KindMap: 2, album.KindNPC: 3, album.KindPlace: 4}
	lastRank, seenKinds := -1, map[string]bool{}
	for _, k := range kinds {
		if rank[k] < lastRank {
			t.Fatalf("kind order broken: %v", kinds)
		}
		lastRank = rank[k]
		seenKinds[k] = true
	}

	assert.Equal(t, 2, a.Counts.Monsters)
	assert.Equal(t, 3, a.Counts.Items, "priced ∪ dropped ∪ lore-claimed")
	assert.Equal(t, 1, a.Counts.NPCs)
	assert.Equal(t, 1, a.Counts.Places)
	assert.GreaterOrEqual(t, a.Counts.Maps, 1)
	assert.Equal(t, len(a.Cards), a.Counts.Cards)
	assert.Equal(t, 5, a.Counts.Referenced.MonsterIDs)
	assert.Equal(t, 7, a.Counts.Referenced.ItemIDs)
	assert.Equal(t, 3, a.Counts.Referenced.MapRefs)

	// Lore joined by ID, tier-labeled with source.
	fabre, ok := a.Get("monster:1007")
	require.True(t, ok)
	var narrative []album.Line
	for _, l := range fabre.Lines {
		if l.Tier == album.TierNarrative {
			narrative = append(narrative, l)
		}
	}
	require.Len(t, narrative, 1)
	assert.Equal(t, "Fabre lore of legend.", narrative[0].Text)
	assert.Equal(t, "Page A", narrative[0].Source)

	// Excluded item truly absent.
	_, ok = a.Get("item:60000")
	assert.False(t, ok)

	// Place card present with curated maps line.
	place, ok := a.Get("place:Prontera Field 8")
	require.True(t, ok)
	require.NotEmpty(t, place.Lines)
	assert.Equal(t, album.TierCurated, place.Lines[0].Tier)
	assert.Contains(t, place.Lines[0].Text, "prt_fild08")
}

func TestBind_NarrativeAndCuratedCounts(t *testing.T) {
	a := bindFixture(t)
	narrative, curated := 0, 0
	for _, c := range a.Cards {
		for _, l := range c.Lines {
			switch l.Tier {
			case album.TierNarrative:
				narrative++
			case album.TierCurated:
				curated++
			}
		}
	}
	assert.Equal(t, narrative, a.Counts.Narrative)
	assert.Equal(t, curated, a.Counts.Curated)
}

func TestMarshal_DeterministicBytes(t *testing.T) {
	build := func() []byte {
		raw, err := marshalAlbum(bindFixture(t))
		require.NoError(t, err)
		return raw
	}
	first := build()
	for i := 0; i < 5; i++ {
		require.True(t, bytes.Equal(first, build()), "rebind %d not byte-identical", i)
	}
}

func TestMarshal_RoundTripsThroughLoad(t *testing.T) {
	raw, err := marshalAlbum(bindFixture(t))
	require.NoError(t, err)
	a, err := album.Load(raw)
	require.NoError(t, err)
	again, err := marshalAlbum(a)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(raw, again), "Load→marshal round trip is lossless")
}
