package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/golden"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
)

// heldPartTestValue is a value whose one part, "snapshot", is complete in
// the snapshot SnapshotID, which it holds as core's values hold theirs: its
// record links the snapshot under the part's role, its owner links name it,
// and its codec reads the part's snapshot from the record's links. A File's
// when File is set, its path there, and a directory's otherwise; with no
// SnapshotID it has no part.
type heldPartTestValue struct {
	SnapshotID string `json:"snapshotID,omitempty"`
	File       string `json:"file,omitempty"`
}

func (*heldPartTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "HeldPartTestValue", NonNull: true}
}

func (v *heldPartTestValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	if v.SnapshotID == "" {
		return nil
	}
	return []PersistedSnapshotRefLink{{RefKey: v.SnapshotID, Role: "snapshot"}}
}

func (v *heldPartTestValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(v)
	return PersistedObjectEncoding{JSON: raw, SnapshotLinks: v.PersistedSnapshotRefLinks()}, err
}

func (*heldPartTestValue) DecodePersistedObject(_ context.Context, _ *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	v := new(heldPartTestValue)
	return v, json.Unmarshal(raw, v)
}

func (*heldPartTestValue) PersistedOutputRevision() (OutputRevision, error) { return 0, nil }

type heldPartTestCodec struct{}

func (heldPartTestCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}

func (heldPartTestCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }

func (heldPartTestCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	var value heldPartTestValue
	if err := json.Unmarshal(v.Payload, &value); err != nil {
		return nil, err
	}
	for _, link := range v.SnapshotLinks {
		if link.Role != "snapshot" || link.RefKey == "" {
			continue
		}
		out := CapturedCodecOutput{
			Address:    PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"},
			State:      "completed",
			ValueKind:  "directory",
			Value:      &SnapshotValue{Kind: "directory"},
			Role:       "snapshot",
			SnapshotID: link.RefKey,
		}
		if value.File != "" {
			out.ValueKind, out.Value = "file", &SnapshotValue{Kind: "file", Path: value.File}
		}
		return []CapturedCodecOutput{out}, nil
	}
	return nil, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.HeldPart", Typed: (*heldPartTestValue)(nil),
		Visitor: persistTestSnapshotRoleVisitor{}, Transfer: heldPartTestCodec{},
	})
}

// captureReadsCache is a cache whose snapshots export the chains given, by
// snapshot, with heldPartTestValue installed.
func captureReadsCache(t *testing.T, chains map[string]*snapshots.ExportChain) (context.Context, *Cache, *Server) {
	t.Helper()
	mgr := &exportChainTestManager{fakeSnapshotManager: &fakeSnapshotManager{}, chains: chains}
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), mgr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*heldPartTestValue]{}))
	srv.InstallObject(NewClass(srv, ClassOpts[*exportChainTestValue]{}))
	return srvToContext(ContextWithCache(ctx, c), srv), c, srv
}

// captureReadsChain is a chain of the layers of snapshots, each a prefix of
// it up to its own, the last the whole chain.
func captureReadsChain(snapshotIDs ...string) *snapshots.ExportChain {
	chain := &snapshots.ExportChain{Prefixes: map[string]int{}}
	for i, id := range snapshotIDs {
		chain.Layers = append(chain.Layers, exportChainTestLayer(id))
		chain.Prefixes[id] = i + 1
	}
	return chain
}

// captureReadsExport is what one content export gave, with every row named
// by its entry number, which is the engine's, instead of by its ordinal,
// which is the bundle's and follows the order the walk visits a row's
// dependencies in, which is a map's: its roots, its values by entry, its
// outputs by row and part, its chains, the roots it left out, the rows it
// carried, and its passes. bundle is the bundle as sent, and copied the
// entries whose records it copied.
type captureReadsExport struct {
	Roots   []uint64
	Values  []captureReadsValue
	Outputs []captureReadsOutput
	Chains  []captureReadsChainEntry
	LeftOut []captureReadsLeftOut
	Full    []uint64
	Held    []uint64
	Passes  int
	bundle  ValueBundle
	copied  map[uint64]bool
	// copiedRows, unpredicted and lateMoved are the export's own counts
	// (ExportedValues), and phases its phases.
	copiedRows, unpredicted, lateMoved int
	phases                             ExportPhases
}

