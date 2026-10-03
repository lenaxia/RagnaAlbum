// albumbuild wiki.go — the iRO Wiki slice of the binder: entity-
// annotated narrative sentences ([Name](monster:ID)/item:ID markers
// in the materialized pages_md) keyed by integer IDs, plus map
// display names and place pages. The wiki contributes narrative and
// curated lines ONLY — rAthena stays authoritative for every number;
// the DB-name join (marker ID must resolve to a real monster/item
// whose DB name matches the marker text) keeps mis-annotated markers
// ("[Octopus Leg](monster:2218)") out.
//
// Selection is deterministic: candidates are totally ordered by
// (narrative strength desc, second-person asc, sentence length asc,
// page title asc, page id asc, sentence asc); the top ≤2 from
// distinct pages win.
package main

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/lenaxia/RagnaAlbum/album"
	"github.com/lenaxia/iroWikiScraper/sdk/irowiki"
)

// wikiClient is the irowiki surface the binder consumes.
type wikiClient interface {
	ListPages(ctx context.Context, namespace int, offset, limit int) ([]irowiki.Page, error)
	GetPageMarkdown(ctx context.Context, title string) (*irowiki.PageMarkdown, error)
}

// nameMatcher resolves (kind, id) → the entity's DB name.
type nameMatcher func(kind string, id int) string

// wikiFacts is the extracted wiki slice.
type wikiFacts struct {
	MonsterLore map[int][]loreCandidate
	ItemLore    map[int][]loreCandidate
	MapDisplays map[string]string // map name → majority display name
	// PlacePages maps a place title → its narrative candidates and
	// the distinct maps its page references (place-card material).
	PlacePages map[string]*placePage
	// Referenced counts DISTINCT ids/markers seen anywhere in the
	// walk (coverage validation against the archive's measured
	// shape), regardless of admission into cards.
	Referenced referencedCounts
}

type referencedCounts struct {
	MonsterIDs int
	ItemIDs    int
	MapRefs    int // distinct maps referenced by (map:) markers
}

type placePage struct {
	title      string
	pageID     int64
	maps       map[string]bool
	candidates []loreCandidate
}

// loreCandidate is one qualifying sentence with its ranking key
// precomputed. Lower rank sorts first.
type loreCandidate struct {
	sentence   string
	pageTitle  string
	pageID     int64
	strong     int // 2 = strong-lore keyword, 1 = narrative keyword
	secondPers int // 1 = contains you/your
	length     int
}

func (c loreCandidate) less(o loreCandidate) bool {
	if c.strong != o.strong {
		return c.strong > o.strong
	}
	if c.secondPers != o.secondPers {
		return c.secondPers < o.secondPers
	}
	if c.length != o.length {
		return c.length < o.length
	}
	if c.pageTitle != o.pageTitle {
		return c.pageTitle < o.pageTitle
	}
	if c.pageID != o.pageID {
		return c.pageID < o.pageID
	}
	return c.sentence < o.sentence
}

var (
	// markerRe captures entity-annotated links: [Text](kind:ID).
	// Text may contain markdown-escaped brackets ([Sword \[3\]](item:1101)),
	// so the name group admits backslash-escaped characters.
	markerRe = regexp.MustCompile(`\[((?:[^\]\\]|\\.)+)\]\((monster|item):(\d+)\)`)
	// anyLinkRe strips any markdown link down to its text.
	anyLinkRe = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	// mapMarkerRe captures map references: [Display](map:name).
	mapMarkerRe = regexp.MustCompile(`\[([^\]]+)\]\(map:([a-z0-9_]+)\)`)
	// narrativeRe is the broad narrative gate.
	narrativeRe = regexp.MustCompile(`(?i)\b(said to|legend|legendary|ancient|rumor|rumoured|rumored|stories|tale|tales|myth|folklore|history|war|curse|cursed|sacred|blessed|sealed|forbidden|kingdom|empire|god|goddess|shrine|prophecy|guardian|protect|created|born|forge|forged|crafted|descendant|hero|saint|holy|evil|demon|dragon|immortal|eternal|forgotten|located|dungeon|temple|cave|forest|desert|island|tower|castle|ruins|village|city of|wanders|roams|inhabit|nest|lair|home to|found in|deadly|dangerous|fierce|hostile|aggressive|peaceful|friendly|carries|treasure|MVP|boss)\b`)
	// strongLoreRe marks the grandiose-lore tier.
	strongLoreRe = regexp.MustCompile(`(?i)\b(said to|legend|legendary|ancient|rumor|rumoured|rumored|tale|tales|myth|folklore|curse|cursed|sacred|sealed|forbidden|prophecy|guardian|created|born|forged|crafted|hero|saint|holy|evil|demon|dragon|immortal|forgotten|wanders|roams|lair|treasure)\b`)
	// secondPersonRe detects quest-instruction voice.
	secondPersonRe = regexp.MustCompile(`(?i)\b(you|your|you'll)\b`)
	// boldRe strips markdown emphasis.
	boldRe = regexp.MustCompile(`\*+`)
	// linePrefixRe strips list bullets and "1."/"2c)" enumerators.
	linePrefixRe = regexp.MustCompile(`^(?:[-*]\s+|\d+[a-z]?[.)]\s+)`)
)

