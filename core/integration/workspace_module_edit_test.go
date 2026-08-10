package core

// The confirmation experiment for hack/designs/async-agents.md §11 item 14
// ("Changeset replay loses tracked-ness"): does a MODULE-held workspace record
// a surgical edit to a long-tracked, host-present file as a MODIFY, or as a
// whole-file ADD?
//
// Why a module: ADDED vs MODIFIED is decided in buildDiffStats
// (core/changeset.go) from the delta between the changeset's before and after
// trees, and for a host-backed workspace the BEFORE tree is deliberately
// sparse — sparseHostBase (core/schema/workspace.go) builds it as
// host.directory(path: ".", include: <TouchedPaths>), i.e. only the paths the
// overlay has accumulated. A path absent from that set is absent from the tree
// the edit is diffed against, so the edit reads as an addition of the whole
// file. That is not cosmetic: a staged commit's own changeset is computed
// against the same sparse base (core/schema/workspace_commit.go), so a commit
// of such a file becomes a whole-file add that a later pull cannot apply.
//
// The suspect is overlayWorkspaceWithMutation, which re-bases a new overlay on
// the parent overlay's (sparse) Changes.Before while passing nil for
// TouchedPaths, on the assumption that it is only reached for
// value/git/rootless workspaces — an assumption overlayEdit upholds by
// branching on `ws.HostPath() == "" || !ws.ClientLocalBase()`. The prediction
// under test is that a workspace handed to a module stops satisfying that
// branch while still carrying a host-derived sparse overlay.
//
// Both halves of the experiment perform the SAME two writes against the same
// checkout, differing only in who performs them:
//
//   - client side: currentWorkspace.withNewFile(scratch) then
//     .withNewFile(tracked)
//   - module side: the same, inside a module function that was handed the
//     workspace (testdata/modules/go/workspace-editor)
//
// The scratch write comes first on purpose: it gives the overlay the second
// edit re-bases on a touched path set that does not contain the tracked file.
//
// RESULT (measured, not predicted): the defect does NOT reproduce here. All
// three anchors — the overlay changeset, git.uncommitted, and the staged
// commit's own changeset — report MODIFIED +1 -1 in every case, module-held or
// not. The prediction's premise is false: a workspace handed to a module does
// not stop being client-local. ClientLocalBase reads the workspace's SOURCE
// TYPE (core/workspace.go) and HostPath its recorded host path, both of which
// travel with the value across the module boundary, so overlayEdit takes the
// sparse-host branch inside a module exactly as it does for the owning client,
// and unions the edited path into TouchedPaths before sizing the base. (Read
// off a temporary probe in overlayEdit during this experiment: inside the
// module the workspace reported hostPath=/work, clientLocalBase=true, source
// WorkspaceSourceOverlay over WorkspaceSourceClientLocal, parent touched
// [scratch.txt], edit touched [tracked.txt].)
//
// So this test stands as a regression guard rather than a reproduction, and
// item 14's real trigger is narrower than "held by a module": something else
// must strip host-ness (or the host path) from a workspace that still carries
// a host-derived sparse overlay, since only that combination reaches
// overlayWorkspaceWithMutation with a sparse Before.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const (
	// moduleEditTrackedPath is committed before either half runs, and is never
	// touched again except by the one-line edit under test.
	moduleEditTrackedPath = "tracked.txt"
	// moduleEditScratchPath is the unrelated write that precedes the edit, so
	// the edit lands on an overlay whose touched paths exclude the tracked
	// file.
	moduleEditScratchPath = "scratch.txt"
	moduleEditOldLine     = "line 07"
	moduleEditNewLine     = "line 07 (edited)"
)

// moduleEditTrackedLines is the committed content of the edited file: long
// enough that a whole-file add is unmistakable next to a one-line modify.
const moduleEditTrackedLines = 12

func moduleEditTrackedContents() string {
	var b strings.Builder
	for i := 1; i <= moduleEditTrackedLines; i++ {
		fmt.Fprintf(&b, "line %02d\n", i)
	}
	return b.String()
}

