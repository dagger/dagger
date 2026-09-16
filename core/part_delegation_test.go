package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestPartDelegationPureRoutes(t *testing.T) {
	fields := []string{"withWorkdir", "withEnvVariable", "withoutDefaultArgs", "__withSystemEnvVariable", "withEntrypoint", "withMountedCache", "withoutMount", "withUnixSocket", "withoutUnixSocket", "withoutEnvVariable"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			payload := persistedContainerPayload{Metadata: persistedContainerMetadata{Consumed: true, Value: persistedContainerMetadataValue{Platform: Platform{OS: "linux", Architecture: "amd64"}, Mounts: []persistedContainerMountPayload{{Target: "/cache", Kind: persistedContainerMountKindCache, CacheSourceResultID: 9}, {Target: "/kept", Kind: persistedContainerMountKindFile}}}}, Parts: map[dagql.PartKey]persistedContainerPart{
				"fs": {Kind: containerPartPending}, "execMeta": {Kind: containerPartPending}, "mount:/kept": {Kind: containerPartPending, ValueKind: containerPartFile, Role: "mount_file:1"},
			}}
			raw, err := json.Marshal(payload)
			require.NoError(t, err)
			frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType((&Container{}).Type()), Receiver: &dagql.ResultCallRef{ResultID: 42}}
			visit := dagql.PersistedPayloadVisit{Call: frame, Payload: raw, Path: dagql.PersistedRefPath{}.Field("items").Index(3)}
			for _, part := range []dagql.PartKey{"fs", "execMeta", "mount:/kept"} {
				route, err := foreignFamilyCodec("Container").RouteParts(visit, part)
				require.NoError(t, err)
				require.False(t, route.HasProducer)
				require.Empty(t, route.WriteSet)
				require.Equal(t, visit.Path, route.Group.OutputPath)
				require.Equal(t, &dagql.PartDelegation{ParentResultID: 42, Address: dagql.PersistedPartAddress{Part: part}}, route.Delegation)
			}
			route, err := foreignFamilyCodec("Container").RouteParts(visit, "metadata")
			require.NoError(t, err)
			require.Nil(t, route.Delegation)
			for _, part := range []dagql.PartKey{"mount:/cache", "mount:/removed"} {
				_, err = foreignFamilyCodec("Container").RouteParts(visit, part)
				require.Error(t, err)
			}
			frame.Receiver = nil
			_, err = foreignFamilyCodec("Container").RouteParts(visit, "fs")
			require.ErrorContains(t, err, "exact receiver")
			frame.Receiver = &dagql.ResultCallRef{Call: &dagql.ResultCall{Field: "container"}}
			_, err = foreignFamilyCodec("Container").RouteParts(visit, "fs")
			require.ErrorContains(t, err, "exact receiver")
			frame.Module = &dagql.ResultCallModule{Name: "user-defined"}
			route, err = foreignFamilyCodec("Container").RouteParts(visit, "fs")
			require.NoError(t, err)
			require.Nil(t, route.Delegation)
			frame.Module = nil
			frame.Nth = 1
			route, err = foreignFamilyCodec("Container").RouteParts(visit, "fs")
			require.NoError(t, err)
			require.Nil(t, route.Delegation)
			frame.Nth = 0
			frame.Field = "unassessedField"
			route, err = foreignFamilyCodec("Container").RouteParts(visit, "fs")
			require.NoError(t, err)
			require.Nil(t, route.Delegation)
		})
	}
}

func attachDelegationChild(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session, field string, parent dagql.ObjectResult[*Container], value *Container) dagql.ObjectResult[*Container] {
	t.Helper()
	id, err := cache.PersistedResultID(parent)
	require.NoError(t, err)
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Receiver: &dagql.ResultCallRef{ResultID: id}, Type: dagql.NewResultCallType(value.Type())}
	result, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) { return dagql.NewObjectResultForCall(value, srv, frame) })
	require.NoError(t, err)
	return result.(dagql.ObjectResult[*Container])
}

