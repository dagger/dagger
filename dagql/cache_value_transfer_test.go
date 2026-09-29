package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type transferTestValue struct {
	Text   string `json:"text"`
	Child  uint64 `json:"child,omitempty"`
	Recipe string `json:"recipe,omitempty"`
	// PartDeps are the result numbers its snapshot part's probe declares as
	// dependencies, which a receiver installing the part takes on.
	PartDeps []uint64 `json:"partDeps,omitempty"`
	rev      atomic.Uint64
	release  OnReleaseFunc
	links    []PersistedSnapshotRefLink
	// revisionHook runs on each output revision read, in the reader's goroutine.
	revisionHook func()
	// unready makes the output revision read report a held persistence guard.
	unready atomic.Bool
}

func (*transferTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "TransferTestValue", NonNull: true}
}
func (v *transferTestValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(v)
	return PersistedObjectEncoding{JSON: raw, SnapshotLinks: cloneSnapshotRefLinks(v.links)}, err
}
func (v *transferTestValue) PersistedOutputRevision() (OutputRevision, error) {
	if v.revisionHook != nil {
		v.revisionHook()
	}
	if v.unready.Load() {
		return 0, fmt.Errorf("%w: test output in use", ErrPersistStateNotReady)
	}
	return OutputRevision(v.rev.Load()), nil
}
func (*transferTestValue) DecodePersistedObject(ctx context.Context, dec *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	value := new(transferTestValue)
	err := json.Unmarshal(raw, value)
	if err == nil && value.Text == "snapshot" {
		value.links, err = dec.SnapshotRoles(ctx)
	}
	if err == nil {
		if hook, ok := transferDecodeHooks.Load(dec.ResultID()); ok {
			err = hook.(func(context.Context, *PersistDecodeContext, *transferTestValue) error)(ctx, dec, value)
		}
	}
	return value, err
}

type transferTestCodec struct{}

func (transferTestCodec) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	if err := VisitPersistedSnapshotRoles(visit, PersistedRefOutputRole, v.Path, v.SnapshotLinks); err != nil {
		return nil, err
	}
	value := new(transferTestValue)
	if err := json.Unmarshal(v.Payload, value); err != nil {
		return nil, err
	}
	if value.Child != 0 {
		if _, err := VisitPersistedRow(visit, PersistedRefChild, v.Path.Field("child"), &value.Child); err != nil {
			return nil, err
		}
	}
	if value.Recipe != "" {
		if _, err := VisitPersistedCallID(visit, PersistedRefChild, v.Path.Field("recipe"), &value.Recipe); err != nil {
			return nil, err
		}
	}
	return json.Marshal(value)
}
func (transferTestCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}
func (transferTestCodec) ValidateForeign(v PersistedPayloadVisit) error {
	_, err := (transferTestCodec{}).VisitPersistedReferences(v, func(*PersistedRef) error { return nil })
	return err
}
func (transferTestCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	return []CapturedCodecOutput{{Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, State: "pending", Value: &SnapshotValue{Kind: "directory"}}}, nil
}
func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.Transfer", Typed: (*transferTestValue)(nil), Visitor: transferTestCodec{}, Transfer: transferTestCodec{}})
}
func transferTestCache(t *testing.T) (context.Context, *Cache, *Server) {
	t.Helper()
	ctx, c, srv := persistedListTestCache(t, filepath.Join(t.TempDir(), "cache.db"))
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	return ctx, c, srv
}
func exportTestBundle(t *testing.T, ctx context.Context, c *Cache, roots ...AnyResult) ValueBundle {
	t.Helper()
	var bundle ValueBundle
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error { bundle = values.Bundle; return nil }))
	return bundle
}
func transferTestDependency(c *Cache, ctx context.Context, parent, child AnyResult) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	p, d := parent.cacheSharedResult(), child.cacheSharedResult()
	if p.deps == nil {
		p.deps = map[sharedResultID]struct{}{}
	}
	if _, exists := p.deps[d.id]; exists {
		return
	}
	p.deps[d.id] = struct{}{}
	p.dependencyOwnershipRevision++
	c.rememberDependencyEdgeLocked(p, d)
	c.incrementIncomingOwnershipLocked(ctx, d)
}
func transferTestOffer(t *testing.T, c *Cache, ctx context.Context, parent, child AnyResult) {
	t.Helper()
	c.egraphMu.Lock()
	record := PersistedPartOffer{Address: PersistedPartAddress{Part: "snapshot"}, Value: SnapshotValue{Kind: "directory"}, Owner: PersistedOfferOwner{DependencyIDs: []uint64{uint64(child.cacheSharedResult().id)}}}
	owner, err := c.newOfferOwnerLocked(ctx, record.Owner)
	if err == nil {
		err = c.testAttachPartOfferLocked(parent.cacheSharedResult(), record.Address, &partOffer{record: record, owner: owner})
	}
	c.egraphMu.Unlock()
	require.NoError(t, err)
}

