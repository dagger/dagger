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
	Text    string `json:"text"`
	Child   uint64 `json:"child,omitempty"`
	Recipe  string `json:"recipe,omitempty"`
	rev     atomic.Uint64
	release OnReleaseFunc
	links   []PersistedSnapshotRefLink
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
		err = c.attachPartOfferLocked(parent.cacheSharedResult(), record.Address, &partOffer{record: record, owner: owner})
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
	mapping, err := b.ImportValues(bctx, bundle)
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
	for _, bad := range []string{"dangling", "cycle", "root", "extra", "body", "unreachable"} {
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
			case "extra":
				broken.Values[0].Record.Call.ExtraDigests = []call.ExtraDigest{{Digest: digest.FromString("unmarked"), Label: call.ExtraDigestLabelContent}}
			case "body":
				broken.Values[0].Record.Envelope.Items = []PersistedResultEnvelope{{}}
			case "unreachable":
				broken.Roots[0].Ordinal = 1
			}
			before := len(b.resultsByID)
			_, err = b.ImportValues(bctx, broken)
			require.Error(t, err)
			require.Len(t, b.resultsByID, before)
		})
	}
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
		_, err := b.ImportValues(ctx, bundle)
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
			mapping, err := b.ImportValues(ctx, bundle)
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
		mapping, err := b.ImportValues(ctx, bundle)
		require.NoError(t, err)
		require.Equal(t, expiry, b.persistedEdgesByResult[sharedResultID(mapping[0].ResultID)].expiresAtUnix)
	})
}

func TestValueTransferReferencesExtras(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	marked, unmarked := digest.FromString("marked"), digest.FromString("private")
	recipe := call.New().Append(String("").Type(), "source").With(call.WithContentDigest(unmarked)).Append(String("").Type(), "child").With(call.WithExtraDigest(call.ExtraDigest{Digest: marked, Label: call.ExtraDigestLabelRemoteCache}), call.WithContentDigest(marked))
	raw, err := recipe.Encode()
	require.NoError(t, err)
	value := persistedListTestResult(t, ctx, c, srv, "recipe", &transferTestValue{Text: "recipe", Recipe: raw})
	bundle := exportTestBundle(t, ctx, c, value)
	var payload transferTestValue
	require.NoError(t, json.Unmarshal(bundle.Values[0].Record.Envelope.ObjectJSON, &payload))
	var copied call.ID
	require.NoError(t, copied.Decode(payload.Recipe))
	require.Equal(t, marked, copied.ContentDigest())
	require.Empty(t, copied.Receiver().ExtraDigests())
	require.NotEmpty(t, recipe.Receiver().ExtraDigests(), "original DAG is untouched")
	filtered, err := recipe.FilterTransferDigests()
	require.NoError(t, err)
	require.Equal(t, filtered.Digest(), copied.Digest())
	bctx, b, _ := transferTestCache(t)
	_, err = b.ImportValues(bctx, bundle)
	require.NoError(t, err)
	bundle.Values[0].Record.Envelope.ObjectJSON = json.RawMessage(`{"text":"invalid","recipe":` + string(mustTransferJSON(t, raw)) + `}`)
	_, err = b.ImportValues(bctx, bundle)
	require.ErrorContains(t, err, "unmarked recipe ID extras")
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
	mapping, err := b.ImportValues(ctx, bundle)
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
	removed, err := b.removePersistedEdge(ctx, row.id)
	require.NoError(t, err)
	require.True(t, removed)
	require.EqualValues(t, 1, rowCleanups.Load())
	require.EqualValues(t, 1, winningReleases.Load())
}

func (v *transferTestValue) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	return cloneSnapshotRefLinks(v.links)
}