const (
	loreMinLen = 40
	loreMaxLen = 280
)

func newWikiFacts() *wikiFacts {
	return &wikiFacts{
		MonsterLore: map[int][]loreCandidate{},
		ItemLore:    map[int][]loreCandidate{},
		MapDisplays: map[string]string{},
		PlacePages:  map[string]*placePage{},
	}
}

// loadWiki walks the archive's main-namespace materialized markdown
// and extracts lore candidates, map display votes, place pages, and
// the coverage counts.
func loadWiki(ctx context.Context, client wikiClient, dbNames nameMatcher) (*wikiFacts, error) {
	f := newWikiFacts()
	mapVotes := map[string]map[string]int{}
	seenMonster := map[int]bool{}
	seenItem := map[int]bool{}
	seenMaps := map[string]bool{}

	const pageSize = 500
	for offset := 0; ; offset += pageSize {
		pages, err := client.ListPages(ctx, 0, offset, pageSize)
		if err != nil {
			return nil, fmt.Errorf("albumbuild: wiki list: %w", err)
		}
		if len(pages) == 0 {
			break
		}
		for _, p := range pages {
			if p.IsRedirect {
				continue
			}
			md, err := client.GetPageMarkdown(ctx, p.Title)
			if err != nil {
				if err == irowiki.ErrNotFound || err == irowiki.ErrNotMaterialized {
					continue
				}
				return nil, fmt.Errorf("albumbuild: wiki markdown %q: %w", p.Title, err)
			}
			if md == nil {
				continue
			}
			// The lead section rides .Markdown; every body section
			// rides .Sections (marker-heavy tables and quest bodies
			// live there — walking only the lead drops most of the
			// archive's entity annotations).
			extractWikiPage(f, mapVotes, seenMonster, seenItem, seenMaps, md.PageID, p.Title, md.Markdown, dbNames)
			for _, sec := range md.Sections {
				extractWikiPage(f, mapVotes, seenMonster, seenItem, seenMaps, md.PageID, p.Title, sec.Markdown, dbNames)
			}
		}
		if len(pages) < pageSize {
			break
		}
	}
	f.MapDisplays = resolveMapDisplays(mapVotes)
	f.Referenced = referencedCounts{
		MonsterIDs: len(seenMonster),
		ItemIDs:    len(seenItem),
		MapRefs:    len(seenMaps),
	}
	return f, nil
}