// captureReadsValue is a bundle's value by entry. An expiry is a time the
// test's clock set: it is compared as set or not.
type captureReadsValue struct {
	SenderNumber       uint64
	SenderReplacements uint64
	Record             PersistedRecord
	Dependencies       []uint64
	Expires            bool
	Held               string
}

type captureReadsOutput struct {
	Row     uint64
	Address PersistedPartAddress
	State   string
	Value   *SnapshotValue
	Chain   []string
	Prefix  string
	Owner   []uint64
}

type captureReadsChainEntry struct {
	Row     uint64
	Address PersistedPartAddress
	Layers  []string
}

type captureReadsLeftOut struct {
	Index int
	Row   uint64
	Kind  BusyKind
}

// captureReadsCanonical names a bundle's rows by entry number.
func captureReadsCanonical(t *testing.T, got *captureReadsExport, bundle ValueBundle, chains *SelectedChains) {
	t.Helper()
	entry := func(o uint64) uint64 {
		require.True(t, o >= 1 && o <= uint64(len(bundle.Values)), "ordinal %d outside the bundle", o)
		return bundle.Values[o-1].SenderNumber
	}
	entries := func(ordinals []uint64) []uint64 {
		var out []uint64
		for _, o := range ordinals {
			out = append(out, entry(o))
		}
		slices.Sort(out)
		return out
	}
	layers := func(ls []snapshots.ExportLayer) []string {
		var out []string
		for _, l := range ls {
			out = append(out, l.Description)
		}
		return out
	}
	for _, root := range bundle.Roots {
		got.Roots = append(got.Roots, entry(uint64(root.Ordinal)))
	}
	for _, v := range bundle.Values {
		got.Values = append(got.Values, captureReadsValue{SenderNumber: v.SenderNumber, SenderReplacements: v.SenderReplacements, Record: v.Record, Dependencies: entries(v.DependencyIDs), Expires: v.ExpiresAtUnix != 0, Held: v.Held.String()})
	}
	slices.SortFunc(got.Values, func(a, b captureReadsValue) int { return int(a.SenderNumber) - int(b.SenderNumber) })
	for _, out := range bundle.Outputs {
		o := captureReadsOutput{Row: entry(uint64(out.Ordinal)), Address: out.Address, State: out.State}
		if out.Value != nil {
			value := *out.Value
			value.Services = slices.Clone(value.Services)
			for i := range value.Services {
				value.Services[i].ServiceResultID = entry(value.Services[i].ServiceResultID)
			}
			o.Value = &value
		}
		if out.Chain != nil {
			o.Chain = layers(out.Chain.Layers)
		}
		if out.Prefix != nil {
			key, err := partAddressKey(out.Prefix.Address)
			require.NoError(t, err)
			o.Prefix = fmt.Sprintf("%d %s %d", entry(uint64(out.Prefix.Ordinal)), key, out.Prefix.Layers)
		}
		if out.Owner != nil {
			o.Owner = entries(out.Owner.DependencyIDs)
		}
		got.Outputs = append(got.Outputs, o)
	}
	slices.SortStableFunc(got.Outputs, func(a, b captureReadsOutput) int {
		if a.Row != b.Row {
			return int(a.Row) - int(b.Row)
		}
		ka, _ := partAddressKey(a.Address)
		kb, _ := partAddressKey(b.Address)
		switch {
		case ka < kb:
			return -1
		case ka > kb:
			return 1
		}
		return 0
	})
	for _, e := range chains.Entries {
		got.Chains = append(got.Chains, captureReadsChainEntry{Row: entry(uint64(e.Ordinal)), Address: e.Address, Layers: layers(e.Layers)})
	}
}

