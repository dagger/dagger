package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/golden"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
)

// exportChainTestValue is a value whose one part, "snapshot", is complete in
// the snapshot SnapshotID: a File's when File is set, its path there, and a
// directory's otherwise.
type exportChainTestValue struct {
	SnapshotID string `json:"snapshotID"`
	File       string `json:"file,omitempty"`
}

func (*exportChainTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "ExportChainTestValue", NonNull: true}
}

func (v *exportChainTestValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(v)
	return PersistedObjectEncoding{JSON: raw}, err
}

func (*exportChainTestValue) DecodePersistedObject(_ context.Context, _ *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	v := new(exportChainTestValue)
	return v, json.Unmarshal(raw, v)
}

func (*exportChainTestValue) PersistedOutputRevision() (OutputRevision, error) { return 0, nil }

type exportChainTestCodec struct{}

func (exportChainTestCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}

func (exportChainTestCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }

func (exportChainTestCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	var value exportChainTestValue
	if err := json.Unmarshal(v.Payload, &value); err != nil {
		return nil, err
	}
	out := CapturedCodecOutput{
		Address:    PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"},
		State:      "completed",
		ValueKind:  "directory",
		Value:      &SnapshotValue{Kind: "directory"},
		SnapshotID: value.SnapshotID,
	}
	if value.File != "" {
		out.ValueKind, out.Value = "file", &SnapshotValue{Kind: "file", Path: value.File}
	}
	return []CapturedCodecOutput{out}, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.ExportChain", Typed: (*exportChainTestValue)(nil),
		Visitor: PersistedNoReferences{}, Transfer: exportChainTestCodec{},
	})
}

// exportChainTestManager opens snapshots as exportChainTestRefs, whose chains
// each wait to build until want of them are building, or until the wait
// gives up.
type exportChainTestManager struct {
	*fakeSnapshotManager
	want int
	wait time.Duration
	// chains, when set, are the chains the snapshots export, by snapshot;
	// a snapshot it doesn't name exports an empty chain. Its builds don't
	// wait.
	chains map[string]*snapshots.ExportChain
	// fail, when set, fails every File's chain build. fileStarts counts
	// the Files' chain builds begun.
	fail       bool
	fileStarts int

	mu       sync.Mutex
	building int
	peak     int
	all      chan struct{}
}

func (m *exportChainTestManager) GetBySnapshotID(_ context.Context, id string, _ ...snapshots.RefOption) (snapshots.ImmutableRef, error) {
	return &exportChainTestRef{id: id, manager: m}, nil
}