// extractWikiPage is the pure per-page extraction. Exposed for unit
// tests.
func extractWikiPage(f *wikiFacts, mapVotes map[string]map[string]int, seenMonster, seenItem map[int]bool, seenMaps map[string]bool, pageID int64, pageTitle, markdown string, dbNames nameMatcher) {
	var pageMaps map[string]bool

	// Coverage counting first: EVERY marker the page carries counts as
	// a reference, regardless of line filters or admission — these
	// numbers validate the walk against the archive's measured shape
	// (full-page marker semantics).
	for _, m := range markerRe.FindAllStringSubmatch(markdown, -1) {
		if id, err := strconv.Atoi(m[3]); err == nil {
			switch m[2] {
			case "monster":
				seenMonster[id] = true
			case "item":
				seenItem[id] = true
			}
		}
	}
	for _, m := range mapMarkerRe.FindAllStringSubmatch(markdown, -1) {
		seenMaps[m[2]] = true
	}

	for _, rawLine := range strings.Split(markdown, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		switch line[0] {
		case '|', '#', '<', '!', '{', '}':
			continue
		}
		line = linePrefixRe.ReplaceAllString(line, "")
		for _, mv := range mapNamesIn(line) {
			if _, ok := mapVotes[mv.mp]; !ok {
				mapVotes[mv.mp] = map[string]int{}
			}
			mapVotes[mv.mp][mv.name]++
			if pageMaps == nil {
				pageMaps = map[string]bool{}
			}
			pageMaps[mv.mp] = true
		}
		for _, sent := range splitSentences(line) {
			sent = strings.TrimSpace(sent)
			if len(sent) < loreMinLen || len(sent) > loreMaxLen {
				continue
			}
			if !narrativeRe.MatchString(sent) {
				continue
			}
			collectMarkers(f, pageID, pageTitle, sent, dbNames)
			collectPlaceCandidate(f, pageID, pageTitle, sent)
		}
	}
	if pageMaps != nil {
		pp, ok := f.PlacePages[pageTitle]
		if !ok {
			pp = &placePage{title: pageTitle, pageID: pageID, maps: map[string]bool{}}
			f.PlacePages[pageTitle] = pp
		}
		for m := range pageMaps {
			pp.maps[m] = true
		}
	}
}

// mapNamesIn yields (display, mapname) pairs for map markers with a
// plausible display (not the mapname itself, not placeholders).
func mapNamesIn(line string) []struct {
	name string
	mp   string
} {
	var out []struct {
		name string
		mp   string
	}
	for _, m := range mapMarkerRe.FindAllStringSubmatch(line, -1) {
		name, mp := strings.TrimSpace(m[1]), m[2]
		if name == "" || name == mp || name == "Map" || name == "map" {
			continue
		}
		out = append(out, struct {
			name string
			mp   string
		}{name, mp})
	}
	return out
}

// collectMarkers finds entity markers whose ID resolves to a DB
// entity with a matching display name (exact, or simple plural) and
// files the sentence as a candidate for that entity.
func collectMarkers(f *wikiFacts, pageID int64, pageTitle, sent string, dbNames nameMatcher) {
	for _, m := range markerRe.FindAllStringSubmatch(sent, -1) {
		text, kind := boldRe.ReplaceAllString(m[1], ""), m[2]
		id, err := strconv.Atoi(m[3])
		if err != nil {
			continue
		}
		dbName := dbNames(kind, id)
		if dbName == "" || !nameMatches(text, dbName) {
			continue
		}
		cand := loreCandidate{
			sentence:   cleanSentence(sent),
			pageTitle:  pageTitle,
			pageID:     pageID,
			strong:     1,
			secondPers: 0,
			length:     len(sent),
		}
		if strongLoreRe.MatchString(sent) {
			cand.strong = 2
		}
		if secondPersonRe.MatchString(sent) {
			cand.secondPers = 1
		}
		switch kind {
		case "monster":
			f.MonsterLore[id] = append(f.MonsterLore[id], cand)
		case "item":
			f.ItemLore[id] = append(f.ItemLore[id], cand)
		}
	}
}

// collectPlaceCandidate files a narrative sentence for the place-page
// keyed by its title (place cards draw from these).
func collectPlaceCandidate(f *wikiFacts, pageID int64, pageTitle, sent string) {
	cand := loreCandidate{
		sentence:  cleanSentence(sent),
		pageTitle: pageTitle,
		pageID:    pageID,
		strong:    1,
		length:    len(sent),
	}
	if strongLoreRe.MatchString(sent) {
		cand.strong = 2
	}
	if secondPersonRe.MatchString(sent) {
		cand.secondPers = 1
	}
	pp, ok := f.PlacePages[pageTitle]
	if !ok {
		pp = &placePage{title: pageTitle, pageID: pageID, maps: map[string]bool{}}
		f.PlacePages[pageTitle] = pp
	}
	if len(pp.candidates) < 64 { // bound per-page memory, not selection
		pp.candidates = append(pp.candidates, cand)
	}
}

// nameMatches accepts the DB name or its simple plural.
func nameMatches(markerText, dbName string) bool {
	t := normalizeWikiName(markerText)
	d := normalizeWikiName(dbName)
	return t == d || t == d+"s"
}

func normalizeWikiName(s string) string {
	s = unescapeBrackets(strings.ToLower(strings.TrimSpace(s)))
	return strings.Join(strings.Fields(s), " ")
}

