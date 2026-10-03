// albumbuild — offline binder for the album artifact
// (album/album.json in this module).
//
// Run manually per rAthena bump / monthly wiki release:
//
//	go run ./cmd/albumbuild \
//	  -rathena-db /path/to/rathena/db \
//	  -rathena-tag packetver20200401-renewal-YYYYMMDD \
//	  -wiki-db /path/to/irowiki.db \
//	  -wiki-release 2026.08 \
//	  -date 2026-10-02 \
//	  -out album/album.json
//
// Determinism: identical inputs + flags bind a byte-identical album
// (the header date comes from -date, never the wall clock). Two
// consecutive binds must diff clean — that is the release checklist.
//
// Validation:
//
//	  -check                  re-bind and require byte equality with the
//		                          checked-in album (the determinism contract
//		                          made executable)
//	  -validate-coverage      additionally require the wiki-referenced
//	                         coverage counts to meet the archive's
//	                         measured targets (default: v2026.08 numbers)
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/lenaxia/RagnaAlbum/album"
	"github.com/lenaxia/iroWikiScraper/sdk/irowiki"
	ragnadata "github.com/lenaxia/ragnaDataSDK/pkg/ragnadata"
)

// converterVersion bumps when the binding rules change in a way that
// alters output (new caps, new filters) — the header records it so an
// album's lineage is readable at a glance.
const converterVersion = "albumbuild-1"

// coverageTargets are the v2026.08 archive's measured reference
// counts (distinct ids/markers the walk must see). A release against
// a new archive passes -monster-refs/-item-refs/-map-refs explicitly.
const (
	defaultMonsterRefs = 866
	defaultItemRefs    = 3764
	defaultMapRefs     = 359
)

func main() {
	rathenaDB := flag.String("rathena-db", "", "path to the pinned rAthena db dir (the SDK's RathenaDBPath)")
	rathenaTag := flag.String("rathena-tag", "", "provenance: the pinned rathena image tag")
	wikiDB := flag.String("wiki-db", "", "path to the materialized irowiki SQLite archive")
	wikiRelease := flag.String("wiki-release", "", "provenance: the wiki archive release (e.g. 2026.08)")
	date := flag.String("date", "", "provenance: build date YYYY-MM-DD (explicit for deterministic rebuilds)")
	out := flag.String("out", "album/album.json", "output album path")
	check := flag.Bool("check", false, "validation mode: re-bind and require byte equality with the checked-in album")
	validateCov := flag.Bool("validate-coverage", false, "require wiki-referenced coverage to meet the measured targets")
	monsterRefs := flag.Int("monster-refs", defaultMonsterRefs, "coverage target: distinct monster ids referenced")
	itemRefs := flag.Int("item-refs", defaultItemRefs, "coverage target: distinct item ids referenced")
	mapRefs := flag.Int("map-refs", defaultMapRefs, "coverage target: distinct map refs")
	flag.Parse()

	if err := run(runArgs{
		rathenaDB: *rathenaDB, rathenaTag: *rathenaTag,
		wikiDB: *wikiDB, wikiRelease: *wikiRelease,
		date: *date, out: *out,
		check: *check, validateCov: *validateCov,
		monsterRefs: *monsterRefs, itemRefs: *itemRefs, mapRefs: *mapRefs,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "albumbuild:", err)
		os.Exit(1)
	}
}

type runArgs struct {
	rathenaDB, rathenaTag, wikiDB, wikiRelease, date, out string
	check, validateCov                                    bool
	monsterRefs, itemRefs, mapRefs                        int
}