// build is one chain's build: it begins, waits for the others, and ends.
func (m *exportChainTestManager) build(ctx context.Context) (*snapshots.ExportChain, error) {
	m.mu.Lock()
	m.building++
	if m.building > m.peak {
		m.peak = m.building
	}
	if m.building == m.want {
		close(m.all)
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.building--
		m.mu.Unlock()
	}()
	select {
	case <-m.all:
		return &snapshots.ExportChain{}, nil
	case <-time.After(m.wait):
		m.mu.Lock()
		defer m.mu.Unlock()
		return nil, fmt.Errorf("chain built with %d of %d building at once", m.building, m.want)
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

type exportChainTestRef struct {
	id      string
	manager *exportChainTestManager
}

func (r *exportChainTestRef) ID() string                          { return r.id }
func (r *exportChainTestRef) SnapshotID() string                  { return r.id }
func (r *exportChainTestRef) Release(context.Context) error       { return nil }
func (r *exportChainTestRef) Size(context.Context) (int64, error) { return 0, nil }
func (r *exportChainTestRef) Mount(context.Context, bool) (snapshots.MountableRef, error) {
	panic("an export's chains don't mount here")
}
func (r *exportChainTestRef) ExportChain(ctx context.Context, _ config.RefConfig) (*snapshots.ExportChain, error) {
	if r.manager.chains != nil {
		if chain, ok := r.manager.chains[r.id]; ok {
			return chain, nil
		}
		return &snapshots.ExportChain{}, nil
	}
	return r.manager.build(ctx)
}
func (r *exportChainTestRef) ExportFileChain(ctx context.Context, _ config.RefConfig, path string) (*snapshots.ExportChain, error) {
	r.manager.mu.Lock()
	r.manager.fileStarts++
	fail := r.manager.fail
	r.manager.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("file chain %s failed", r.id)
	}
	if r.manager.chains != nil {
		return &snapshots.ExportChain{Layers: []snapshots.ExportLayer{exportChainTestLayer(r.id + ":" + path)}}, nil
	}
	return r.manager.build(ctx)
}

var _ snapshots.ImmutableRef = (*exportChainTestRef)(nil)

// An export builds the chains of its Files, each its own content, at once
// with each other and with its other parts' chains, as it builds those: two
// Files' chains and a directory's are all building together, rather than
// each File's alone before the rest.
func TestExportBuildsFileChainsTogether(t *testing.T) {
	t.Parallel()
	mgr := &exportChainTestManager{fakeSnapshotManager: &fakeSnapshotManager{}, want: 3, wait: 5 * time.Second, all: make(chan struct{})}
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), mgr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*exportChainTestValue]{}))
	ctx = srvToContext(ContextWithCache(ctx, c), srv)

	roots := []AnyResult{
		persistedListTestResult(t, ctx, c, srv, "fileA", &exportChainTestValue{SnapshotID: "a", File: "/bin/a"}),
		persistedListTestResult(t, ctx, c, srv, "fileB", &exportChainTestValue{SnapshotID: "b", File: "/bin/b"}),
		persistedListTestResult(t, ctx, c, srv, "dir", &exportChainTestValue{SnapshotID: "d"}),
	}
	selection := ValueSelection{Roots: roots}
	for _, root := range roots {
		selection.Outputs = append(selection.Outputs, SelectedValueOutput{Result: root, Address: PersistedPartAddress{Part: "snapshot"}})
	}
	var entries int
	err = c.WithExportedValues(ctx, selection, config.RefConfig{Builds: config.LayerBuilds{Workers: 4}}, func(_ context.Context, values *ExportedValues) error {
		entries = len(values.Chains.Entries)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, entries)
	require.Equal(t, 3, mgr.peak, "chains building at once")
}

// The capture's last step does its work once per row, however many of the
// closure's parts the bundle carries. Of a closure of rows each built on the
// one before, each with a complete part, the selected chain walked every
// other row's snapshot. Those rows' parts go as prefixes of the selected
// chain, each owned by the row below them all that needs a session
// resource. Only those rows' parts are routed, and each row's owners are
// named once.
func TestExportLastStepOncePerRow(t *testing.T) {
	t.Parallel()
	const n = 40
	top := fmt.Sprintf("s%d", n-1)
	chain := &snapshots.ExportChain{Layers: make([]snapshots.ExportLayer, n), Prefixes: map[string]int{top: n}}
	for i := 0; i < n-1; i += 2 {
		chain.Prefixes[fmt.Sprintf("s%d", i)] = i + 1
	}
	mgr := &exportChainTestManager{fakeSnapshotManager: &fakeSnapshotManager{}, chains: map[string]*snapshots.ExportChain{top: chain}}
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), mgr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*exportChainTestValue]{}))
	ctx = srvToContext(ContextWithCache(ctx, c), srv)

	rows := make([]AnyResult, n)
	for i := range rows {
		rows[i] = persistedListTestResult(t, ctx, c, srv, fmt.Sprintf("row%d", i), &exportChainTestValue{SnapshotID: fmt.Sprintf("s%d", i)})
		if i > 0 {
			transferTestDependency(c, ctx, rows[i], rows[i-1])
		}
	}
	c.egraphMu.Lock()
	rows[0].cacheSharedResult().sessionResourceHandle = "socket"
	c.egraphMu.Unlock()

	var routes, walks int
	c.testPartRouted = func(uint64) { routes++ }
	c.testOwnerWalked = func(uint64) { walks++ }
	var bundle ValueBundle
	var sources []ImportedValue
	err = c.WithExportedValues(ctx, ValueSelection{
		Roots:   []AnyResult{rows[n-1]},
		Outputs: []SelectedValueOutput{{Result: rows[n-1], Address: PersistedPartAddress{Part: "snapshot"}}},
	}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle, sources = values.Bundle, values.Sources
		return nil
	})
	require.NoError(t, err)

	ordinalOf := map[uint64]TransferOrdinal{}
	for _, source := range sources {
		ordinalOf[source.ResultID] = source.Ordinal
	}
	socket := uint64(ordinalOf[uint64(rows[0].cacheSharedResult().id)])
	carried := map[TransferOrdinal]bool{}
	for _, output := range bundle.Outputs {
		carried[output.Ordinal] = true
		require.NotNil(t, output.Owner, "output of %d", output.Ordinal)
		require.Equal(t, []uint64{socket}, output.Owner.DependencyIDs, "owners of %d", output.Ordinal)
	}
	for i, row := range rows {
		ordinal := ordinalOf[uint64(row.cacheSharedResult().id)]
		require.Equal(t, i%2 == 0 || i == n-1, carried[ordinal], "row %d carried", i)
	}
	require.Equal(t, n/2, routes, "parts routed")
	require.LessOrEqual(t, walks, n, "rows walked to name owners")
}

