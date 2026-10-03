package album

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefault_ArtifactValid(t *testing.T) {
	a, err := Default()
	require.NoError(t, err)

	// The release album's conversational-relevant scale: pinned
	// loosely (exact counts live in the header + release notes).
	assert.NotEmpty(t, a.Cards)
	assert.NotEmpty(t, a.Sources.RathenaTag)
	assert.NotEmpty(t, a.Sources.WikiRelease)
	assert.NotEmpty(t, a.Built)
	assert.NotEmpty(t, a.SourceHash)
	assert.Equal(t, SchemaVersion, a.SchemaVersion)
}

func TestDefault_EmbeddedMatchesFile(t *testing.T) {
	raw, err := os.ReadFile("album.json")
	require.NoError(t, err)
	fromFile, err := Load(raw)
	require.NoError(t, err)
	def, err := Default()
	require.NoError(t, err)
	require.Equal(t, def.Counts, fromFile.Counts)
	require.Len(t, fromFile.Cards, len(def.Cards))
}

// spotPins: stable, famous entities whose card facts are pinned — the
// drift tripwire between the binder's rendering and every consumer.
func TestDefault_SpotPins(t *testing.T) {
	a, err := Default()
	require.NoError(t, err)

	fabre, ok := a.MonsterCard(1007)
	require.True(t, ok, "Fabre card")
	assert.Equal(t, "Fabre", fabre.Name)
	joined := ""
	for _, l := range fabre.Lines {
		joined += l.Text + "\n"
	}
	assert.Contains(t, joined, "Lv6")
	assert.Contains(t, joined, "Fluff 70%")
	assert.Contains(t, joined, "Fabre Card")

	red, ok := a.ItemCard(501)
	require.True(t, ok, "Red Potion card")
	joined = ""
	for _, l := range red.Lines {
		joined += l.Text + "\n"
	}
	assert.Contains(t, joined, "NPC buy 10z")

	_, ok = a.MapCard("prt_fild08")
	assert.True(t, ok, "prt_fild08 card")
}

func TestLoad_SchemaVersionRefusal(t *testing.T) {
	raw, err := os.ReadFile("album.json")
	require.NoError(t, err)

	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	require.NoError(t, json.Unmarshal(raw, &probe))
	if probe.SchemaVersion != SchemaVersion {
		t.Skipf("fixture album is schema %d, this loader %d — refusal covered by the synthetic case", probe.SchemaVersion, SchemaVersion)
	}

	bumped := strings.Replace(string(raw), `"schema_version":1`, `"schema_version":2`, 1)
	if bumped == string(raw) {
		t.Fatal("could not bump schema_version in fixture")
	}
	_, err = Load([]byte(bumped))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema version 2, want 1")
}

func TestGet_DeterministicAndFailOpen(t *testing.T) {
	a, err := Default()
	require.NoError(t, err)

	c1, ok1 := a.Get("monster:1007")
	require.True(t, ok1)
	for i := 0; i < 10; i++ {
		c2, ok2 := a.Get("monster:1007")
		require.True(t, ok2)
		assert.Equal(t, c1, c2, "Get is deterministic")
	}
	_, ok := a.Get("monster:999999999")
	assert.False(t, ok, "unknown id fails open")
	_, ok = a.Get("")
	assert.False(t, ok)
}

func TestGet_NilAlbumFailsOpen(t *testing.T) {
	var a *Album
	_, ok := a.Get("monster:1007")
	assert.False(t, ok)
	_, ok = a.MonsterCard(1007)
	assert.False(t, ok)
	_, ok = a.MapCard("prontera")
	assert.False(t, ok)
	_, ok = a.ByName("Fabre")
	assert.False(t, ok)
	assert.Nil(t, a.CardIDs())
}

func TestByName_NormalizedAndMonsterWins(t *testing.T) {
	a, err := Default()
	require.NoError(t, err)

	c, ok := a.ByName("  fabre ")
	require.True(t, ok)
	assert.Equal(t, KindMonster, c.Kind)

	c, ok = a.ByName("RED   POTION")
	require.True(t, ok)
	assert.Equal(t, KindItem, c.Kind)

	_, ok = a.ByName("no such entity")
	assert.False(t, ok)
}

