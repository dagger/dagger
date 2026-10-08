package core

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceCommitMergeBreakdown commits on a dagger/dagger-sized remote
// workspace (16,000 files) the way agents do: tool edits and new files,
// path-filtered commits, a pull of another worker's commits,
// and a commit after it. It logs every merge's per-phase timings and the walk
// each changeset's paths took, and requires the merges' own work to stay
// proportional to the change: no phase may cost what a whole-tree walk or
// copy costs.
func (WorkspaceSuite) TestWorkspaceCommitMergeBreakdown(ctx context.Context, t *testctx.T) {
	sink := newAgentTraceSink(t)
	c := connect(ctx, t, append(sink.clientOpts(), dagger.WithLogOutput(io.Discard))...)
	const files = 16000
	fixture := c.Container().From(alpineImage).
		WithExec([]string{"awk", fmt.Sprintf(`BEGIN {
	for (i = 0; i < %d; i++) {
		d = sprintf("/src/tree/d%%03d", int(i / 100))
		if (i %% 100 == 0) system("mkdir -p " d)
		f = sprintf("%%s/f%%05d.txt", d, i)
		for (j = 0; j < 64; j++) printf("line %%d of file %%d: lorem ipsum dolor sit amet\n", j, i) > f
		close(f)
	}
}`, files)}).
		Directory("/src").
		WithNewFile("pending.txt", "base\n").
		WithNewFile("edit.txt", "one\ntwo\nthree\n")
	service, url := gitService(ctx, t, c, fixture)
	started := time.Now()
	base := snapshotWorkspace(ctx, t, c, c.Git(url, dagger.GitOpts{ExperimentalServiceHost: service}).Head().AsWorkspace())
	t.Logf("BREAKDOWN capture=%s", time.Since(started))

	commit := func(name string, ws *dagger.Workspace, include ...string) *dagger.Workspace {
		t.Helper()
		started := time.Now()
		id, err := ws.WithCommit(ws.Git().Uncommitted().Filter(dagger.ChangesetFilterOpts{Include: include}), name, workspaceCommitDate, dagger.WorkspaceWithCommitOpts{
			AuthorName: "Agent", AuthorEmail: "agent@example.com",
		}).ID(ctx)
		require.NoError(t, err, name)
		committed := dagger.Ref[*dagger.Workspace](c, id)
		_, err = committed.Git().Head().CommitSHA(ctx)
		require.NoError(t, err, name)
		paths, err := committed.Git().Uncommitted().ModifiedPaths(ctx)
		require.NoError(t, err, name)
		t.Logf("BREAKDOWN commit=%q wall=%s pending_modified=%v", name, time.Since(started), paths)
		return committed
	}
	edited := base.WithNewFile("tree/d001/f00100.txt", "changed by a tool\n").
		WithNewFile("tree/new/added.txt", "added\n").
		WithNewFile("tree/d002/f00200.txt", "rewritten\n").
		WithNewFile("pending.txt", "keep pending\n")
	first := commit("tool edits", edited, "tree/**")
	second := commit("second edit", first.WithNewFile("edit.txt", "one\nTWO\nthree\n").WithNewFile("tree/d003/f00300.txt", "again\n"), "edit.txt", "tree/**")
	source := commit("other worker", base.WithNewFile("tree/d150/f15000.txt", "theirs\n"), "tree/**")
	pulled, err := applyWorkspacePull(ctx, c, snapshotWorkspace(ctx, t, c, second), snapshotWorkspace(ctx, t, c, source), nil, 100)
	require.NoError(t, err)
	commit("after pull", pulled.WithNewFile("tree/d004/f00400.txt", "after pull\n"), "tree/**")
	require.NoError(t, c.Close())

	traces, _ := sink.capture()
	type sample struct {
		name, parent string
		dur          time.Duration
		attrs        []string
		ms           map[string]int64
		walk         string
	}
	samples := map[string]*sample{}
	var order []string
	for _, request := range traces {
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.EndTimeUnixNano <= span.StartTimeUnixNano {
						continue
					}
					id := string(span.TraceId) + string(span.SpanId)
					if _, ok := samples[id]; ok {
						continue
					}
					s := &sample{name: span.Name, parent: string(span.TraceId) + string(span.ParentSpanId), dur: time.Duration(span.EndTimeUnixNano - span.StartTimeUnixNano), ms: map[string]int64{}}
					for _, attr := range span.Attributes {
						if !strings.HasPrefix(attr.Key, "dagger.git.") && !strings.HasPrefix(attr.Key, "dagger.changeset.") && !strings.HasPrefix(attr.Key, "dagger.snapshot.") {
							continue
						}
						if strings.HasSuffix(attr.Key, "_ms") {
							s.ms[attr.Key] = attr.Value.GetIntValue()
						}
						if attr.Key == "dagger.changeset.paths.walk" || attr.Key == "dagger.snapshot.diff.walk" {
							s.walk = attr.Value.GetStringValue()
						}
						s.attrs = append(s.attrs, fmt.Sprintf("%s=%v", strings.TrimPrefix(attr.Key, "dagger."), attr.Value.String()))
					}
					slices.Sort(s.attrs)
					samples[id] = s
					order = append(order, id)
				}
			}
		}
	}
	ancestors := func(s *sample) []string {
		var names []string
		for parent, ok := samples[s.parent]; ok && len(names) < 6; parent, ok = samples[parent.parent] {
			names = append(names, parent.name)
		}
		return names
	}
	inMerge := func(s *sample) bool {
		for parent, ok := samples[s.parent]; ok; parent, ok = samples[parent.parent] {
			if parent.name == "git native workspace merge" || parent.name == "git changeset merge" {
				return true
			}
		}
		return false
	}
	var nativeMerges int
	var wholeTree []string
	for _, id := range order {
		s := samples[id]
		switch {
		case s.name == "git native workspace merge" || s.name == "git changeset merge":
			t.Logf("BREAKDOWN span=%q dur=%s %s", s.name, s.dur, strings.Join(s.attrs, " "))
			if s.name == "git native workspace merge" {
				nativeMerges++
			}
		case s.walk != "":
			t.Logf("BREAKDOWN walk=%s span=%q dur=%s in_merge=%t %s ancestors=%q", s.walk, s.name, s.dur, inMerge(s), strings.Join(s.attrs, " "), ancestors(s))
			// Two unrelated snapshots of one tree (two checkouts of the
			// commit) have no layers to go by; that is the inputs' shape, not
			// the merge's doing. Snapshots of one lineage must not be walked
			// in full.
			unrelated := slices.ContainsFunc(s.attrs, func(attr string) bool { return strings.Contains(attr, "share no layers") })
			if s.walk == "trees" && inMerge(s) && !unrelated {
				wholeTree = append(wholeTree, fmt.Sprintf("%s %s %q", s.name, strings.Join(s.attrs, " "), ancestors(s)))
			}
		}
	}
	require.Positive(t, nativeMerges)
	// A merge's own work must cost the size of the change: no changeset it
	// reads may walk both whole trees.
	require.Empty(t, wholeTree, "merges walked whole trees")
}
