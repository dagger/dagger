package core

import (
	"context"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/internal/buildkit/identity"
)

// Changesets and directory diffs between two snapshots of one lineage walk
// only the overlay layers separating them. These cases build those layers for
// real, with execs and Directory operations on overlayfs, so whiteouts,
// opaque directories and type changes come from the kernel and the engine
// rather than from a model of them. Each case requires the changes the edits
// made, an application of the changeset that reproduces After, a diff with
// the changed files, and that every one of those comparisons took the layer
// walk instead of falling back to walking both trees.
func (ChangesetSuite) TestLayerWalks(ctx context.Context, t *testctx.T) {
	// The base tree every container case starts from. Every file's content is
	// unique, so git never pairs an added and a removed file as a rename.
	baseScript := `set -e
mkdir -p /src && cd /src
put() { mkdir -p "$(dirname "$1")"; printf '%s\n' "$1 $2" > "$1"; }
put keep.txt base
put deep/er/keep.txt base
put mod.txt base
put twice.txt base
put reverted.txt base
put refilled.txt base
put gone.txt base
put olddir/a.txt base
put olddir/sub/b.txt base
put replaced/keep.txt base
put replaced/sub/x.txt base
put remade/keep.txt base
put remade/sub/y.txt base
put thing base
put was/nested.txt base
put run.sh base
ln -s keep.txt link
`
	edit := func(script string) []string {
		return []string{"sh", "-c", "set -e\ncd /src\nput() { mkdir -p \"$(dirname \"$1\")\"; printf '%s\\n' \"$1 $2\" > \"$1\"; }\n" + script}
	}
	base := func(c *dagger.Client) *dagger.Container {
		return c.Container().From(alpineImage).
			WithExec([]string{"sh", "-c", baseScript}).
			// Nothing computed for these trees may come from an earlier run.
			WithNewFile("/src/nonce.txt", identity.NewID())
	}

	t.Run("one exec", func(ctx context.Context, t *testctx.T) {
		c, walks := connectRecordingWalks(ctx, t)
		before := base(c)
		// One layer, written by the kernel: a directory removed and made
		// again within it is opaque.
		after := before.WithExec(edit(`
put mod.txt new
rm gone.txt
rm -rf olddir
rm -rf replaced && mkdir replaced && put replaced/new.txt new
rm thing && put thing/nested.txt new
rm -rf was && put was new
put fresh/a.txt new
put fresh/sub/b.txt new
mkdir empty
chmod +x run.sh
ln -sfn mod.txt link
`))
		requireLayerWalkCase(ctx, t, c, layerWalkCase{
			before: before.Directory("/src"), after: after.Directory("/src"),
			beforeRoot: before.Rootfs(), afterRoot: after.Rootfs(), rootPrefix: "src",
			added: []string{
				"empty/", "fresh/", "fresh/a.txt", "fresh/sub/", "fresh/sub/b.txt",
				"replaced/new.txt", "thing/", "thing/nested.txt", "was",
			},
			modified: []string{"link", "mod.txt", "run.sh"},
			removed:  []string{"gone.txt", "olddir/", "replaced/keep.txt", "replaced/sub/", "thing", "was/"},
		})
		walks.requireLayers(t, c)
	})

	t.Run("several execs", func(ctx context.Context, t *testctx.T) {
		c, walks := connectRecordingWalks(ctx, t)
		before := base(c)
		// Each exec is a layer; later ones revert, refill, remove and remake
		// what earlier ones wrote.
		after := before.
			WithExec(edit(`
put twice.txt first
put reverted.txt changed
put transient/x.txt new
rm refilled.txt
put olddir/added.txt new
rm -rf remade
rm thing
`)).
			WithExec(edit(`
put reverted.txt base
rm -rf transient
rm -rf olddir
put remade/new.txt new
put thing/nested.txt new
rm -rf was
chmod +x run.sh
ln -sfn twice.txt link
`)).
			WithExec(edit(`
put twice.txt second
put refilled.txt new
put was new
put fresh/a.txt new
put fresh/sub/b.txt new
put mod.txt new
`))
		requireLayerWalkCase(ctx, t, c, layerWalkCase{
			before: before.Directory("/src"), after: after.Directory("/src"),
			beforeRoot: before.Rootfs(), afterRoot: after.Rootfs(), rootPrefix: "src",
			added: []string{
				"fresh/", "fresh/a.txt", "fresh/sub/", "fresh/sub/b.txt",
				"remade/new.txt", "thing/", "thing/nested.txt", "was",
			},
			modified: []string{"link", "mod.txt", "refilled.txt", "run.sh", "twice.txt"},
			removed:  []string{"olddir/", "remade/keep.txt", "remade/sub/", "thing", "was/"},
			// Rewritten with its original content: a new file to a diff,
			// no change to a changeset.
			diffOnly: []string{"reverted.txt"},
		})
		walks.requireLayers(t, c)
	})

	t.Run("siblings", func(ctx context.Context, t *testctx.T) {
		c, walks := connectRecordingWalks(ctx, t)
		shared := base(c)
		// Two children of one snapshot: each side's layer holds its own
		// edits, and the other side keeps the shared tree there.
		before := shared.WithExec(edit(`
put mod.txt lower
put twice.txt both
rm gone.txt
rm -rf olddir
put lower-added/x.txt new
rm thing && put thing/nested.txt lower
rm -rf replaced && mkdir replaced && put replaced/l.txt lower
`))
		after := shared.WithExec(edit(`
put twice.txt both
put reverted.txt upper
rm -rf remade
put upper-added/y.txt new
`))
		requireLayerWalkCase(ctx, t, c, layerWalkCase{
			before: before.Directory("/src"), after: after.Directory("/src"),
			beforeRoot: before.Rootfs(), afterRoot: after.Rootfs(), rootPrefix: "src",
			added: []string{
				"gone.txt", "olddir/", "olddir/a.txt", "olddir/sub/", "olddir/sub/b.txt",
				"replaced/keep.txt", "replaced/sub/", "replaced/sub/x.txt",
				"thing", "upper-added/", "upper-added/y.txt",
			},
			modified: []string{"mod.txt", "reverted.txt"},
			removed:  []string{"lower-added/", "remade/", "replaced/l.txt", "thing/"},
			// Written identically on both sides.
			diffOnly: []string{"twice.txt"},
		})
		walks.requireLayers(t, c)
	})

	t.Run("directory operations", func(ctx context.Context, t *testctx.T) {
		c, walks := connectRecordingWalks(ctx, t)
		before := c.Directory().
			WithNewFile("keep.txt", "keep").
			WithNewFile("mod.txt", "mod base").
			WithNewFile("olddir/a.txt", "olddir a").
			WithNewFile("remade/keep.txt", "remade keep").
			WithNewFile("remade/sub/y.txt", "remade y").
			WithNewFile("nonce.txt", identity.NewID())
		// Each operation is a layer. Removing a directory and making it again
		// (as Workspace.withNewDirectory does) leaves a whiteout in one layer
		// and the new directory in the next.
		after := before.
			WithoutDirectory("remade").
			WithNewDirectory("remade").
			WithNewFile("remade/new.txt", "remade new").
			WithoutDirectory("olddir").
			WithNewFile("mod.txt", "mod new").
			WithNewFile("fresh/a.txt", "fresh a")
		requireLayerWalkCase(ctx, t, c, layerWalkCase{
			before: before, after: after,
			beforeRoot: before, afterRoot: after,
			added:    []string{"fresh/", "fresh/a.txt", "remade/new.txt"},
			modified: []string{"mod.txt"},
			removed:  []string{"olddir/", "remade/keep.txt", "remade/sub/"},
		})
		walks.requireLayers(t, c)
	})
}

