package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

// BenchmarkExportRecordChecks times a records-only export of a closure of
// core's values, every record of which goes in full and is checked: a chain
// of 40 containers, each built on the one before, each with its own
// filesystem, two directories mounted, a dozen environment variables and a
// working directory. It reports the records, the export's time, and the
// share of it its records' checks took (ExportPhases.BundleCheck), for a CPU
// profile to split between the check's steps: the walk of a record's
// references, its normalization, and its validation.
func BenchmarkExportRecordChecks(b *testing.B) {
	store := testutil.NewStore(b)
	ctx, cache, srv := transferCache(b, store, "", "a")
	platform := Platform{OS: "linux", Architecture: "amd64"}
	newDir := func(ref bkcache.ImmutableRef) *Directory {
		dir := &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Platform: platform}
		dir.SetPath("/")
		dir.SetSnapshot(ref)
		return dir
	}
	var snapshot bkcache.ImmutableRef
	var step dagql.ObjectResult[*Container]
	for i := range 40 {
		snapshot, _ = store.Build(b, snapshot, fmt.Sprintf("check-%d.txt", i), fmt.Sprintf("check %d", i))
		src, _ := store.Build(b, nil, fmt.Sprintf("src-%d/main.go", i), fmt.Sprintf("package main // %d", i))
		cachedir, _ := store.Build(b, nil, fmt.Sprintf("gocache-%d/x", i), "cache")
		ctr := NewContainer(platform)
		ctr.FS.setValue(newDir(snapshot))
		srcMount := new(LazyAccessor[*Directory, *Container])
		srcMount.setValue(newDir(src))
		toolMount := new(LazyAccessor[*Directory, *Container])
		toolMount.setValue(newDir(cachedir))
		ctr.Mounts = ContainerMounts{{Target: "/src", DirectorySource: srcMount}, {Target: "/gobuild/tools", DirectorySource: toolMount}}
		ctr.Config.WorkingDir = "/src"
		for e := range 12 {
			ctr.Config.Env = append(ctr.Config.Env, fmt.Sprintf("VAR_%d_%d=/go/pkg/mod/github.com/dagger/dagger@v0.0.0-%d", i, e, e))
		}
		if i == 0 {
			step = attachTransferObject(b, ctx, cache, srv, "a", "checkBase", ctr)
		} else {
			step = attachDelegationChild(b, ctx, cache, srv, "a", "checkStep", step, ctr)
		}
	}
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{step}}
	var records int
	var check time.Duration
	b.ResetTimer()
	for b.Loop() {
		require.NoError(b, cache.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, ex *dagql.ExportedValues) error {
			records = len(ex.Bundle.Values)
			check += ex.Phases.BundleCheck
			return nil
		}))
	}
	b.ReportMetric(float64(records), "records")
	b.ReportMetric(float64(check.Microseconds())/float64(b.N)/float64(records), "check-us/record")
}