// captureReadsRun exports roots with outputs selected against known, as
// every known row the bundle has is kept (testKeepEveryKnownRow), so that
// its rows are the capture's own.
func captureReadsRun(t *testing.T, ctx context.Context, c *Cache, known *KnownRows, roots []AnyResult, outputs []SelectedValueOutput) captureReadsExport {
	t.Helper()
	got := captureReadsExport{copied: map[uint64]bool{}}
	var mu sync.Mutex
	c.testTransferCopied = func(id uint64) {
		mu.Lock()
		got.copied[id] = true
		mu.Unlock()
	}
	c.testKeepEveryKnownRow = true
	defer func() { c.testTransferCopied, c.testKeepEveryKnownRow = nil, false }()
	selection := ValueSelection{Roots: roots, Outputs: outputs, Known: known, LeaveOutOutputsOutsideClosure: true}
	require.NoError(t, c.WithExportedValues(ctx, selection, config.RefConfig{Builds: config.LayerBuilds{Workers: 4}}, func(_ context.Context, values *ExportedValues) error {
		got.bundle = values.Bundle
		captureReadsCanonical(t, &got, values.Bundle, values.Chains)
		for _, root := range values.LeftOut {
			got.LeftOut = append(got.LeftOut, captureReadsLeftOut{Index: root.Index, Row: root.Row, Kind: root.Kind})
		}
		for number := range values.Rows.Full {
			got.Full = append(got.Full, number)
		}
		slices.Sort(got.Full)
		got.Held = slices.Sorted(slices.Values(values.Rows.Held))
		got.Passes = values.Phases.BundlePasses
		got.copiedRows, got.unpredicted, got.lateMoved = values.CopiedRows, values.UnpredictedLinks, values.LateMoved
		got.phases = values.Phases
		return nil
	}))
	return got
}

// assertCaptureReadsGolden checks an export's rows, outputs, chains,
// left-out roots and passes against the golden file the capture wrote
// before it read a held row's record only for its parts.
func assertCaptureReadsGolden(t *testing.T, got captureReadsExport, name string) {
	t.Helper()
	encoded, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	golden.Assert(t, string(encoded)+"\n", "capture-reads-"+name+".json")
}

// knownAll makes every row of roots' closures known, as a merged
// records-only export of each would.
func knownAll(t *testing.T, ctx context.Context, c *Cache, roots ...AnyResult) *KnownRows {
	t.Helper()
	known := NewKnownRows()
	for _, root := range roots {
		_, rows, _ := recordsOnlyCapture(t, ctx, c, known, root)
		known.Merged(rows)
	}
	return known
}

func numbers(results ...AnyResult) map[uint64]bool {
	out := map[uint64]bool{}
	for _, res := range results {
		out[knownRowsTestNumber(res)] = true
	}
	return out
}

// capturePrefixes is a closure whose chains walk the snapshots of known rows
// below the selected parts: rows s0 to s5 each built on the one before, the
// top's chain walking them all, and a side root's walking s0, s2 and its own
// t. u is a known row whose snapshot no chain walks, and plain a known row
// with no snapshot. fresh is new, under the top.
type capturePrefixes struct {
	ctx                        context.Context
	c                          *Cache
	known                      *KnownRows
	rows                       []AnyResult
	u, plain, side, fresh, top AnyResult
}

func capturePrefixesFixture(t *testing.T) capturePrefixes {
	t.Helper()
	ctx, c, srv := captureReadsCache(t, map[string]*snapshots.ExportChain{
		"s5": captureReadsChain("s0", "s1", "s2", "s3", "s4", "s5"),
		"t":  captureReadsChain("s0", "s2", "t"),
	})
	f := capturePrefixes{ctx: ctx, c: c, rows: make([]AnyResult, 6)}
	for i := range f.rows {
		f.rows[i] = persistedListTestResult(t, ctx, c, srv, fmt.Sprintf("reads-row%d", i), &heldPartTestValue{SnapshotID: fmt.Sprintf("s%d", i)})
		if i > 0 {
			transferTestDependency(c, ctx, f.rows[i], f.rows[i-1])
		}
	}
	f.u = persistedListTestResult(t, ctx, c, srv, "reads-unwalked", &heldPartTestValue{SnapshotID: "u"})
	f.plain = persistedListTestResult(t, ctx, c, srv, "reads-plain", String("plain"))
	transferTestDependency(c, ctx, f.rows[5], f.u)
	transferTestDependency(c, ctx, f.u, f.plain)
	f.side = persistedListTestResult(t, ctx, c, srv, "reads-side", &heldPartTestValue{SnapshotID: "t"})
	transferTestDependency(c, ctx, f.side, f.rows[2])
	f.known = knownAll(t, ctx, c, f.rows[5], f.side)
	f.fresh = persistedListTestResult(t, ctx, c, srv, "reads-fresh", &heldPartTestValue{SnapshotID: "f"})
	f.top = persistedListTestResult(t, ctx, c, srv, "reads-top", &heldPartTestValue{SnapshotID: "s5"})
	transferTestDependency(c, ctx, f.top, f.rows[5])
	transferTestDependency(c, ctx, f.top, f.fresh)
	return f
}