func run(args runArgs) error {
	if args.rathenaDB == "" || args.wikiDB == "" {
		return fmt.Errorf("-rathena-db and -wiki-db are required")
	}
	if !args.check && args.date == "" {
		return fmt.Errorf("-date is required for a bind (deterministic provenance)")
	}

	sdk, err := ragnadata.NewSDK(ragnadata.Config{
		RathenaDBPath: args.rathenaDB,
		Mode:          ragnadata.FormulaModeRenewal,
		Region:        "iro",
	})
	if err != nil {
		return fmt.Errorf("rathena sdk: %w", err)
	}
	wiki, err := irowiki.OpenSQLite(args.wikiDB)
	if err != nil {
		return fmt.Errorf("wiki open: %w", err)
	}
	defer wiki.Close()

	a, raw, err := derive(sdk, wiki, albumHeader{
		description: albumDescription,
		sources: album.Sources{
			RathenaTag:  args.rathenaTag,
			RathenaPath: args.rathenaDB,
			WikiRelease: args.wikiRelease,
			WikiPath:    args.wikiDB,
		},
		built:      args.date,
		converter:  converterVersion,
		sourceHash: sourceHash(),
	})
	if err != nil {
		return err
	}

	if args.validateCov {
		if err := checkCoverage(a, args); err != nil {
			return err
		}
	}
	if args.check {
		want, err := os.ReadFile(args.out)
		if err != nil {
			return fmt.Errorf("check: read %s: %w", args.out, err)
		}
		if string(raw) != string(want) {
			return fmt.Errorf("check: %s is stale (fresh bind differs) — rebind with matching flags", args.out)
		}
		fmt.Printf("albumbuild: check OK — %s byte-identical to a fresh bind\n", args.out)
		return nil
	}
	if err := os.WriteFile(args.out, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", args.out, err)
	}
	fmt.Printf("albumbuild: bound %s (%d bytes; monsters=%d items=%d maps=%d npcs=%d places=%d narrative=%d curated=%d)\n",
		args.out, len(raw), a.Counts.Monsters, a.Counts.Items, a.Counts.Maps,
		a.Counts.NPCs, a.Counts.Places, a.Counts.Narrative, a.Counts.Curated)
	return nil
}

const albumDescription = "RagnaAlbum: per-entity fact cards bound from the pinned rAthena DB (authoritative) and the iRO Wiki archive (narrative/curated) for deterministic injection."

// derive runs the full pipeline (shared by bind and check modes).
func derive(sdk *ragnadata.SDK, wiki irowiki.Client, header albumHeader) (*album.Album, []byte, error) {
	rf, err := loadRathena(sdk)
	if err != nil {
		return nil, nil, err
	}
	dbNames := func(kind string, id int) string {
		switch kind {
		case "monster":
			if m, err := sdk.GetMonsterByID(id, ragnadata.FormulaModeRenewal); err == nil && m != nil {
				return m.Name
			}
		case "item":
			if it, err := sdk.GetItemByID(id, ragnadata.FormulaModeRenewal); err == nil && it != nil {
				return it.Name
			}
		}
		return ""
	}
	wf, err := loadWiki(context.Background(), wiki, dbNames)
	if err != nil {
		return nil, nil, err
	}
	a, err := bind(rf, wf, header)
	if err != nil {
		return nil, nil, err
	}
	raw, err := marshalAlbum(a)
	if err != nil {
		return nil, nil, err
	}
	// Load-path round trip: the bytes we hand consumers must parse
	// with the shipping loader, not just serialize.
	if _, err := album.Load(raw); err != nil {
		return nil, nil, fmt.Errorf("albumbuild: round-trip: %w", err)
	}
	return a, raw, nil
}

// checkCoverage validates the walk against the archive's measured
// reference counts (the orchestrator's build-validation targets).
func checkCoverage(a *album.Album, args runArgs) error {
	r := a.Counts.Referenced
	if r.MonsterIDs < args.monsterRefs {
		return fmt.Errorf("coverage: wiki monster refs %d < target %d", r.MonsterIDs, args.monsterRefs)
	}
	if r.ItemIDs < args.itemRefs {
		return fmt.Errorf("coverage: wiki item refs %d < target %d", r.ItemIDs, args.itemRefs)
	}
	if r.MapRefs < args.mapRefs {
		return fmt.Errorf("coverage: wiki map refs %d < target %d", r.MapRefs, args.mapRefs)
	}
	fmt.Printf("albumbuild: coverage OK — monsterRefs=%d (>= %d), itemRefs=%d (>= %d), mapRefs=%d (>= %d)\n",
		r.MonsterIDs, args.monsterRefs, r.ItemIDs, args.itemRefs, r.MapRefs, args.mapRefs)
	return nil
}

// sourceHash digests the binder's own sources at run time so the
// header identifies the converter precisely (ADR-001: converter
// hash). Test files are excluded — they do not ship in the binary.
func sourceHash() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "unknown"
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "unknown"
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return "unknown"
		}
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(raw)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
