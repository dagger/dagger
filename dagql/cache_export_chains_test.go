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

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

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
func (r *exportChainTestRef) ExportFileChain(ctx context.Context, _ config.RefConfig, _ string) (*snapshots.ExportChain, error) {
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
