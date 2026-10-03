package main

import (
	"context"
	"testing"

	"github.com/lenaxia/RagnaAlbum/album"
	"github.com/lenaxia/iroWikiScraper/sdk/irowiki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dbNamesFixture(kind string, id int) string {
	switch {
	case kind == "monster" && id == 1002:
		return "Poring"
	case kind == "item" && id == 999:
		return "Steel"
	case kind == "monster" && id == 1252:
		return "Hatii"
	}
	return ""
}

func newWalkState() (*wikiFacts, map[string]map[string]int, map[int]bool, map[int]bool, map[string]bool) {
	return newWikiFacts(),
		map[string]map[string]int{},
		map[int]bool{}, map[int]bool{}, map[string]bool{}
}

func TestExtractWikiPage_LoreAdmission(t *testing.T) {
	f, votes, seenMon, seenItem, seenMaps := newWalkState()
	md := "" +
		"# Heading is skipped\n" +
		"| table row is skipped\n" +
		"- [Hatii](monster:1252) is located on [Lutie Field](map:xmas_fild01) (2 hours)\n" +
		"There's big trouble in the world of the little [Porings](monster:1002), as they are taking the war between [Angeling](monster:1096) and [Deviling](monster:1582) to a new level.\n" +
		"Short line.\n" +
		"You have heard the legends that say 3 [Steel](item:999) must be forged anew by the smiths.\n" +
		"5 [Octopus Leg](monster:2218)\n" + // mis-annotated: id 2218 is not that name → rejected as lore
		"[Prontera](map:prontera) is the capital city.\n" +
		"[Map](map:prt_fild08)\n"

	extractWikiPage(f, votes, seenMon, seenItem, seenMaps, 42, "Test Page", md, dbNamesFixture)

	// Hatii: located-sentence admitted, markers keep text, links stripped.
	require.Len(t, f.MonsterLore[1252], 1)
	assert.Contains(t, f.MonsterLore[1252][0].sentence, "Hatii is located on Lutie Field")
	assert.NotContains(t, f.MonsterLore[1252][0].sentence, "](map:")

	// Poring via plural marker; Angeling/Deviling do not resolve (no DB names).
	require.Len(t, f.MonsterLore[1002], 1)
	assert.Contains(t, f.MonsterLore[1002][0].sentence, "world of the little Porings")

	// Steel admitted but flagged second-person.
	require.Len(t, f.ItemLore[999], 1)
	assert.Equal(t, 1, f.ItemLore[999][0].secondPers)

	// Mis-annotation rejected as lore but still counted as a reference.
	assert.NotContains(t, f.MonsterLore, 2218)
	assert.True(t, seenMon[2218], "coverage counts every marker seen, admitted or not")
	assert.True(t, seenMon[1096] && seenMon[1582])

	// Map display votes: placeholder "Map" skipped.
	assert.Equal(t, map[string]map[string]int{
		"xmas_fild01": {"Lutie Field": 1},
		"prontera":    {"Prontera": 1},
	}, votes)
	assert.True(t, seenMaps["prt_fild08"], "placeholder display still counts the map ref")
}

func TestSelectLore_RankingDistinctPagesAndDedup(t *testing.T) {
	cands := []loreCandidate{
		{sentence: "You must give Steel.", pageTitle: "Quest", strong: 1, secondPers: 1, length: 60},
		{sentence: "Hatii is a terrible beast of legend.", pageTitle: "MVP", strong: 2, length: 80},
		{sentence: "Another strong tale of legend here.", pageTitle: "MVP", strong: 2, length: 70},
		{sentence: "Hatii is a terrible beast of legend.", pageTitle: "Mirror", strong: 2, length: 80}, // dup sentence
		{sentence: "A neutral narrative line about the lair.", pageTitle: "Zeta", strong: 1, length: 50},
	}
	lore := selectLore(cands)
	require.Len(t, lore, album.MaxNarrative)
	for _, l := range lore {
		assert.Equal(t, album.TierNarrative, l.Tier)
		assert.NotEmpty(t, l.Source, "narrative lines carry their page")
	}
	// Strong-lore first; among them shorter first; the second entry of
	// the same page is skipped, the mirror-page duplicate is skipped.
	assert.Equal(t, "Another strong tale of legend here.", lore[0].Text)
	assert.Equal(t, "MVP", lore[0].Source)
	assert.Equal(t, "A neutral narrative line about the lair.", lore[1].Text)
}

func TestSelectLore_StableOrdering(t *testing.T) {
	build := func() []album.Line {
		return selectLore([]loreCandidate{
			{sentence: "B tale of legend one.", pageTitle: "A", strong: 2, length: 50},
			{sentence: "A tale of legend one.", pageTitle: "B", strong: 2, length: 50},
			{sentence: "C tale of legend two.", pageTitle: "A", strong: 2, length: 50},
		})
	}
	assert.Equal(t, build(), build())
}

func TestNameMatches_PluralAndSlotTolerance(t *testing.T) {
	assert.True(t, nameMatches("Poring", "Poring"))
	assert.True(t, nameMatches("Porings", "Poring"))
	assert.True(t, nameMatches("greatest general", "Greatest General"))
	assert.True(t, nameMatches(`Sword \[3\]`, "Sword [3]"), "escaped slotted names match the DB form")
	assert.False(t, nameMatches("Octopus Leg", "Poring"))
}