// unescapeBrackets renders markdown-escaped brackets literally
// ("Sword \[3\]" → "Sword [3]", the item DB's display form).
func unescapeBrackets(s string) string {
	s = strings.ReplaceAll(s, `\[`, "[")
	return strings.ReplaceAll(s, `\]`, "]")
}

// splitSentences splits a line after sentence punctuation followed by
// one whitespace separator (RE2 has no lookbehind, so this is
// hand-rolled). The separator is consumed, not kept.
func splitSentences(line string) []string {
	var out []string
	start := 0
	for i := 0; i < len(line)-1; i++ {
		c := line[i]
		if (c == '.' || c == '!' || c == '?') && (line[i+1] == ' ' || line[i+1] == '\t') {
			out = append(out, line[start:i+1])
			start = i + 2
		}
	}
	if start < len(line) {
		out = append(out, line[start:])
	}
	return out
}

// cleanSentence reduces a markdown sentence to plain prose: link
// markers keep their text, emphasis is stripped, escaped brackets
// render literally, whitespace collapses.
func cleanSentence(sent string) string {
	s := anyLinkRe.ReplaceAllString(sent, "$1")
	s = boldRe.ReplaceAllString(s, "")
	s = unescapeBrackets(s)
	return strings.Join(strings.Fields(s), " ")
}

// resolveMapDisplays picks each map's majority display name; ties
// break lexicographically on the display name (a stable choice across
// builds).
func resolveMapDisplays(votes map[string]map[string]int) map[string]string {
	out := make(map[string]string, len(votes))
	for mp, names := range votes {
		sorted := make([]string, 0, len(names))
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		best, bestCount := "", -1
		for _, n := range sorted {
			if names[n] > bestCount {
				best, bestCount = n, names[n]
			}
		}
		if best != "" {
			out[mp] = best
		}
	}
	return out
}

// selectLore picks the top ≤album.MaxNarrative candidates for one
// entity, at most one per page, deduplicated by sentence.
func selectLore(cands []loreCandidate) []album.Line {
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].less(cands[j]) })
	seenPage := map[string]bool{}
	seenSent := map[string]bool{}
	out := make([]album.Line, 0, album.MaxNarrative)
	for _, c := range cands {
		if len(out) >= album.MaxNarrative {
			break
		}
		if c.sentence == "" || seenSent[c.sentence] {
			continue
		}
		// Record the sentence even when its page is already used: a
		// mirror-page copy of an already-seen sentence must never
		// re-enter via a later page.
		seenSent[c.sentence] = true
		if seenPage[c.pageTitle] {
			continue
		}
		seenPage[c.pageTitle] = true
		out = append(out, album.Line{Tier: album.TierNarrative, Text: c.sentence, Source: c.pageTitle})
	}
	return out
}

// placeCards binds place cards: one per wiki map display name — the
// wiki's place vocabulary ("Glast Heim", "Prontera Field 8"). Curated
// line: the maps sharing that display (sorted, capped). Narrative:
// top ≤2 candidates from the same-titled page when the wiki carries a
// place article under that exact name.
func placeCards(wf *wikiFacts) []album.Card {
	mapsByDisplay := map[string]map[string]bool{}
	for mp, disp := range wf.MapDisplays {
		if _, ok := mapsByDisplay[disp]; !ok {
			mapsByDisplay[disp] = map[string]bool{}
		}
		mapsByDisplay[disp][mp] = true
	}
	out := make([]album.Card, 0, len(mapsByDisplay))
	for disp, mapSet := range mapsByDisplay {
		maps := make([]string, 0, len(mapSet))
		for m := range mapSet {
			maps = append(maps, m)
		}
		sort.Strings(maps)
		if len(maps) > maxPlaceMaps {
			maps = maps[:maxPlaceMaps]
		}
		c := album.Card{
			ID:   album.KindPlace + ":" + disp,
			Kind: album.KindPlace,
			Name: disp,
			Lines: []album.Line{{
				Tier: album.TierCurated,
				Text: "maps: " + strings.Join(maps, ", "),
			}},
		}
		if pp, ok := wf.PlacePages[disp]; ok {
			c.Lines = append(c.Lines, selectLore(pp.candidates)...)
		}
		if len(c.Lines) > album.MaxLinesPerCard {
			c.Lines = c.Lines[:album.MaxLinesPerCard]
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

const maxPlaceMaps = 8