type layerWalkCase struct {
	// before and after are the trees compared.
	before, after *dagger.Directory
	// beforeRoot and afterRoot are their snapshots' roots, which
	// Directory.diff compares, with the trees at rootPrefix.
	beforeRoot, afterRoot *dagger.Directory
	rootPrefix            string

	added, modified, removed []string
	// diffOnly are files a diff reports but a changeset doesn't: rewritten
	// with the content they had.
	diffOnly []string
}

func requireLayerWalkCase(ctx context.Context, t *testctx.T, c *dagger.Client, lc layerWalkCase) {
	t.Helper()
	changes := lc.after.Changes(lc.before)
	added, err := changes.AddedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, lc.added, added, "added")
	modified, err := changes.ModifiedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, lc.modified, modified, "modified")
	removed, err := changes.RemovedPaths(ctx)
	require.NoError(t, err)
	require.Equal(t, lc.removed, removed, "removed")

	// Applying the changes to Before must give After: a path the walk
	// missed would be left behind or missing.
	require.Equal(t, treeListing(ctx, t, c, lc.after), treeListing(ctx, t, c, lc.before.WithChanges(changes)))

	// A diff holds every added or changed file (and the directories leading
	// to them, which aren't compared here).
	diff := lc.beforeRoot.Diff(lc.afterRoot)
	if lc.rootPrefix != "" {
		diff = diff.Directory(lc.rootPrefix)
	}
	entries, err := diff.Glob(ctx, "**/*")
	require.NoError(t, err)
	isFile := func(p string) bool { return !strings.HasSuffix(p, "/") }
	var wantFiles []string
	for _, p := range slices.Concat(lc.added, lc.modified, lc.diffOnly) {
		if isFile(p) {
			wantFiles = append(wantFiles, p)
		}
	}
	require.ElementsMatch(t, wantFiles, slices.DeleteFunc(entries, func(p string) bool { return !isFile(p) }), "diff")
}

