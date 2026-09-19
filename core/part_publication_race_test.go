package core

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

func TestPartTypedPublicationRoles(t *testing.T) {
	aStore, bStore := testutil.NewStore(t), testutil.NewStore(t)
	actx, a, asrv := transferCache(t, aStore, "", "a")
	bctx, b, bsrv := transferCache(t, bStore, "", "b")
	fs, _ := aStore.Build(t, nil, "payload", "fs bytes")
	meta, _ := aStore.Build(t, nil, "exitCode", "0")
	ctr := NewContainer(Platform{OS: "linux", Architecture: "amd64"})
	ctr.FS.setValue(partTestDirectory(fs, "/"))
	ctr.MetaSnapshot.setValue(meta)
	original := attachTransferObject(t, actx, a, asrv, "a", "publication", ctr)
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{original}, Outputs: []dagql.SelectedValueOutput{{Result: original, Address: dagql.PersistedPartAddress{Part: ContainerPartFS}}, {Result: original, Address: dagql.PersistedPartAddress{Part: ContainerPartExecMeta}}}}
	require.NoError(t, a.WithExportedValues(actx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		providers := partSelectingContentSource{}
		for _, entry := range exported.Chains.Entries {
			providers[string(entry.Address.Part)] = entry.Provider
		}
		b.SetPartContentSource(providers)
		imported, err := b.ImportValues(bctx, exported.Bundle)
		require.NoError(t, err)
		loaded, err := b.LoadResultByResultID(bctx, "", bsrv, imported[0].ResultID)
		require.NoError(t, err)
		result := loaded.(dagql.ObjectResult[*Container])
		raw := result.Self()
		require.Nil(t, raw.Lazy, "adapter form has no native latch")
		require.NotNil(t, raw.acquiredOutput.Load())
		var done atomic.Bool
		var reads atomic.Int64
		failures := make(chan error, 4)
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for !done.Load() {
					encoding, err := raw.EncodePersistedObject(bctx, dagql.NewPersistEncodeContext(b, imported[0].ResultID, nil))
					if err != nil {
						failures <- err
						return
					}
					var p persistedContainerPayload
					if err = json.Unmarshal(encoding.JSON, &p); err != nil {
						failures <- err
						return
					}
					v := dagql.PersistedPayloadVisit{Payload: encoding.JSON, SnapshotLinks: encoding.SnapshotLinks}
					if err = (foreignFamilyCodec("Container")).ValidateSnapshotScope(v); err != nil {
						failures <- err
						return
					}
					if _, err = (foreignFamilyCodec("Container")).DescribeParts(v); err != nil {
						failures <- err
						return
					}
					if _, err = raw.PersistedSnapshotRefLinksChecked(); err != nil {
						failures <- err
						return
					}
					_, err = b.CapturePersistedRecord(bctx, result)
					if err != nil && !errors.Is(err, dagql.ErrPersistStateNotReady) {
						failures <- err
						return
					}
					reads.Add(1)
					runtime.Gosched()
				}
			}()
		}
		for reads.Load() < 4 {
			runtime.Gosched()
		}
		require.NoError(t, b.EvaluateParts(bctx, result, ContainerPartFS, ContainerPartExecMeta))
		done.Store(true)
		wg.Wait()
		close(failures)
		for err := range failures {
			require.NoError(t, err)
		}
		require.Positive(t, reads.Load())
		require.Len(t, raw.acquiredOutput.Load().Links, 2)
		t.Logf("coherent concurrent raw reader samples=%d", reads.Load())
		return nil
	}))
}