func TestCardIDs_CanonicalOrder(t *testing.T) {
	a, err := Default()
	require.NoError(t, err)
	ids := a.CardIDs()
	require.NotEmpty(t, ids)
	rank := map[string]int{KindMonster: 0, KindItem: 1, KindMap: 2, KindNPC: 3, KindPlace: 4}
	last := -1
	for _, id := range ids {
		var kind string
		for i := 0; i < len(id); i++ {
			if id[i] == ':' {
				kind = id[:i]
				break
			}
		}
		r, known := rank[kind]
		require.True(t, known, "unknown kind in id %q", id)
		assert.GreaterOrEqual(t, r, last, "kind blocks out of order at %q", id)
		last = r
	}
}

func TestValidate_Rejections(t *testing.T) {
	good := func() *Album {
		a := &Album{
			SchemaVersion: SchemaVersion,
			Cards: []Card{
				{ID: "monster:1", Kind: KindMonster, Name: "A", Lines: []Line{{Tier: TierAuthoritative, Text: "Lv1"}}},
				{ID: "item:2", Kind: KindItem, Name: "B", Lines: []Line{{Tier: TierAuthoritative, Text: "buy 1z"}}},
			},
		}
		a.Counts = Counts{Cards: 2, Monsters: 1, Items: 1}
		require.NoError(t, Validate(a))
		return a
	}

	badCounts := good()
	badCounts.Counts.Cards = 3
	assert.Error(t, Validate(badCounts))

	noLines := good()
	noLines.Cards[0].Lines = nil
	assert.Error(t, Validate(noLines))

	overCap := good()
	overCap.Cards[0].Lines = make([]Line, MaxLinesPerCard+1)
	for i := range overCap.Cards[0].Lines {
		overCap.Cards[0].Lines[i] = Line{Tier: TierAuthoritative, Text: "x"}
	}
	assert.Error(t, Validate(overCap))

	badTier := good()
	badTier.Cards[0].Lines[0].Tier = "rare"
	assert.Error(t, Validate(badTier), "no rarity/trust conflation — tiers are the enum")

	emptyText := good()
	emptyText.Cards[0].Lines[0].Text = ""
	assert.Error(t, Validate(emptyText))

	narrNoSrc := good()
	narrNoSrc.Cards[0].Lines[0].Tier = TierNarrative
	assert.Error(t, Validate(narrNoSrc), "narrative lines carry their page")

	authWithSrc := good()
	authWithSrc.Cards[0].Lines[0].Source = "page"
	assert.Error(t, Validate(authWithSrc))

	overNarrative := good()
	nl := make([]Line, MaxNarrative+1)
	for i := range nl {
		nl[i] = Line{Tier: TierNarrative, Text: "t", Source: "p"}
	}
	overNarrative.Cards[0].Lines = nl
	assert.Error(t, Validate(overNarrative))

	dup := good()
	dup.Cards = append(dup.Cards, Card{ID: "monster:1", Kind: KindMonster, Name: "A", Lines: []Line{{Tier: TierAuthoritative, Text: "Lv1"}}})
	dup.Counts.Cards = 3
	dup.Counts.Monsters = 2
	assert.Error(t, Validate(dup), "duplicate id")

	misKinded := good()
	misKinded.Cards[0].ID = "item:1"
	assert.Error(t, Validate(misKinded))

	unsorted := good()
	unsorted.Cards[0], unsorted.Cards[1] = unsorted.Cards[1], unsorted.Cards[0]
	assert.Error(t, Validate(unsorted))
}

func TestLoad_RejectsGarbage(t *testing.T) {
	_, err := Load([]byte("not json"))
	assert.Error(t, err)
	_, err = Load([]byte(`{"schema_version": 999, "cards": []}`))
	assert.Error(t, err)
}

func TestLoad_RejectsInvalidBody(t *testing.T) {
	// Right schema version, wrong body: header/card mismatch.
	raw := []byte(`{"schema_version": 1, "counts": {"cards": 5}, "cards": []}`)
	_, err := Load(raw)
	assert.Error(t, err)
}