// Once a File's chain fails, the export starts no File's chain still
// waiting for a build slot: with one build at a time, the first failure is
// the only File's chain begun.
func TestExportStartsNoFileChainAfterAFailure(t *testing.T) {
	t.Parallel()
	mgr := &exportChainTestManager{fakeSnapshotManager: &fakeSnapshotManager{}, fail: true}
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), mgr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*exportChainTestValue]{}))
	ctx = srvToContext(ContextWithCache(ctx, c), srv)

	var selection ValueSelection
	for _, name := range []string{"a", "b", "c"} {
		root := persistedListTestResult(t, ctx, c, srv, "file"+name, &exportChainTestValue{SnapshotID: name, File: "/bin/" + name})
		selection.Roots = append(selection.Roots, root)
		selection.Outputs = append(selection.Outputs, SelectedValueOutput{Result: root, Address: PersistedPartAddress{Part: "snapshot"}})
	}
	err = c.WithExportedValues(ctx, selection, config.RefConfig{Builds: config.LayerBuilds{Workers: 1}}, func(context.Context, *ExportedValues) error {
		t.Error("a failed export consumes nothing")
		return nil
	})
	require.ErrorContains(t, err, "file chain a failed")
	require.Equal(t, 1, mgr.fileStarts, "File chains begun")
}

