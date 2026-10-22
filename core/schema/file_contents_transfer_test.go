package schema

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
)

// fileContentsTestLimit is the most bytes File.contents persists.
const fileContentsTestLimit = 1 << 20

// fileContentsTestSource serves every offered chain from one provider.
type fileContentsTestSource struct{ provider content.InfoReaderProvider }

func (fileContentsTestSource) Available(dagql.PersistedPartOffer, time.Time) bool { return true }

func (s fileContentsTestSource) Provider(context.Context, dagql.PersistedPartOffer, *dagql.PartDemandState) content.InfoReaderProvider {
	return s.provider
}

// fileContentsTestFile is directory().withNewFile("go.inc", contents).file("go.inc").
func fileContentsTestFile(t *testing.T, e *transferTestEngine, ctx context.Context, contents string) dagql.ObjectResult[*core.File] {
	t.Helper()
	var file dagql.ObjectResult[*core.File]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &file,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("go.inc")},
			{Name: "contents", Value: dagql.NewString(contents)},
		}},
		dagql.Selector{Field: "file", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("go.inc")},
		}}))
	return file
}

// fileContentsTestRead is file.contents, reading limitLines lines when it is
// positive.
func fileContentsTestRead(t *testing.T, e *transferTestEngine, ctx context.Context, file dagql.ObjectResult[*core.File], limitLines int) dagql.AnyResult {
	t.Helper()
	sel := dagql.Selector{Field: "contents"}
	if limitLines > 0 {
		sel.Args = []dagql.NamedInput{{Name: "limitLines", Value: dagql.Opt(dagql.NewInt(limitLines))}}
	}
	var res dagql.AnyResult
	require.NoError(t, e.dag.Select(ctx, file, &res, sel))
	return res
}

func fileContentsTestValue(t *testing.T, res dagql.AnyResult) string {
	t.Helper()
	value, ok := res.Unwrap().(dagql.String)
	require.True(t, ok, "contents is a String, got %T", res.Unwrap())
	return value.String()
}

// File.contents persists what it returns up to fileContentsTestLimit bytes,
// so a later session on the engine hits it, and another engine that merges it
// hits it without reading the file: no chain of the file's is read, even
// when its part is offered, and the file is never evaluated. That is a
// check's read of a small file out of a large container, such as lint's
// include lists out of its helper's rootfs, on a fresh engine. A read that
// returns more stays unpersisted, as before: a hit only while the session
// that made it is open, never kept by a later hit. The limit is on the
// returned bytes, so a short slice of a large file persists.
func TestFileContentsPersistsUpToItsLimit(t *testing.T) {
	small := "module lint\n"
	large := strings.Repeat("x", fileContentsTestLimit)
	for _, tc := range []struct {
		name       string
		contents   string
		limitLines int
		want       string
		persisted  bool
	}{
		{name: "a small file", contents: small, want: small, persisted: true},
		{name: "a file at the limit", contents: large, want: large, persisted: true},
		{name: "a file over the limit", contents: large + "y", want: large + "y", persisted: false},
		{name: "a short slice of a file over the limit", contents: small + large, limitLines: 1, want: small, persisted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			salt := transferTestSalt(t)
			a := newTransferTestEngine(t, salt)

			// The read, then the same read in another session while the
			// first is open: a hit either way, as today.
			a1 := a.session(t, "a1")
			first := fileContentsTestRead(t, a, a1, fileContentsTestFile(t, a, a1, tc.contents), tc.limitLines)
			require.False(t, first.HitCache(), "the first read runs")
			require.Equal(t, tc.want, fileContentsTestValue(t, first))
			a2 := a.session(t, "a2")
			second := fileContentsTestRead(t, a, a2, fileContentsTestFile(t, a, a2, tc.contents), tc.limitLines)
			require.True(t, second.HitCache(), "a second session hits while the first is open")
			require.Equal(t, tc.want, fileContentsTestValue(t, second))

			// Both sessions end, as a check does. Only a persisted read is
			// still there for the next session.
			for _, session := range []string{"a1-session", "a2-session"} {
				require.NoError(t, a.cache.ReleaseSession(a.base, session))
				require.NoError(t, a.cache.WaitSessionRelease(a.base, session))
			}
			a3 := a.session(t, "a3")
			fileA := fileContentsTestFile(t, a, a3, tc.contents)
			third := fileContentsTestRead(t, a, a3, fileA, tc.limitLines)
			require.Equal(t, tc.want, fileContentsTestValue(t, third))
			if !tc.persisted {
				require.False(t, third.HitCache(), "a read over the limit isn't kept after its sessions end")
				return
			}
			require.True(t, third.HitCache(), "a read within the limit is kept after its sessions end")

			// Another engine merges the read with the file's own part
			// offered, as a selection that brings a root's filesystem
			// dependencies does, and makes the same read.
			require.NoError(t, a.cache.Evaluate(a3, fileA))
			b := newTransferTestEngine(t, salt)
			b1 := b.session(t, "b1")
			selection := dagql.ValueSelection{
				Roots:   []dagql.AnyResult{third},
				Outputs: []dagql.SelectedValueOutput{{Result: fileA, Address: dagql.PersistedPartAddress{Part: "snapshot"}}},
			}
			require.NoError(t, a.cache.WithExportedValues(a3, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
				require.Len(t, exported.Chains.Entries, 1, "the file's chain is offered")
				provider := &testutil.Provider{InfoReaderProvider: exported.Chains.Entries[0].Provider}
				b.cache.SetPartContentSource(fileContentsTestSource{provider})
				reply, err := b.cache.MergeValues(b1, dagql.CloudCacheID, exported.Bundle)
				require.NoError(t, err)
				require.Len(t, reply.Imported(), 1, "the read lands on the other engine")

				fileB := fileContentsTestFile(t, b, b1, tc.contents)
				onB := fileContentsTestRead(t, b, b1, fileB, tc.limitLines)
				require.True(t, onB.HitCache(), "the other engine hits the merged read")
				require.Equal(t, tc.want, fileContentsTestValue(t, onB))
				require.Zero(t, provider.Reads.Load(), "no chain of the file's is read")
				require.True(t, dagql.HasPendingLazyComputation(fileB), "the file is never evaluated, so none of its parts is installed")
				return nil
			}))
		})
	}
}