func TestPartDelegationRealStore(t *testing.T) {
	for _, mode := range []string{"ready-parent", "pending-parent", "sync-retry", "native-restart", "ready-parent-over-chain", "ordinary-ready-tie", "child-chain-first", "late-child"} {
		t.Run(mode, func(t *testing.T) {
			aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
			actx, a, asrv := transferCache(t, aStore, "", "a")
			ref, _ := aStore.Build(t, nil, "payload", "delegated bytes")
			base := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
			base.FS.setValue(partTestDirectory(ref, "/"))
			parent := attachTransferObject(t, actx, a, asrv, "a", "delegationBase", base)
			child := NewContainer(base.Platform)
			child.FS, _ = CloneContainerDirectoryAccessor(actx, base.FS)
			child.Config.WorkingDir = "/child"
			child.Config.Env = []string{"KEEP=child"}
			result := attachDelegationChild(t, actx, a, asrv, "a", "withWorkdir", parent, child)
			if mode == "native-restart" {
				for _, depth := range []int{1, 8, 32} {
					t.Run(fmt.Sprint(depth), func(t *testing.T) { testNativeDelegationRestart(t, aStore, ref.SnapshotID(), depth) })
				}
				return
			}
			observed := &partObservedManager{SnapshotManager: bStore.Manager}
			bStore.Manager = observed
			bctx, b, bsrv := transferCache(t, bStore, filepath.Join(t.TempDir(), "b.db"), "b")
			b.EnableTransferFixtureParts()
			if mode != "pending-parent" && mode != "child-chain-first" && mode != "late-child" {
				localRef, _ := bStore.Build(t, nil, "payload", "delegated bytes")
				local := NewContainer(base.Platform)
				local.FS.setValue(partTestDirectory(localRef, "/"))
				attachTransferObject(t, bctx, b, bsrv, "b", "delegationBase", local)
			}
			selection := dagql.ValueSelection{Roots: []dagql.AnyResult{result}, Outputs: []dagql.SelectedValueOutput{{Result: parent, Address: dagql.PersistedPartAddress{Part: "fs"}}}}
			if mode == "ready-parent-over-chain" || mode == "child-chain-first" {
				selection.Outputs = append(selection.Outputs, dagql.SelectedValueOutput{Result: result, Address: dagql.PersistedPartAddress{Part: "fs"}})
			}
			require.NoError(t, a.WithExportedValues(actx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
				provider := &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}
				b.SetPartContentSource(partTestContentSource{provider})
				imported, err := b.ImportValues(bctx, exported.Bundle)
				require.NoError(t, err)
				loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
				require.NoError(t, err)
				got := loaded.(dagql.ObjectResult[*Container])
				frame, err := b.ResultCallByResultID(bctx, "", imported[0].ResultID)
				require.NoError(t, err)
				parentID := frame.Receiver.ResultID
				parentHolds := func() int64 {
					for _, row := range b.DebugEGraphSnapshot().Results {
						if row.SharedResultID == parentID {
							return row.IncomingOwnershipCount
						}
					}
					t.Fatal("exact parent disappeared")
					return 0
				}
				if mode == "ready-parent" || mode == "sync-retry" || mode == "ready-parent-over-chain" || mode == "ordinary-ready-tie" {
					exact, err := b.LoadResultByResultID(bctx, "", bsrv, parentID)
					require.NoError(t, err)
					require.NoError(t, b.EvaluateParts(bctx, exact, ContainerPartFS))
				}
				addDonor := func() {
					ref, _ := bStore.Build(t, nil, "payload", "delegated bytes")
					donor := NewContainer(base.Platform)
					donor.FS.setValue(partTestDirectory(ref, "/"))
					row := attachTransferObject(t, bctx, b, bsrv, "b", "ordinaryDonor", donor)
					require.NoError(t, b.TeachCallEquivalentToResult(bctx, "b", frame, row))
				}
				if mode == "ordinary-ready-tie" {
					addDonor()
				}
				require.NoError(t, b.EvaluateParts(bctx, got, ContainerPartMetadata))
				require.Zero(t, provider.Reads.Load())
				if mode == "late-child" {
					started, release := make(chan struct{}), make(chan struct{})
					var once sync.Once
					provider.BeforeRead = func(ctx context.Context, _ ocispec.Descriptor) error {
						once.Do(func() { close(started) })
						select {
						case <-release:
							return nil
						case <-ctx.Done():
							return context.Cause(ctx)
						}
					}
					done := make(chan error, 1)
					go func() { done <- b.EvaluateParts(bctx, got, ContainerPartFS) }()
					select {
					case <-started:
					case <-time.After(10 * time.Second):
						t.Fatal("parent did not start")
					}
					addDonor()
					require.NoError(t, b.RunLazyTask(bctx, got, "obtain:fs", dagql.LazyTaskSpec{Body: func(ctx context.Context) error {
						address := dagql.PersistedPartAddress{Part: "fs"}
						source, err := b.AcquireEquivalentPartSource(ctx, got, address)
						if err != nil {
							return err
						}
						require.NotNil(t, source)
						permit, outcome, err := b.TryAcquire(ctx, got, address, dagql.PartTaskFromContext(ctx))
						if err != nil {
							return err
						}
						require.Equal(t, dagql.GateGranted, outcome)
						return b.InstallReadyPart(ctx, got, source, permit)
					}}))
					select {
					case err := <-done:
						t.Fatalf("waiter returned before parent completed: %v", err)
					case <-time.After(100 * time.Millisecond):
					}
					close(release)
					require.NoError(t, <-done)
				} else if mode == "sync-retry" {
					before, pins, releases := parentHolds(), observed.pins.Load(), observed.pinReleaseAttempts.Load()
					observed.failOwner.Store(true)
					require.ErrorIs(t, b.EvaluateParts(bctx, got, ContainerPartFS), partInjectedOwnerFailure)
					require.Equal(t, before, parentHolds(), "temporary parent hold survived failed child sync")
					require.Equal(t, pins+1, observed.pins.Load())
					require.Equal(t, releases, observed.pinReleaseAttempts.Load(), "child pin must survive failed sync")
				}
				require.NoError(t, b.EvaluateParts(bctx, got, ContainerPartFS))
				require.Equal(t, "/child", got.Self().Config.WorkingDir)
				require.Equal(t, []string{"KEEP=child"}, got.Self().Config.Env)
				dir, ok := got.Self().FS.Peek()
				require.True(t, ok)
				snapshot, ok := dir.Snapshot.Peek()
				require.True(t, ok)
				testutil.CheckFile(t, snapshot, "payload", "delegated bytes")
				report, err := b.TransferFixtureSnapshot(bctx, "b", nil)
				require.NoError(t, err)
				installs := 0
				for _, event := range report.Parts {
					if event.Kind == "installed-delegation" {
						installs++
						require.Equal(t, imported[0].ResultID, event.ResultID)
						require.NotNil(t, event.Source)
						require.Equal(t, dagql.PersistedPartAddress{Part: "fs"}, event.Source.Address)
					} else if event.Kind != "selected-delegation" {
						require.Nil(t, event.Source)
					}
				}
				if mode == "ordinary-ready-tie" || mode == "child-chain-first" || mode == "late-child" {
					require.Zero(t, installs)
				} else {
					require.Equal(t, 1, installs)
				}
				if mode == "pending-parent" || mode == "child-chain-first" || mode == "late-child" {
					require.Positive(t, provider.Reads.Load())
				} else {
					require.Zero(t, provider.Reads.Load())
				}
				return nil
			}))
		})
	}
}