// Two of an export's parts that are the same File, the same path in the same
// snapshot, share one chain: it is built once.
func TestExportBuildsOneFileOnce(t *testing.T) {
	t.Parallel()
	mgr := &exportChainTestManager{fakeSnapshotManager: &fakeSnapshotManager{}, chains: map[string]*snapshots.ExportChain{}}
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), mgr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*exportChainTestValue]{}))
	ctx = srvToContext(ContextWithCache(ctx, c), srv)

	roots := []AnyResult{
		persistedListTestResult(t, ctx, c, srv, "mountA", &exportChainTestValue{SnapshotID: "bin", File: "/pkg.test"}),
		persistedListTestResult(t, ctx, c, srv, "mountB", &exportChainTestValue{SnapshotID: "bin", File: "/pkg.test"}),
		persistedListTestResult(t, ctx, c, srv, "other", &exportChainTestValue{SnapshotID: "bin", File: "/other"}),
	}
	selection := ValueSelection{Roots: roots}
	for _, root := range roots {
		selection.Outputs = append(selection.Outputs, SelectedValueOutput{Result: root, Address: PersistedPartAddress{Part: "snapshot"}})
	}
	var layers [][]snapshots.ExportLayer
	err = c.WithExportedValues(ctx, selection, config.RefConfig{Builds: config.LayerBuilds{Workers: 4}}, func(_ context.Context, values *ExportedValues) error {
		for _, entry := range values.Chains.Entries {
			layers = append(layers, entry.Layers)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, mgr.fileStarts, "File chains built")
	require.Len(t, layers, 3)
	require.Equal(t, layers[0], layers[1])
	require.Len(t, layers[2], 1)
}

// exportChainTestLayer is a layer whose blob is named by name alone.
func exportChainTestLayer(name string) snapshots.ExportLayer {
	return snapshots.ExportLayer{Description: name, Descriptor: ocispecs.Descriptor{MediaType: ocispecs.MediaTypeImageLayerZstd, Digest: digest.FromString(name), Size: int64(len(name))}}
}

// An export's bundle and chains are what they were before the capture built
// Files' chains together, built one chain for parts that are the same File
// and did its last step once per row: its records, its outputs with their
// chains, prefixes and owners, and each chain's layers. The golden file was
// written by the capture as it was at 27280baaf3. The closure: rows each
// built on the one before, the bottom one needing a session resource, whose
// directories two selected chains walked, each every other one of some, the
// first carried chain naming a prefix both walked; Files, two of them the
// same path in one snapshot and a third that path in another; and entry
// numbers that aren't the bundle's ordinals.
func TestExportBundleUnchanged(t *testing.T) {
	t.Parallel()
	const n = 8
	top := fmt.Sprintf("s%d", n-1)
	chain := &snapshots.ExportChain{Prefixes: map[string]int{top: n}}
	for i := 0; i < n; i++ {
		chain.Layers = append(chain.Layers, exportChainTestLayer(fmt.Sprintf("s%d", i)))
	}
	for i := 0; i < n-1; i += 2 {
		chain.Prefixes[fmt.Sprintf("s%d", i)] = i + 1
	}
	// side walked the lower rows, and comes first among the selected parts.
	side := &snapshots.ExportChain{Layers: []snapshots.ExportLayer{exportChainTestLayer("s0"), exportChainTestLayer("s1"), exportChainTestLayer("s2"), exportChainTestLayer("t")}, Prefixes: map[string]int{"s0": 1, "s2": 3, "t": 4}}
	mgr := &exportChainTestManager{fakeSnapshotManager: &fakeSnapshotManager{}, chains: map[string]*snapshots.ExportChain{top: chain, "t": side}}
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), mgr, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*exportChainTestValue]{}))
	ctx = srvToContext(ContextWithCache(ctx, c), srv)

	// Entries outside the closure, so its entry numbers aren't its ordinals.
	for i := range 5 {
		persistedListTestResult(t, ctx, c, srv, fmt.Sprintf("unrelated%d", i), &exportChainTestValue{SnapshotID: fmt.Sprintf("u%d", i)})
	}
	rows := make([]AnyResult, n)
	for i := range rows {
		rows[i] = persistedListTestResult(t, ctx, c, srv, fmt.Sprintf("row%d", i), &exportChainTestValue{SnapshotID: fmt.Sprintf("s%d", i)})
		if i > 0 {
			transferTestDependency(c, ctx, rows[i], rows[i-1])
		}
	}
	c.egraphMu.Lock()
	rows[0].cacheSharedResult().sessionResourceHandle = "socket"
	c.egraphMu.Unlock()
	sideRoot := persistedListTestResult(t, ctx, c, srv, "side", &exportChainTestValue{SnapshotID: "t"})
	transferTestDependency(c, ctx, sideRoot, rows[3])
	roots := []AnyResult{
		sideRoot,
		rows[n-1],
		persistedListTestResult(t, ctx, c, srv, "mountA", &exportChainTestValue{SnapshotID: "bin", File: "/pkg.test"}),
		persistedListTestResult(t, ctx, c, srv, "mountB", &exportChainTestValue{SnapshotID: "bin", File: "/pkg.test"}),
		persistedListTestResult(t, ctx, c, srv, "mountC", &exportChainTestValue{SnapshotID: "bin2", File: "/pkg.test"}),
		persistedListTestResult(t, ctx, c, srv, "cli", &exportChainTestValue{SnapshotID: "bin", File: "/cli"}),
	}
	transferTestDependency(c, ctx, roots[2], rows[0])
	selection := ValueSelection{Roots: roots}
	for _, root := range roots {
		selection.Outputs = append(selection.Outputs, SelectedValueOutput{Result: root, Address: PersistedPartAddress{Part: "snapshot"}})
	}
	var got struct {
		Bundle ValueBundle
		Chains []SelectedChain
	}
	err = c.WithExportedValues(ctx, selection, config.RefConfig{Builds: config.LayerBuilds{Workers: 4}}, func(_ context.Context, values *ExportedValues) error {
		got.Bundle = values.Bundle
		for _, entry := range values.Chains.Entries {
			entry.Provider = nil
			got.Chains = append(got.Chains, entry)
		}
		return nil
	})
	require.NoError(t, err)
	encoded, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	golden.Assert(t, string(encoded)+"\n", "export-bundle.json")
}
