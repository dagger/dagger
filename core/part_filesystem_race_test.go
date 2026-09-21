package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/stretchr/testify/require"
)

func TestPartFilesystemPublicationRoles(t *testing.T) {
	for _, family := range []string{"File", "Directory"} {
		t.Run(family, func(t *testing.T) {
			store := testutil.NewStore(t)
			ctx, cache, srv := transferCache(t, store, "", "publication")
			srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Service]{}))
			service := attachTransferObject(t, ctx, cache, srv, "publication", "boundService", &Service{CustomHostname: "fixture"})
			refs := make([]bkcache.ImmutableRef, 2)
			for i := range refs {
				refs[i], _ = store.Build(t, nil, fmt.Sprintf("v%d", i), "coherent tuple")
			}
			platforms := []Platform{{OS: "linux", Architecture: "amd64"}, {OS: "linux", Architecture: "arm64"}}
			services := []ServiceBindings{{{Service: service, Hostname: "old"}}, {{Service: service, Hostname: "new"}}}
			var value interface {
				dagql.PersistedObject
				dagql.PersistedOutputVersion
				PersistedSnapshotRefLinksChecked() ([]dagql.PersistedSnapshotRefLink, error)
			}
			var storeNext func(int) dagql.PreparedPartStore
			if family == "File" {
				file := &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Platform: platforms[0], Services: services[0]}
				file.SetPath("/v0")
				file.SetSnapshot(refs[0])
				value = file
				storeNext = func(i int) dagql.PreparedPartStore {
					return &filePartStore{receiver: file, next: &File{Platform: platforms[i], Services: services[i]}, expected: file.OutputRev, path: fmt.Sprintf("/v%d", i), ref: refs[i]}
				}
			} else {
				dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platforms[0], Services: services[0]}
				dir.SetPath("/v0")
				dir.SetSnapshot(refs[0])
				value = dir
				storeNext = func(i int) dagql.PreparedPartStore {
					return &directoryPartStore{receiver: dir, next: &Directory{Platform: platforms[i], Services: services[i]}, expected: dir.OutputRev, path: fmt.Sprintf("/v%d", i), ref: refs[i]}
				}
			}
			var done atomic.Bool
			var samples atomic.Int32
			failures := make(chan error, 4)
			var wg sync.WaitGroup
			for range 4 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for !done.Load() {
						before, err := value.PersistedOutputRevision()
						if errors.Is(err, dagql.ErrPersistStateNotReady) {
							runtime.Gosched()
							continue
						}
						if err != nil {
							failures <- err
							return
						}
						encoded, err := value.EncodePersistedObject(ctx, dagql.NewPersistEncodeContext(cache, 0, nil))
						if errors.Is(err, dagql.ErrPersistStateNotReady) {
							continue
						}
						if err != nil {
							failures <- err
							return
						}
						var p struct {
							File     string                    `json:"file"`
							Dir      string                    `json:"dir"`
							Platform Platform                  `json:"platform"`
							Services []persistedServiceBinding `json:"services"`
						}
						if err := json.Unmarshal(encoded.JSON, &p); err != nil {
							failures <- err
							return
						}
						path := p.File
						if family == "Directory" {
							path = p.Dir
						}
						i := 0
						if path == "/v1" {
							i = 1
						}
						if path != fmt.Sprintf("/v%d", i) || p.Platform.Architecture != platforms[i].Architecture || len(p.Services) != 1 || p.Services[0].Hostname != services[i][0].Hostname || len(encoded.SnapshotLinks) != 1 || encoded.SnapshotLinks[0].RefKey != refs[i].SnapshotID() {
							failures <- fmt.Errorf("incoherent %s tuple: %s links=%v", family, encoded.JSON, encoded.SnapshotLinks)
							return
						}
						links, err := value.PersistedSnapshotRefLinksChecked()
						if errors.Is(err, dagql.ErrPersistStateNotReady) {
							continue
						}
						if err != nil {
							failures <- err
							return
						}
						after, err := value.PersistedOutputRevision()
						if errors.Is(err, dagql.ErrPersistStateNotReady) {
							continue
						}
						if err != nil {
							failures <- err
							return
						}
						if before == after && (len(links) != 1 || links[0].RefKey != encoded.SnapshotLinks[0].RefKey) {
							failures <- fmt.Errorf("%s role reader disagreed at revision %d", family, before)
							return
						}
						samples.Add(1)
					}
				}()
			}
			for range 1024 {
				for i := range 2 {
					next := storeNext(i)
					for !next.TryLock() {
						runtime.Gosched()
					}
					next.Publish()
					next.Unlock()
					runtime.Gosched()
				}
			}
			done.Store(true)
			wg.Wait()
			close(failures)
			for err := range failures {
				require.NoError(t, err)
			}
			require.Positive(t, samples.Load())
			t.Logf("%s coherent tuple/role samples=%d publications=2048", family, samples.Load())
		})
	}
}