// moduleEditedContents is what both halves write back: the committed content
// with exactly one line replaced.
func moduleEditedContents() string {
	return strings.Replace(moduleEditTrackedContents(), moduleEditOldLine, moduleEditNewLine, 1)
}

// moduleEditBase is a git checkout holding the committed tracked file and the
// editor module at editor/. The module lives in a subdirectory so its own
// codegen never touches the tracked file's path.
func moduleEditBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return gitRepoBase(t, c).
		With(withModuleFixture(t, c, "editor", "go/workspace-editor")).
		WithNewFile(moduleEditTrackedPath, moduleEditTrackedContents()).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})
}

// moduleEditSelection is the three views the experiment reads:
//
//   - changes: the overlay changeset, diffed against the sparse base — the
//     anchor under suspicion.
//   - git.uncommitted: git's own view, diffed against the staged HEAD — the
//     anchor item 14's seventh sighting observed to be right when the other
//     one lied.
//   - withCommit(...).git.stagedCommits: what a staged commit records, which
//     is computed against that same sparse base (core/schema/
//     workspace_commit.go) and is the projection the sighting reported as a
//     whole-file add.
const moduleEditSelection = `
      address
      changes { isEmpty diffStats { path kind addedLines removedLines } }
      git { uncommitted { isEmpty diffStats { path kind addedLines removedLines } } }
      withCommit(message: "edit tracked", date: "` + commitTestDate + `", paths: ["` + moduleEditTrackedPath + `"]) {
        git { stagedCommits { changes { diffStats { path kind addedLines removedLines } } } }
      }`

// moduleEditProbe is the decoded shape of moduleEditSelection.
type moduleEditProbe struct {
	Address string             `json:"address"`
	Changes uncommittedChanges `json:"changes"`
	Git     struct {
		Uncommitted uncommittedChanges `json:"uncommitted"`
	} `json:"git"`
	WithCommit struct {
		Git struct {
			StagedCommits []struct {
				Changes uncommittedChanges `json:"changes"`
			} `json:"stagedCommits"`
		} `json:"git"`
	} `json:"withCommit"`
}

// requireHostBacked pins the premise of the experiment: the workspace that was
// edited is still the client's local checkout, not a synthetic value workspace
// (whose address is a content digest). Without this a future change that hands
// modules a detached copy would make the assertions below pass while measuring
// something else entirely — the sparse base only exists for a host-backed
// workspace.
func requireHostBacked(t *testctx.T, label string, probe moduleEditProbe) {
	t.Helper()
	t.Logf("%s: workspace address %s", label, probe.Address)
	require.True(t, strings.HasPrefix(probe.Address, "file://"),
		"%s: expected a host-backed workspace, got address %q", label, probe.Address)
}

// staged returns the changeset the single staged commit recorded.
func (p moduleEditProbe) staged(t *testctx.T) uncommittedChanges {
	t.Helper()
	require.Len(t, p.WithCommit.Git.StagedCommits, 1)
	return p.WithCommit.Git.StagedCommits[0].Changes
}

// decodeModuleEditProbe pulls the probe payload out of a query response at the
// given gjson path.
func decodeModuleEditProbe(t *testctx.T, out, path string) moduleEditProbe {
	t.Helper()
	raw := gjson.Get(out, path)
	require.True(t, raw.Exists(), "no %q in response: %s", path, out)
	var probe moduleEditProbe
	require.NoError(t, json.Unmarshal([]byte(raw.Raw), &probe))
	return probe
}