// run exports the top and the side root with their parts selected.
func (f capturePrefixes) run(t *testing.T) captureReadsExport {
	t.Helper()
	snapshot := PersistedPartAddress{Part: "snapshot"}
	return captureReadsRun(t, f.ctx, f.c, f.known, []AnyResult{f.top, f.side}, []SelectedValueOutput{{Result: f.top, Address: snapshot}, {Result: f.side, Address: snapshot}})
}

// A content export sends the bundle it sent when it copied every row's
// record, field for field, while it reads a held row's record only for its
// parts: those of the rows on a selected output's way down, and those of
// the rows holding a snapshot a selected chain walks, the chain's prefixes.
// It copies the records of the rows it carries in full and of those rows,
// and of no other held row. The golden files were written by the capture
// that copied every row.
func TestContentExportReadsHeldRowsOnlyForTheirParts(t *testing.T) {
	t.Parallel()
	snapshot := PersistedPartAddress{Part: "snapshot"}

	t.Run("prefixes on held rows", func(t *testing.T) {
		t.Parallel()
		f := capturePrefixesFixture(t)
		got := f.run(t)
		assertCaptureReadsGolden(t, got, "prefixes")
		require.Equal(t, numbers(f.top, f.fresh, f.side, f.rows[0], f.rows[1], f.rows[2], f.rows[3], f.rows[4], f.rows[5]), got.copied,
			"the rows in full, the selected rows and the rows the chains walk; not u, whose snapshot no chain walks, nor plain")
	})

	t.Run("a selected part on a held row's way down", func(t *testing.T) {
		t.Parallel()
		// The selected part is the second item of a list: the list's record
		// names its items' rows, and the item's record holds the part.
		ctx, c, srv := captureReadsCache(t, map[string]*snapshots.ExportChain{"i1": captureReadsChain("i1")})
		items := []AnyResult{
			persistedListTestResult(t, ctx, c, srv, "reads-item0", &heldPartTestValue{SnapshotID: "i0"}),
			persistedListTestResult(t, ctx, c, srv, "reads-item1", &heldPartTestValue{SnapshotID: "i1"}),
		}
		list := persistedListTestResult(t, ctx, c, srv, "reads-list", DynamicResultArrayOutput{Elem: &heldPartTestValue{}, Values: items})
		known := knownAll(t, ctx, c, list)
		root := persistedListTestResult(t, ctx, c, srv, "reads-list-root", String("root"))
		transferTestDependency(c, ctx, root, list)
		path := PersistedPartAddress{OutputPath: PersistedRefPath{{Field: "items"}, {IsIndex: true, Index: 1}}, Part: "snapshot"}

		got := captureReadsRun(t, ctx, c, known, []AnyResult{root}, []SelectedValueOutput{{Result: list, Address: path}})
		assertCaptureReadsGolden(t, got, "way-down")
		require.Len(t, got.bundle.Outputs, 1, "the selected item's part goes")
		require.Equal(t, numbers(root, list, items[1]), got.copied, "the list and the item on the way down; not the other item")
	})

	t.Run("a held row carried in full", func(t *testing.T) {
		t.Parallel()
		// The old entry, known, is retired when its recipe gets a new value
		// under the same root: it goes in full, in a second pass.
		ctx, c, srv := captureReadsCache(t, map[string]*snapshots.ExportChain{"r": captureReadsChain("o", "r")})
		old := persistedListTestResult(t, ctx, c, srv, "reads-recipe", &heldPartTestValue{SnapshotID: "o"})
		below := persistedListTestResult(t, ctx, c, srv, "reads-recipe-below", &heldPartTestValue{SnapshotID: "b"})
		transferTestDependency(c, ctx, old, below)
		known := knownAll(t, ctx, c, old)
		root := persistedListTestResult(t, ctx, c, srv, "reads-recipe-root", &heldPartTestValue{SnapshotID: "r"})
		transferTestDependency(c, ctx, root, old)
		currentEntryTestExpire(c, old)
		fresh := persistedListTestResult(t, ctx, c, srv, "reads-recipe", &heldPartTestValue{SnapshotID: "n"})
		transferTestDependency(c, ctx, root, fresh)

		got := captureReadsRun(t, ctx, c, known, []AnyResult{root}, []SelectedValueOutput{{Result: root, Address: snapshot}})
		assertCaptureReadsGolden(t, got, "carried")
		require.Equal(t, 2, got.Passes, "the old entry goes in full in a second pass")
		require.False(t, got.copied[knownRowsTestNumber(below)], "the row below the old entry is held and no chain walks its snapshot")
	})

	t.Run("a busy held row", func(t *testing.T) {
		t.Parallel()
		// Root a reaches a known row whose evaluation is in flight, which no
		// part of the bundle reads: it is left out, as when the capture
		// copied it and found it busy. Root b goes.
		ctx, c, srv := captureReadsCache(t, map[string]*snapshots.ExportChain{"a": captureReadsChain("a"), "b": captureReadsChain("b")})
		busy := persistedListTestResult(t, ctx, c, srv, "reads-busy", &heldPartTestValue{SnapshotID: "busy"})
		calm := persistedListTestResult(t, ctx, c, srv, "reads-calm", &heldPartTestValue{SnapshotID: "calm"})
		known := knownAll(t, ctx, c, busy, calm)
		a := persistedListTestResult(t, ctx, c, srv, "reads-busy-a", &heldPartTestValue{SnapshotID: "a"})
		b := persistedListTestResult(t, ctx, c, srv, "reads-busy-b", &heldPartTestValue{SnapshotID: "b"})
		transferTestDependency(c, ctx, a, busy)
		transferTestDependency(c, ctx, b, calm)
		res := busy.cacheSharedResult()
		res.lazyMu.Lock()
		res.lazyWhole.syncPending = true
		res.lazyMu.Unlock()
		t.Cleanup(func() {
			res.lazyMu.Lock()
			res.lazyWhole.syncPending = false
			res.lazyMu.Unlock()
		})

		got := captureReadsRun(t, ctx, c, known, []AnyResult{a, b}, []SelectedValueOutput{{Result: a, Address: snapshot}, {Result: b, Address: snapshot}})
		assertCaptureReadsGolden(t, got, "busy")
		require.Len(t, got.LeftOut, 1)
		require.Equal(t, 0, got.LeftOut[0].Index, "a, which reaches the busy row, is left out")
		require.False(t, got.copied[knownRowsTestNumber(calm)], "a held row no part reads isn't copied")
	})

	t.Run("files", func(t *testing.T) {
		t.Parallel()
		// Two Files of the same path in one snapshot and one in another, on
		// a known directory under them all.
		ctx, c, srv := captureReadsCache(t, map[string]*snapshots.ExportChain{})
		dir := persistedListTestResult(t, ctx, c, srv, "reads-dir", &heldPartTestValue{SnapshotID: "bin"})
		known := knownAll(t, ctx, c, dir)
		files := []AnyResult{
			persistedListTestResult(t, ctx, c, srv, "reads-fileA", &heldPartTestValue{SnapshotID: "bin", File: "/pkg.test"}),
			persistedListTestResult(t, ctx, c, srv, "reads-fileB", &heldPartTestValue{SnapshotID: "bin", File: "/pkg.test"}),
			persistedListTestResult(t, ctx, c, srv, "reads-fileC", &heldPartTestValue{SnapshotID: "bin2", File: "/pkg.test"}),
		}
		var outputs []SelectedValueOutput
		for _, file := range files {
			transferTestDependency(c, ctx, file, dir)
			outputs = append(outputs, SelectedValueOutput{Result: file, Address: snapshot})
		}

		got := captureReadsRun(t, ctx, c, known, files, outputs)
		assertCaptureReadsGolden(t, got, "files")
		require.False(t, got.copied[knownRowsTestNumber(dir)], "a File's chain is its own content: it walks no snapshot, so no prefix is read")
	})
}
