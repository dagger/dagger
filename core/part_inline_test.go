package core

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

type inlineObservedRef struct {
	bkcache.ImmutableRef
	releases atomic.Int64
}

func (r *inlineObservedRef) Release(ctx context.Context) error {
	r.releases.Add(1)
	return r.ImmutableRef.Release(ctx)
}

type inlineObservedManager struct{ bkcache.SnapshotManager }

func (m inlineObservedManager) GetBySnapshotID(ctx context.Context, id string, opts ...bkcache.RefOption) (bkcache.ImmutableRef, error) {
	ref, err := m.SnapshotManager.GetBySnapshotID(ctx, id, opts...)
	if err != nil {
		return nil, err
	}
	return &inlineObservedRef{ImmutableRef: ref}, nil
}

type partInlineSources map[int]*testutil.Provider

func (s partInlineSources) Provider(_ context.Context, offer dagql.PersistedPartOffer, _ *dagql.PartDemandState) content.InfoReaderProvider {
	if len(offer.Address.OutputPath) == 0 {
		return s[-1]
	}
	return s[offer.Address.OutputPath[1].Index]
}
func TestPartInlineAddress(t *testing.T) {
	for _, name := range []string{"sequential", "concurrent", "shared-snapshot-sync-retry"} {
		concurrent := name == "concurrent"
		shared := name == "shared-snapshot-sync-retry"
		t.Run(name, func(t *testing.T) {
			aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
			actx, a, asrv := transferCache(t, aStore, filepath.Join(t.TempDir(), "a.db"), "a")
			db := filepath.Join(t.TempDir(), "b.db")
			observedManager := &partObservedManager{SnapshotManager: bStore.Manager}
			bStore.Manager = observedManager
			bctx, b, bsrv := transferCache(t, bStore, db, "b")
			first, _ := aStore.Build(t, nil, "one/data", "first bytes")
			second, _ := aStore.Build(t, nil, "two/data", "second bytes")
			if shared {
				first, _ = aStore.Build(t, first, "two/data", "second bytes")
				var err error
				second, err = aStore.Manager.GetBySnapshotID(actx, first.SnapshotID())
				require.NoError(t, err)
			}
			childRef, _ := aStore.Build(t, nil, "child/data", "separate child")
			child := attachTransferObject(t, actx, a, asrv, "a", "separateInlineChild", partTestDirectory(childRef, "/child"))
			childID, err := a.PersistedResultID(child)
			require.NoError(t, err)
			firstValue, err := dagql.NewResultForCall(partTestDirectory(first, "/one"), &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "first", Type: dagql.NewResultCallType((*Directory)(nil).Type())})
			require.NoError(t, err)
			secondValue, err := dagql.NewResultForCall(partTestDirectory(second, "/two"), &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "second", Type: dagql.NewResultCallType((*Directory)(nil).Type())})
			require.NoError(t, err)
			values := dagql.ResultArray[*Directory]{firstValue, secondValue, child.Result}
			frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "inlineDirectories", Type: dagql.NewResultCallType(values.Type())}
			frame.ImplicitInputs = []*dagql.ResultCallArg{{Name: "separateChild", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindResultRef, ResultRef: &dagql.ResultCallRef{ResultID: childID}}}}
			original, err := a.GetOrInitCall(actx, "a", asrv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) { return dagql.NewResultForCall(values, frame) })
			require.NoError(t, err)
			selection := dagql.ValueSelection{Roots: []dagql.AnyResult{original}}
			for i := range 2 {
				selection.Outputs = append(selection.Outputs, dagql.SelectedValueOutput{Result: original, Address: dagql.PersistedPartAddress{OutputPath: dagql.PersistedRefPath{}.Field("items").Index(i), Part: "snapshot"}})
			}
			selection.Outputs = append(selection.Outputs, dagql.SelectedValueOutput{Result: child, Address: dagql.PersistedPartAddress{Part: "snapshot"}})
			require.NoError(t, a.WithExportedValues(actx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
				require.Len(t, exported.Chains.Entries, 3)
				sources := partInlineSources{}
				for _, entry := range exported.Chains.Entries {
					index := -1
					if len(entry.Address.OutputPath) != 0 {
						index = entry.Address.OutputPath[1].Index
					}
					sources[index] = &testutil.Provider{InfoReaderProvider: entry.Provider}
				}
				b.SetPartContentSource(sources)
				imported, err := b.ImportValues(bctx, exported.Bundle)
				require.NoError(t, err)
				loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
				require.NoError(t, err)
				items := loaded.Unwrap().(dagql.DynamicResultArrayOutput).Values
				dirs := make([]*Directory, 2)
				for i, item := range items[:2] {
					dirs[i], _ = dagql.UnwrapAs[*Directory](item)
					require.NotNil(t, dirs[i])
					require.Equal(t, imported[0].ResultID, dirs[i].partHost.Load().DecodeContext(bctx).SnapshotScope().OwnerResultID)
				}
				if concurrent {
					var wg sync.WaitGroup
					errs := make([]error, 2)
					for i := range 2 {
						wg.Go(func() { errs[i] = dirs[i].LazyEvalFunc()(bctx) })
					}
					wg.Wait()
					for _, err := range errs {
						require.NoError(t, err)
					}
				} else {
					require.NoError(t, dirs[0].LazyEvalFunc()(bctx))
					require.Greater(t, sources[0].Reads.Load(), int64(0))
					require.Zero(t, sources[1].Reads.Load())
					record, err := b.CapturePersistedRecord(bctx, loaded)
					require.NoError(t, err)
					require.Len(t, record.SnapshotLinks, 1)
					require.Len(t, record.Envelope.PendingOffers, 1)
					require.Equal(t, dagql.PersistedRefPath{}.Field("items").Index(0), record.SnapshotLinks[0].OutputPath)
					if shared {
						observedManager.failOwner.Store(true)
						require.ErrorIs(t, dirs[1].LazyEvalFunc()(bctx), errPartInjectedOwner)
						_, err := b.CapturePersistedRecord(bctx, loaded)
						require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
						reads := sources[0].Reads.Load() + sources[1].Reads.Load()
						require.NoError(t, dirs[1].LazyEvalFunc()(bctx))
						require.Equal(t, reads, sources[0].Reads.Load()+sources[1].Reads.Load())
					} else {
						require.NoError(t, dirs[1].LazyEvalFunc()(bctx))
					}
				}
				record, err := b.CapturePersistedRecord(bctx, loaded)
				require.NoError(t, err)
				require.Len(t, record.SnapshotLinks, 2)
				if shared {
					require.Equal(t, record.SnapshotLinks[0].RefKey, record.SnapshotLinks[1].RefKey)
				}
				require.Empty(t, record.Envelope.PendingOffers)
				require.Zero(t, sources[-1].Reads.Load())
				require.NotZero(t, record.Envelope.Items[2].ResultID)
				for _, env := range record.Envelope.Items[:2] {
					require.Zero(t, env.ResultID)
				}
				for i, dir := range dirs {
					ref, _ := dir.Snapshot.Peek()
					want := "first bytes"
					path := "one/data"
					if i == 1 {
						want = "second bytes"
						path = "two/data"
					}
					testutil.CheckFile(t, ref, path, want)
				}
				require.NoError(t, b.ReleaseSession(bctx, "b"))
				require.NoError(t, b.Close(bctx))
				bStore.Reload(t)
				bStore.Manager = inlineObservedManager{bStore.Manager}
				bctx, b, bsrv = transferCache(t, bStore, db, "restored")
				require.Equal(t, dagql.CachePersistenceResetNone, b.PersistenceResetReason())
				loaded, err = b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
				require.NoError(t, err)
				restored := loaded.Unwrap().(dagql.DynamicResultArrayOutput).Values
				for i, item := range restored[:2] {
					dir, _ := dagql.UnwrapAs[*Directory](item)
					require.NotNil(t, dir)
					_, open := dir.Snapshot.Peek()
					require.False(t, open)
					require.NoError(t, dir.partHost.Load().Evaluate(bctx, "snapshot"))
					ref, _ := dir.Snapshot.Peek()
					want := "first bytes"
					path := "one/data"
					if i == 1 {
						want = "second bytes"
						path = "two/data"
					}
					testutil.CheckFile(t, ref, path, want)
				}
				// Public selection reuses the inline object. Releasing its session must
				// leave the persisted list's accessor and host alive for another selection.
				var selected dagql.ObjectResult[*Directory]
				dagql.Fields[*Query]{dagql.NodeFunc("borrowInline", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Directory], error) {
					item, err := loaded.NthValue(ctx, 1)
					if err != nil {
						return dagql.ObjectResult[*Directory]{}, err
					}
					return item.(dagql.ObjectResult[*Directory]), nil
				})}.Install(bsrv)
				originalHost := restored[0].Unwrap().(*Directory).partHost.Load()
				for _, session := range []string{"selection1", "selection2"} {
					ctx := engine.ContextWithClientMetadata(bctx, &engine.ClientMetadata{SessionID: session, ClientID: session})
					require.NoError(t, bsrv.Select(ctx, bsrv.Root(), &selected, dagql.Selector{Field: "borrowInline"}))
					require.Same(t, restored[0].Unwrap(), selected.Self())
					require.Same(t, originalHost, selected.Self().partHost.Load())
					require.NoError(t, b.Evaluate(ctx, selected))
					ref, _ := selected.Self().Snapshot.Peek()
					testutil.CheckFile(t, ref, "one/data", "first bytes")
					require.NoError(t, b.ReleaseSession(ctx, session))
					require.NoError(t, b.WaitSessionRelease(ctx, session))
					testutil.CheckFile(t, ref, "one/data", "first bytes")
				}
				selectedOnly := dagql.ValueSelection{Roots: []dagql.AnyResult{loaded}, Outputs: []dagql.SelectedValueOutput{{Result: loaded, Address: selection.Outputs[1].Address}}}
				require.NoError(t, b.WithExportedValues(bctx, selectedOnly, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, forward *dagql.ExportedValues) error {
					require.Len(t, forward.Chains.Entries, 1)
					require.Equal(t, selection.Outputs[1].Address, forward.Chains.Entries[0].Address)
					for _, value := range forward.Bundle.Values {
						require.Empty(t, value.Record.SnapshotLinks)
					}
					return nil
				}))
				observed := make([]*inlineObservedRef, 2)
				for i, item := range restored[:2] {
					dir, _ := dagql.UnwrapAs[*Directory](item)
					ref, _ := dir.Snapshot.Peek()
					observed[i] = ref.(*inlineObservedRef)
					require.Zero(t, observed[i].releases.Load())
				}
				require.NoError(t, b.ReleaseSession(bctx, "restored"))
				_, err = b.Prune(bctx, []dagql.CachePrunePolicy{{All: true}})
				require.NoError(t, err)
				for _, ref := range observed {
					require.EqualValues(t, 1, ref.releases.Load(), "only final enclosing-row cleanup releases an inline accessor")
				}
				return nil
			}))
		})
	}
}

func (partInlineSources) Available(dagql.PersistedPartOffer, time.Time) bool { return true }