// requireSurgicalModify is the whole measurement: the edited path must be
// reported as a one-line MODIFIED, not as an ADDED of all
// moduleEditTrackedLines lines.
func requireSurgicalModify(t *testctx.T, label string, changes uncommittedChanges) {
	t.Helper()
	stat, ok := changes.find(moduleEditTrackedPath)
	require.True(t, ok, "%s: no %s entry in %v", label, moduleEditTrackedPath, changes.DiffStats)
	t.Logf("%s: %s %s +%d -%d", label, stat.Path, stat.Kind, stat.AddedLines, stat.RemovedLines)
	require.Equal(t, "MODIFIED", stat.Kind,
		"%s: a one-line edit to a committed, host-present file reported as %s +%d -%d",
		label, stat.Kind, stat.AddedLines, stat.RemovedLines)
	require.Equal(t, 1, stat.AddedLines, "%s: added lines", label)
	require.Equal(t, 1, stat.RemovedLines, "%s: removed lines", label)
}

// TestWorkspaceModuleEditOfUntouchedTrackedFile runs the experiment described
// at the top of this file. The client-side half is the control: item 14
// predicts it behaves correctly.
func (WorkspaceSuite) TestWorkspaceModuleEditOfUntouchedTrackedFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := moduleEditBase(t, c)
	edited := graphqlString(t, moduleEditedContents())

	// Control: the same two writes performed by the client that owns the
	// checkout, through the plain (non-module) workspace API.
	t.Run("client-side edit", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
  currentWorkspace {
    scratch: withNewFile(path: "` + moduleEditScratchPath + `", contents: "scratch\n") {
      edited: withNewFile(path: "` + moduleEditTrackedPath + `", contents: ` + edited + `) {` +
			moduleEditSelection + `
      }
    }
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		probe := decodeModuleEditProbe(t, out, "currentWorkspace.scratch.edited")
		requireHostBacked(t, "client-side", probe)
		requireSurgicalModify(t, "client-side overlay changes", probe.Changes)
		requireSurgicalModify(t, "client-side git.uncommitted", probe.Git.Uncommitted)
		requireSurgicalModify(t, "client-side staged commit", probe.staged(t))
	})

	// A module editing the tracked file with nothing else pending: the
	// workspace it is handed has no overlay at all, so no touched path set can
	// be too small.
	t.Run("module edit of a pristine workspace", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQueryAt("editor", `{
  editLine(
    path: "`+moduleEditTrackedPath+`",
    oldLine: "`+moduleEditOldLine+`",
    newLine: "`+moduleEditNewLine+`"
  ) {`+moduleEditSelection+`
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		probe := decodeModuleEditProbe(t, out, "editLine")
		requireHostBacked(t, "module (pristine)", probe)
		requireSurgicalModify(t, "module overlay changes", probe.Changes)
		requireSurgicalModify(t, "module git.uncommitted", probe.Git.Uncommitted)
		requireSurgicalModify(t, "module staged commit", probe.staged(t))
	})

	// The predicted population: a module-held workspace whose parent overlay's
	// touched paths exclude the edited path.
	t.Run("module edit after an unrelated write", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQueryAt("editor", `{
  touchThenEditLine(
    path: "`+moduleEditTrackedPath+`",
    oldLine: "`+moduleEditOldLine+`",
    newLine: "`+moduleEditNewLine+`",
    scratch: "`+moduleEditScratchPath+`"
  ) {`+moduleEditSelection+`
  }
}`)).Stdout(ctx)
		require.NoError(t, err)

		probe := decodeModuleEditProbe(t, out, "touchThenEditLine")
		requireHostBacked(t, "module (touch then edit)", probe)

		// The unrelated write must really be pending, or the setup proves
		// nothing about a too-small touched set.
		_, ok := probe.Changes.find(moduleEditScratchPath)
		require.True(t, ok, "scratch write missing from overlay changes: %v", probe.Changes.DiffStats)

		requireSurgicalModify(t, "module overlay changes", probe.Changes)
		requireSurgicalModify(t, "module git.uncommitted", probe.Git.Uncommitted)
		requireSurgicalModify(t, "module staged commit", probe.staged(t))
	})
}

// graphqlString renders s as a GraphQL string literal.
func graphqlString(t testing.TB, s string) string {
	t.Helper()
	quoted, err := json.Marshal(s)
	require.NoError(t, err)
	return string(quoted)
}