func TestValueTransferCapture(t *testing.T) {
	testValueTransferCaptureConcurrent(t)
	t.Run("direct and offer diamond", func(t *testing.T) {
		ctx, c, srv := transferTestCache(t)
		leaf := persistedListTestResult(t, ctx, c, srv, "leaf", String("shared"))
		left := persistedListTestResult(t, ctx, c, srv, "left", &transferTestValue{Text: "left"})
		right := persistedListTestResult(t, ctx, c, srv, "right", &transferTestValue{Text: "right"})
		root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "root"})
		transferTestDependency(c, ctx, left, leaf)
		transferTestDependency(c, ctx, right, leaf)
		transferTestDependency(c, ctx, root, left)
		transferTestOffer(t, c, ctx, root, right)
		bundle := exportTestBundle(t, ctx, c, root)
		require.Len(t, bundle.Values, 4)
		rootValue := bundle.Values[int(bundle.Roots[0].Ordinal)-1]
		require.Len(t, rootValue.DependencyIDs, 1)
		require.Len(t, rootValue.Record.Envelope.PendingOffers, 1)
		require.Len(t, rootValue.Record.Envelope.PendingOffers[0].Owner.DependencyIDs, 1)
		require.Zero(t, c.activeGlobalOperations.Load())
	})
	for _, kind := range []string{"output", "payload", "offer", "ownership", "requirements", "frame", "busy"} {
		t.Run(kind, func(t *testing.T) {
			ctx, c, srv := transferTestCache(t)
			object := &transferTestValue{Text: "pending"}
			root := persistedListTestResult(t, ctx, c, srv, "root", object)
			res := root.cacheSharedResult()
			owners := res.incomingOwnershipCount
			c.testTransferCopied = func(uint64) {
				switch kind {
				case "output":
					object.rev.Add(1)
				case "payload":
					res.payloadMu.Lock()
					res.payloadRevision++
					res.payloadMu.Unlock()
				case "offer":
					c.egraphMu.Lock()
					res.transferRevision++
					c.egraphMu.Unlock()
				case "ownership":
					c.egraphMu.Lock()
					res.dependencyOwnershipRevision++
					c.egraphMu.Unlock()
				case "requirements":
					res.requiredSessionResourcesGen.Add(1)
				case "frame":
					res.storeResultCall(res.loadResultCall().clone())
				case "busy": // Attachment is rechecked after every copy guard is released.
					c.egraphMu.Lock()
					res.attachDepsMu.Lock()
					res.attachDepsWaitCh = make(chan struct{})
					res.attachDepsMu.Unlock()
					c.egraphMu.Unlock()
				}
			}
			err := c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(context.Context, *ExportedValues) error { t.Fatal("incoherent capture delivered"); return nil })
			c.testTransferCopied = nil
			if kind == "busy" {
				close(res.attachDepsWaitCh)
			}
			require.ErrorIs(t, err, ErrPersistStateNotReady)
			require.Equal(t, owners, res.incomingOwnershipCount)
			require.Zero(t, c.activeGlobalOperations.Load())
		})
	}
	t.Run("cancellation", func(t *testing.T) {
		ctx, c, srv := transferTestCache(t)
		root := persistedListTestResult(t, ctx, c, srv, "cancel-root", String("value"))
		ctx, cancel := context.WithCancel(ctx)
		c.testTransferCopied = func(uint64) { cancel() }
		err := c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(context.Context, *ExportedValues) error { t.Fatal("canceled capture delivered"); return nil })
		require.ErrorIs(t, err, context.Canceled)
		require.Zero(t, c.activeGlobalOperations.Load())
	})
}

