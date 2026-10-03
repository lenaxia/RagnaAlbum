// albumbuild assemble.go — binds the rAthena and wiki slices into
// the album and renders it deterministically.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lenaxia/RagnaAlbum/album"
	ragnadata "github.com/lenaxia/ragnaDataSDK/pkg/ragnadata"
)

// bind builds the album: cards from the rAthena slice (authoritative
// lines), narrative/curated lines joined from the wiki slice.
func bind(rf *rathenaFacts, wf *wikiFacts, header albumHeader) (*album.Album, error) {
	itemNames := itemNameIndex(rf.Items)

	// Sort by ID regardless of source iteration order: the SDK's
	// ordering is an implementation detail, the album's is not.
	mons := make([]*ragnadata.MonsterInfo, 0, len(rf.Monsters))
	mons = append(mons, rf.Monsters...)
	sort.SliceStable(mons, func(i, j int) bool { return mons[i].ID < mons[j].ID })
	items := make([]*ragnadata.ItemInfo, 0, len(rf.Items))
	items = append(items, rf.Items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })

	// Item inclusion: priced ∪ dropped ∪ wiki-narrative-claimed.
	loreItems := make(map[int]bool, len(wf.ItemLore))
	for id := range wf.ItemLore {
		loreItems[id] = true
	}
	dropped := droppedItemIDs(mons)

	a := &album.Album{
		SchemaVersion: album.SchemaVersion,
		Description:   header.description,
		Sources:       header.sources,
		Built:         header.built,
		Converter:     header.converter,
		SourceHash:    header.sourceHash,
	}

	// Monsters: every named monster.
	for _, m := range mons {
		if m == nil || m.Name == "" || isWeirdName(m.Name) {
			continue
		}
		card := monsterCard(m, itemNames)
		card.Lines = append(card.Lines, selectLore(wf.MonsterLore[m.ID])...)
		if len(card.Lines) > album.MaxLinesPerCard {
			card.Lines = card.Lines[:album.MaxLinesPerCard]
		}
		a.Cards = append(a.Cards, card)
	}
	// Items: the conversational-relevant subset.
	for _, it := range items {
		if it == nil || it.Name == "" || isWeirdName(it.Name) {
			continue
		}
		if !itemIncluded(it, dropped, loreItems) {
			continue
		}
		card := itemCard(it)
		card.Lines = append(card.Lines, selectLore(wf.ItemLore[it.ID])...)
		if len(card.Lines) > album.MaxLinesPerCard {
			card.Lines = card.Lines[:album.MaxLinesPerCard]
		}
		if len(card.Lines) == 0 {
			// Included but fact-less (no type, price, or lore): no
			// card — the album never carries an empty card.
			continue
		}
		a.Cards = append(a.Cards, card)
	}
	// Maps: every map with spawns or an included NPC.
	npcs := npcRows(rf.NPCs)
	for mapName, facts := range mapFacts(mons, npcs) {
		card, ok := mapCard(mapName, facts, wf.MapDisplays[mapName])
		if ok {
			a.Cards = append(a.Cards, card)
		}
	}
	// NPCs: shop staff + kafras.
	a.Cards = append(a.Cards, npcCards(npcs)...)
	// Places: wiki place articles.
	a.Cards = append(a.Cards, placeCards(wf)...)

	album.SortCards(a.Cards)

	a.Counts = album.Counts{Cards: len(a.Cards)}
	for _, c := range a.Cards {
		switch c.Kind {
		case album.KindMonster:
			a.Counts.Monsters++
		case album.KindItem:
			a.Counts.Items++
		case album.KindMap:
			a.Counts.Maps++
		case album.KindNPC:
			a.Counts.NPCs++
		case album.KindPlace:
			a.Counts.Places++
		}
		for _, l := range c.Lines {
			if l.Tier == album.TierNarrative {
				a.Counts.Narrative++
			}
			if l.Tier == album.TierCurated {
				a.Counts.Curated++
			}
		}
	}
	a.Counts.Referenced.MonsterIDs = wf.Referenced.MonsterIDs
	a.Counts.Referenced.ItemIDs = wf.Referenced.ItemIDs
	a.Counts.Referenced.MapRefs = wf.Referenced.MapRefs

	if err := album.Validate(a); err != nil {
		return nil, fmt.Errorf("albumbuild: bound album invalid: %w", err)
	}
	return a, nil
}

type albumHeader struct {
	description string
	sources     album.Sources
	built       string
	converter   string
	sourceHash  string
}

// marshalAlbum renders the album as compact JSON. encoding/json
// emits struct fields in declaration order and every slice is sorted
// at bind time, so identical inputs yield identical bytes.
func marshalAlbum(a *album.Album) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(a); err != nil {
		return nil, fmt.Errorf("albumbuild: marshal: %w", err)
	}
	return buf.Bytes(), nil
}