func TestExtractWikiPage_EscapedSlotMarkers(t *testing.T) {
	f, _, seenMon, seenItem, _ := newWalkState()
	md := `Bring the legendary [Sword \[3\]](item:1101) to the shrine, they say the forge is ancient.`
	extractWikiPage(f, map[string]map[string]int{}, seenMon, seenItem, map[string]bool{}, 5, "Guide", md, func(kind string, id int) string {
		if kind == "item" && id == 1101 {
			return "Sword [3]"
		}
		return ""
	})
	assert.True(t, seenItem[1101], "slotted marker counted for coverage")
	require.Len(t, f.ItemLore[1101], 1)
	assert.Contains(t, f.ItemLore[1101][0].sentence, "Sword [3]", "escaped brackets render literally")
}

func TestCleanSentence(t *testing.T) {
	got := cleanSentence("The **[Baphomet](monster:1039)** guards [prt_maze03](map:prt_maze03) forever.")
	assert.Equal(t, "The Baphomet guards prt_maze03 forever.", got)
}

func TestSplitSentences(t *testing.T) {
	assert.Equal(t,
		[]string{"One.", "Two!", "Three?", "x"},
		splitSentences("One. Two! Three? x"), "separator consumed, fragments kept")
}

func TestResolveMapDisplays_MajorityDeterministic(t *testing.T) {
	votes := map[string]map[string]int{
		"prt_fild08": {"Prontera Field 8": 3, "prt_fild08": 2},
		"mjolnir_08": {"Mjolnir Field 08": 1, "Mt. Mjolnir 8": 1}, // tie → lexicographic first
	}
	out := resolveMapDisplays(votes)
	assert.Equal(t, "Prontera Field 8", out["prt_fild08"])
	assert.Equal(t, "Mjolnir Field 08", out["mjolnir_08"])
	for i := 0; i < 20; i++ {
		assert.Equal(t, out, resolveMapDisplays(votes), "stable across map-iteration randomness")
	}
}

func TestPlaceCards_FromDisplayNamesWithNarrative(t *testing.T) {
	f, _, _, _, _ := newWalkState()
	md := "The tower is an ancient structure of legend, and its bells are said to be cursed."
	extractWikiPage(f, map[string]map[string]int{}, map[int]bool{}, map[int]bool{}, map[string]bool{}, 7, "Clock Tower", md, dbNamesFixture)

	// "Clock Tower" is the wiki display for c_tower → a place card,
	// with narrative from the same-titled page when it exists.
	f.MapDisplays = resolveMapDisplays(map[string]map[string]int{
		"c_tower": {"Clock Tower": 1},
	})
	cards := placeCards(f)
	require.Len(t, cards, 1)
	c := cards[0]
	assert.Equal(t, "place:Clock Tower", c.ID)
	assert.Equal(t, album.KindPlace, c.Kind)
	require.Len(t, c.Lines, 2)
	assert.Equal(t, album.TierCurated, c.Lines[0].Tier)
	assert.Equal(t, "maps: c_tower", c.Lines[0].Text)
	assert.Equal(t, album.TierNarrative, c.Lines[1].Tier)
	assert.Contains(t, c.Lines[1].Text, "cursed")
	assert.Equal(t, "Clock Tower", c.Lines[1].Source)
}

func TestPlaceCards_DisplayWithoutArticleDegrades(t *testing.T) {
	f, _, _, _, _ := newWalkState()
	f.MapDisplays = map[string]string{"prt_fild08": "Prontera Field 8"}
	cards := placeCards(f)
	require.Len(t, cards, 1)
	require.Len(t, cards[0].Lines, 1, "no same-titled page → curated-only card, never fabricated lore")
	assert.Equal(t, album.TierCurated, cards[0].Lines[0].Tier)
}

// fakeWikiClient stubs the walk.
type fakeWikiClient struct {
	pages []irowiki.Page
	md    map[string]*irowiki.PageMarkdown
}

func (c *fakeWikiClient) ListPages(_ context.Context, _ int, offset, limit int) ([]irowiki.Page, error) {
	if offset >= len(c.pages) {
		return nil, nil
	}
	end := offset + limit
	if end > len(c.pages) {
		end = len(c.pages)
	}
	return c.pages[offset:end], nil
}

func (c *fakeWikiClient) GetPageMarkdown(_ context.Context, title string) (*irowiki.PageMarkdown, error) {
	if md, ok := c.md[title]; ok {
		return md, nil
	}
	return nil, irowiki.ErrNotFound
}

func TestLoadWiki_WalkIncludesBodySections(t *testing.T) {
	client := &fakeWikiClient{
		pages: []irowiki.Page{
			{ID: 1, Namespace: 0, Title: "MVP"},
			{ID: 2, Namespace: 0, Title: "Redirect Page", IsRedirect: true},
		},
		md: map[string]*irowiki.PageMarkdown{
			"MVP": {
				PageID: 1, Title: "MVP",
				Markdown: "Lead section without markers.",
				Sections: []irowiki.MarkdownSection{
					{Anchor: "bosses", Heading: "Bosses", Markdown: "[Hatii](monster:1252) is located on [Lutie Field](map:xmas_fild01) (2 hours)"},
					{Anchor: "loot", Heading: "Loot", Markdown: "Bring 3 [Steel](item:999) for the forge of legend."},
				},
			},
		},
	}
	f, err := loadWiki(t.Context(), client, dbNamesFixture)
	require.NoError(t, err)
	// Body sections carry the markers; the lead alone would have
	// found none.
	require.Len(t, f.MonsterLore[1252], 1)
	assert.Equal(t, "Lutie Field", f.MapDisplays["xmas_fild01"])
	assert.Equal(t, 1, f.Referenced.MonsterIDs)
	assert.Equal(t, 1, f.Referenced.ItemIDs)
	assert.Equal(t, 1, f.Referenced.MapRefs)
}