func TestValueTransferReferences(t *testing.T) {
	ctx, a, srv := transferTestCache(t)
	value := persistedListTestResult(t, ctx, a, srv, "large-int", Int(9007199254740993))
	list := persistedListTestResult(t, ctx, a, srv, "list", DynamicResultArrayOutput{Elem: Int(0), Values: []AnyResult{value}})
	bundle := exportTestBundle(t, ctx, a, list)
	bctx, b, bsrv := transferTestCache(t)
	for i := range 7 {
		persistedListTestResult(t, bctx, b, bsrv, "padding-"+string(rune('a'+i)), String("unrelated"))
	}
	mappingReply, err := b.MergeValues(bctx, cloudCacheID, bundle)
	mapping := mappingReply.Imported()
	require.NoError(t, err)
	require.Len(t, mapping, 1)
	require.Greater(t, mapping[0].ResultID, uint64(7))
	imported, err := b.LoadResultByResultID(bctx, "test-session", bsrv, mapping[0].ResultID)
	require.NoError(t, err)
	require.True(t, IsImportedResult(imported))
	array := imported.Unwrap().(Enumerable)
	item, err := array.NthValue(1, nil)
	require.NoError(t, err)
	require.Equal(t, Int(9007199254740993), item.Unwrap())
	require.NotEqual(t, value.cacheSharedResult().id, item.cacheSharedResult().id)
	forward := exportTestBundle(t, bctx, b, imported)
	require.Equal(t, bundle.Values[0].Record.Envelope.ScalarJSON, forward.Values[0].Record.Envelope.ScalarJSON)
	for _, bad := range []string{"dangling", "cycle", "root", "body", "unreachable"} {
		t.Run(bad, func(t *testing.T) {
			raw, err := json.Marshal(bundle)
			require.NoError(t, err)
			var broken ValueBundle
			require.NoError(t, json.Unmarshal(raw, &broken))
			switch bad {
			case "dangling":
				broken.Values[1].DependencyIDs = []uint64{99}
			case "cycle":
				broken.Values[0].DependencyIDs = []uint64{2}
			case "root":
				broken.Roots[0].Ordinal = 999
			case "body":
				broken.Values[0].Record.Envelope.Items = []PersistedResultEnvelope{{}}
			case "unreachable":
				broken.Roots[0].Ordinal = 1
			}
			before := len(b.resultsByID)
			_, err = b.MergeValues(bctx, cloudCacheID, broken)
			require.Error(t, err)
			require.Len(t, b.resultsByID, before)
		})
	}
	// An extra digest on a frame merges with it, and classes the entry it
	// arrives with.
	t.Run("extra", func(t *testing.T) {
		raw, err := json.Marshal(bundle)
		require.NoError(t, err)
		var withExtra ValueBundle
		require.NoError(t, json.Unmarshal(raw, &withExtra))
		extra := digest.FromString("carried")
		withExtra.Values[0].Record.Call.ExtraDigests = []call.ExtraDigest{{Digest: extra, Label: call.ExtraDigestLabelContent}}
		cctx, c, _ := transferTestCache(t)
		_, err = c.MergeValues(cctx, cloudCacheID, withExtra)
		require.NoError(t, err)
		c.egraphMu.RLock()
		class, known := c.egraphDigestToClass[extra.String()]
		results := len(c.outputEqClassResults[c.eqClassRootLocked(class)])
		c.egraphMu.RUnlock()
		require.True(t, known)
		require.Equal(t, 1, results)
	})
}

func TestValueTransferImportPublication(t *testing.T) {
	testValueTransferImportConcurrent(t)
	ctx, a, srv := transferTestCache(t)
	leaf := persistedListTestResult(t, ctx, a, srv, "leaf", String("value"))
	root := persistedListTestResult(t, ctx, a, srv, "root", DynamicResultArrayOutput{Elem: String(""), Values: []AnyResult{leaf}})
	bundle := exportTestBundle(t, ctx, a, root)
	t.Run("preparation failure", func(t *testing.T) {
		ctx, b, _ := transferTestCache(t)
		injected := errors.New("later identity plan refused")
		b.testTransferPlanPrepared = func(n int) error {
			if n == 2 {
				return injected
			}
			return nil
		}
		_, err := b.MergeValues(ctx, cloudCacheID, bundle)
		require.ErrorIs(t, err, injected)
		require.Empty(t, b.resultsByID)
		require.Empty(t, b.persistedEdgesByResult)
		require.Empty(t, b.eqClassToDigests)
	})
	for _, committed := range []bool{false, true} {
		name := "before commit"
		if committed {
			name = "after commit"
		}
		t.Run(name, func(t *testing.T) {
			ctx, b, _ := transferTestCache(t)
			ctx, cancel := context.WithCancel(ctx)
			if committed {
				b.testAfterTransferCommit = cancel
			} else {
				b.testBeforeTransferCommit = cancel
			}
			mappingReply, err := b.MergeValues(ctx, cloudCacheID, bundle)
			mapping := mappingReply.Imported()
			if committed {
				require.NoError(t, err)
				require.Len(t, mapping, 1)
				require.Len(t, b.resultsByID, 2)
			} else {
				require.ErrorIs(t, err, context.Canceled)
				require.Empty(t, b.resultsByID)
			}
		})
	}
	t.Run("finite root expiry", func(t *testing.T) {
		ctx, b, _ := transferTestCache(t)
		expiry := time.Now().Add(time.Hour).Unix()
		bundle.Roots[0].ExpiresAtUnix = expiry
		mappingReply, err := b.MergeValues(ctx, cloudCacheID, bundle)
		mapping := mappingReply.Imported()
		require.NoError(t, err)
		require.Equal(t, expiry, b.persistedEdgesByResult[sharedResultID(mapping[0].ResultID)].expiresAtUnix)
	})
}