// treeListing lists dir's entries with their type, permissions, and content
// or link target: what a changeset's application must reproduce.
func treeListing(ctx context.Context, t *testctx.T, c *dagger.Client, dir *dagger.Directory) string {
	t.Helper()
	out, err := c.Container().From(alpineImage).
		WithMountedDirectory("/m", dir).
		WithWorkdir("/m").
		WithExec([]string{"sh", "-c", `find . -mindepth 1 | sort | while IFS= read -r p; do
	if [ -L "$p" ]; then echo "$p link $(readlink "$p")"
	elif [ -d "$p" ]; then echo "$p dir $(stat -c %a "$p")"
	else echo "$p file $(stat -c %a "$p") $(cat "$p")"; fi
done`}).
		Stdout(ctx)
	require.NoError(t, err)
	return out
}

// layerWalkRecorder collects the walks a session's changeset comparisons and
// directory diffs took, from the engine's telemetry.
type layerWalkRecorder struct {
	sink *agentTraceSink
}

func connectRecordingWalks(ctx context.Context, t *testctx.T) (*dagger.Client, *layerWalkRecorder) {
	t.Helper()
	sink := newAgentTraceSink(t)
	return connect(ctx, t, sink.clientOpts()...), &layerWalkRecorder{sink: sink}
}

// requireLayers closes c, flushing its telemetry, and requires that every
// changeset comparison and every Directory.diff walked the layers between
// its snapshots, and that there was at least one of each.
func (r *layerWalkRecorder) requireLayers(t *testctx.T, c *dagger.Client) {
	t.Helper()
	require.NoError(t, c.Close())
	type span struct {
		name, parent string
		attrs        map[string]string
	}
	spans := map[string]span{}
	traces, _ := r.sink.capture()
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, s := range scope.Spans {
					attrs := map[string]string{}
					for _, attr := range s.Attributes {
						attrs[attr.Key] = attr.Value.GetStringValue()
					}
					spans[string(s.TraceId)+string(s.SpanId)] = span{
						name:   s.Name,
						parent: string(s.TraceId) + string(s.ParentSpanId),
						attrs:  attrs,
					}
				}
			}
		}
	}
	underDiff := func(s span) bool {
		for p, ok := spans[s.parent]; ok; p, ok = spans[p.parent] {
			if p.name == "Directory.diff" {
				return true
			}
		}
		return false
	}
	var comparisons, diffs int
	for _, s := range spans {
		switch {
		case s.name == "compare changeset trees":
			comparisons++
			require.Equal(t, "layers", s.attrs["dagger.changeset.paths.walk"], "changeset comparison: %v", s.attrs)
		case s.name == "snapshot diff" && underDiff(s):
			diffs++
			require.Contains(t, []string{"layer", "layers"}, s.attrs["dagger.snapshot.diff.walk"], "diff: %v", s.attrs)
		}
	}
	require.Positive(t, comparisons, "no changeset comparisons recorded")
	require.Positive(t, diffs, "no directory diffs recorded")
}