func testNativeDelegationRestart(t *testing.T, store *testutil.Store, snapshotID string, depth int) {
	path := filepath.Join(t.TempDir(), "native.db")
	ctx, cache, srv := transferCache(t, store, path, "native")
	ref, err := store.Manager.GetBySnapshotID(ctx, snapshotID)
	require.NoError(t, err)
	base := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
	base.FS.setValue(partTestDirectory(ref, "/"))
	parent := attachTransferObject(t, ctx, cache, srv, "native", "nativeParent", base)
	payload := persistedContainerPayload{Metadata: persistedContainerMetadata{Consumed: true, Value: persistedContainerMetadataValue{Platform: base.Platform}}, Parts: map[dagql.PartKey]persistedContainerPart{"fs": {Kind: containerPartPending}, "execMeta": {Kind: containerPartAbsent}}}
	child := parent
	for range depth {
		value := NewContainer(base.Platform)
		value.acquiredOutput.Store(&containerAcquiredOutput{Payload: payload, Revision: 1})
		child = attachDelegationChild(t, ctx, cache, srv, "native", "withWorkdir", child, value)
	}
	id, err := cache.PersistedResultID(child)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, "native"))
	require.NoError(t, cache.Close(ctx))
	store.Reload(t)
	observed := &partObservedManager{SnapshotManager: store.Manager}
	store.Manager = observed
	ctx, cache, srv = transferCache(t, store, path, "reopened")
	cache.EnableTransferFixtureParts()
	require.Zero(t, observed.bodies.Load())
	loaded, err := cache.LoadResultByResultID(ctx, "", srv, id)
	require.NoError(t, err)
	report, err := cache.TransferFixtureSnapshot(ctx, "reopened", nil)
	require.NoError(t, err)
	for _, row := range report.Rows {
		require.False(t, row.Imported)
	}
	started := time.Now()
	observed.ownerAttempts.Store(0)
	observed.accessorRefs.Store(0)
	require.NoError(t, cache.EvaluateParts(ctx, loaded, ContainerPartFS))
	t.Logf("native delegation depth=%d bookkeeping latency=%s pins=%d owner-syncs=%d independent-accessor-refs=%d", depth, time.Since(started), observed.pins.Load(), observed.ownerAttempts.Load(), observed.accessorRefs.Load())
	require.EqualValues(t, depth, observed.pins.Load())
	report, err = cache.TransferFixtureSnapshot(ctx, "reopened", nil)
	require.NoError(t, err)
	installs := 0
	for _, event := range report.Parts {
		if event.Kind == "installed-delegation" {
			installs++
		}
	}
	require.Equal(t, depth, installs)
	record, err := cache.CapturePersistedRecord(ctx, loaded)
	require.NoError(t, err)
	require.Equal(t, snapshotID, record.SnapshotLinks[0].RefKey)
	require.Zero(t, observed.bodies.Load())
	require.Equal(t, dagql.CachePersistenceResetNone, cache.PersistenceResetReason(), fmt.Sprint(cache.PersistenceResetReason()))
}