// A recipe ID inside a payload travels with every extra digest of every
// vertex: the receiver's content digest as well as the call's. The export
// copies the DAG, so the original is untouched, and the receiving cache
// accepts the recipe as it is.
func TestValueTransferReferencesExtras(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	sourceContent, childContent := digest.FromString("source-content"), digest.FromString("child-content")
	recipe := call.New().Append(String("").Type(), "source").With(call.WithContentDigest(sourceContent)).Append(String("").Type(), "child").With(call.WithContentDigest(childContent))
	raw, err := recipe.Encode()
	require.NoError(t, err)
	value := persistedListTestResult(t, ctx, c, srv, "recipe", &transferTestValue{Text: "recipe", Recipe: raw})
	bundle := exportTestBundle(t, ctx, c, value)
	var payload transferTestValue
	require.NoError(t, json.Unmarshal(bundle.Values[0].Record.Envelope.ObjectJSON, &payload))
	var copied call.ID
	require.NoError(t, copied.Decode(payload.Recipe))
	require.Equal(t, childContent, copied.ContentDigest())
	require.Equal(t, sourceContent, copied.Receiver().ContentDigest(), "the receiver's content digest travels too")
	require.Equal(t, recipe.Receiver().ExtraDigests(), copied.Receiver().ExtraDigests())
	require.NotSame(t, recipe.Receiver(), copied.Receiver(), "the export copies the DAG")
	filtered, err := recipe.FilterTransferDigests()
	require.NoError(t, err)
	require.Equal(t, filtered.Digest(), copied.Digest())
	bctx, b, _ := transferTestCache(t)
	_, err = b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
	// The original recipe, as the sender holds it, is accepted as it is.
	bundle.Values[0].Record.Envelope.ObjectJSON = json.RawMessage(`{"text":"original","recipe":` + string(mustTransferJSON(t, raw)) + `}`)
	_, err = b.MergeValues(bctx, cloudCacheID, bundle)
	require.NoError(t, err)
}
func mustTransferJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return raw
}

var transferDecodeHooks sync.Map

func (v *transferTestValue) OnRelease(ctx context.Context) error {
	if v.release != nil {
		return v.release(ctx)
	}
	return nil
}

func TestValueTransferPersistenceDecodePublication(t *testing.T) {
	ctx, a, srv := transferTestCache(t)
	source := persistedListTestResult(t, ctx, a, srv, "encoded-source", &transferTestValue{Text: "old"})
	bundle := exportTestBundle(t, ctx, a, source)
	ctx, b, srv := transferTestCache(t)
	mappingReply, err := b.MergeValues(ctx, cloudCacheID, bundle)
	mapping := mappingReply.Imported()
	require.NoError(t, err)
	id := mapping[0].ResultID
	row := b.resultsByID[sharedResultID(id)]
	var rowCleanups, losingReleases, winningReleases atomic.Int32
	row.onRelease = joinOnRelease(row.onRelease, func(context.Context) error { rowCleanups.Add(1); return nil })
	row.payloadMu.Lock()
	row.snapshotOwnerLinks = []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "old-applied"}}
	row.snapshotLinkIntent = &snapshotLinkIntent{Links: []PersistedSnapshotRefLink{}}
	row.payloadRevision++
	row.payloadMu.Unlock()
	var calls int
	transferDecodeHooks.Store(id, func(ctx context.Context, dec *PersistDecodeContext, value *transferTestValue) error {
		calls++
		roles, err := dec.SnapshotRoles(ctx)
		require.NoError(t, err)
		if calls == 1 {
			require.Empty(t, roles, "an empty copied map is authoritative")
			value.release = func(context.Context) error { losingReleases.Add(1); return nil }
			row.payloadMu.Lock()
			next, err := clonePersistedEnvelope(*row.persistedEnvelope)
			if err != nil {
				row.payloadMu.Unlock()
				return err
			}
			next.ObjectJSON = json.RawMessage(`{"text":"new"}`)
			row.persistedEnvelope = &next
			row.snapshotLinkIntent = &snapshotLinkIntent{Links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "new-desired"}}}
			row.payloadRevision++
			row.payloadMu.Unlock()
		} else {
			require.Equal(t, []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "new-desired"}}, roles)
			value.release = func(context.Context) error { winningReleases.Add(1); return nil }
		}
		return nil
	})
	defer transferDecodeHooks.Delete(id)
	loaded, err := b.LoadResultByResultID(ctx, "test-session", srv, id)
	require.NoError(t, err)
	require.Equal(t, "new", loaded.Unwrap().(*transferTestValue).Text)
	require.Equal(t, 2, calls)
	require.EqualValues(t, 1, losingReleases.Load())
	require.Zero(t, rowCleanups.Load())
	require.NoError(t, b.ReleaseSession(ctx, "test-session"))
	_, removed, err := b.removePersistedEdge(ctx, row.id)
	require.NoError(t, err)
	require.True(t, removed)
	require.EqualValues(t, 1, rowCleanups.Load())
	require.EqualValues(t, 1, winningReleases.Load())
}

func (v *transferTestValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	return cloneSnapshotRefLinks(v.links)
}

// A prefix candidate that delegates to a parent part the bundle offers gets
// no prefix output; one that delegates to a part the bundle doesn't offer, or
// doesn't delegate, gets one.
func TestPrefixServedByDelegation(t *testing.T) {
	t.Parallel()
	rows := map[sharedResultID]*capturedTransferRow{7: {ordinal: 2}}
	fs, err := partAddressKey(PersistedPartAddress{Part: "fs"})
	require.NoError(t, err)
	delegating := LazyOperationRoute{Delegation: &PartDelegation{ParentResultID: 7, Address: PersistedPartAddress{Part: "fs"}}}
	for _, tc := range []struct {
		name    string
		route   LazyOperationRoute
		offered map[string]bool
		want    bool
	}{
		{"delegating, the parent part offered", delegating, map[string]bool{"2:" + fs: true}, true},
		{"delegating, the parent part not offered", delegating, map[string]bool{}, false},
		{"delegating, another part of the parent offered", delegating, map[string]bool{"2:other": true}, false},
		{"delegating to a parent outside the capture", LazyOperationRoute{Delegation: &PartDelegation{ParentResultID: 9, Address: PersistedPartAddress{Part: "fs"}}}, map[string]bool{"2:" + fs: true}, false},
		{"not delegating", LazyOperationRoute{}, map[string]bool{"2:" + fs: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, prefixServedByDelegation(tc.route, rows, tc.offered))
		})
	}
}

// A selected output whose entry the captured closure doesn't hold is refused,
// unless the selection asks to leave such outputs out: then it is left out,
// and the rest of the bundle is exported.
func TestExportLeavesOutOutputsOutsideTheClosureOnRequest(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	inside := persistedListTestResult(t, ctx, c, srv, "inside", &transferTestValue{Text: "pending"})
	outside := persistedListTestResult(t, ctx, c, srv, "outside", &transferTestValue{Text: "pending"})
	selection := ValueSelection{
		Roots: []AnyResult{inside},
		Outputs: []SelectedValueOutput{
			{Result: inside, Address: PersistedPartAddress{Part: "snapshot"}},
			{Result: outside, Address: PersistedPartAddress{Part: "snapshot"}},
		},
	}
	consume := func(context.Context, *ExportedValues) error { return nil }
	require.ErrorContains(t, c.WithExportedValues(ctx, selection, config.RefConfig{}, consume), "outside captured closure")

	selection.LeaveOutOutputsOutsideClosure = true
	var bundle ValueBundle
	require.NoError(t, c.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(t, bundle.Values, 1)
	require.Len(t, bundle.Outputs, 1, "only the output inside the closure")
	require.Equal(t, bundle.Values[0].Ordinal, bundle.Outputs[0].Ordinal)
}
