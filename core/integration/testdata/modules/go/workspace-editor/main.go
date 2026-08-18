// A module that edits a file in the workspace it was handed, so the resulting
// changeset is produced from inside a module function rather than by the
// client that owns the checkout.
//
// Used by core/integration/workspace_module_edit_test.go, the confirmation
// experiment for hack/designs/async-agents.md §11 item 14 ("Changeset replay
// loses tracked-ness"): a module-held workspace editing a host-present file
// that the parent overlay never touched.
//
// Harvest (below) is used by core/integration/workspace_commit_sequence_test.go
// for the other half of that investigation: replaying the commits one workspace
// staged onto another, which needs two workspaces alive in one session and so
// cannot be expressed as a single GraphQL document against currentWorkspace.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger/editor/internal/dagger"
)

type Editor struct{}

// EditLine rewrites a single line of an existing workspace file in place: read
// it through the workspace, replace one line, write it back. The workspace
// argument is auto-injected with the caller's workspace when the caller is a
// client rather than another module.
func (m *Editor) EditLine(
	ctx context.Context,
	ws *dagger.Workspace,
	// Workspace-relative path of the file to edit.
	path string,
	// Line to replace, which must be present.
	oldLine string,
	// Replacement line.
	newLine string,
) (*dagger.Workspace, error) {
	return editLine(ctx, ws, path, oldLine, newLine)
}

// TouchThenEditLine writes an unrelated file first, then performs the same
// surgical edit. The unrelated write gives the workspace an overlay whose
// accumulated touched path set does NOT contain the edited path, which is the
// configuration item 14 predicts will report the edit as a whole-file add.
func (m *Editor) TouchThenEditLine(
	ctx context.Context,
	ws *dagger.Workspace,
	// Workspace-relative path of the file to edit.
	path string,
	// Line to replace, which must be present.
	oldLine string,
	// Replacement line.
	newLine string,
	// Workspace-relative path of the unrelated file to write first.
	scratch string,
) (*dagger.Workspace, error) {
	return editLine(ctx, ws.WithNewFile(scratch, "scratch\n"), path, oldLine, newLine)
}

func editLine(ctx context.Context, ws *dagger.Workspace, path, oldLine, newLine string) (*dagger.Workspace, error) {
	contents, err := ws.File(path).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if !strings.Contains(contents, oldLine) {
		return nil, fmt.Errorf("%s does not contain %q", path, oldLine)
	}
	return ws.WithNewFile(path, strings.Replace(contents, oldLine, newLine, 1)), nil
}

// harvestReport is what Harvest returns, as JSON: everything the test needs to
// say what the replay actually did.
type harvestReport struct {
	Mode string `json:"mode"`
	// SourceCommits is what the source workspace's staged commits RECORD —
	// the per-commit deltas Workspace.git.stagedCommits renders, which are
	// also the patches the replay applies.
	SourceCommits []harvestCommit `json:"sourceCommits"`
	// Picks is the plan: what Workspace.commitsFrom says would happen.
	Picks []harvestPick `json:"picks"`
	// ApplyError is the error withCommitsFrom failed with, if it did.
	ApplyError string `json:"applyError"`
	// ReceiverCommits are the messages of the receiver's staged commits after
	// a successful replay, oldest first.
	ReceiverCommits []string `json:"receiverCommits"`
	// ReceiverContents is the receiver's copy of the edited file after the
	// replay: the answer to "did the whole-file add clobber it?".
	ReceiverContents string `json:"receiverContents"`
}

type harvestCommit struct {
	Message string            `json:"message"`
	Stats   []harvestDiffStat `json:"stats"`
}

type harvestDiffStat struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	AddedLines   int    `json:"addedLines"`
	RemovedLines int    `json:"removedLines"`
}

type harvestPick struct {
	Message       string   `json:"message"`
	Status        string   `json:"status"`
	Reason        string   `json:"reason"`
	ConflictPaths []string `json:"conflictPaths"`
}

// Harvest stages two commits in the workspace it was handed and replays them
// onto a RECEIVER workspace snapshotted from the same checkout — the shape of a
// chief pulling a worker's commits (Workspace.withCommitsFrom, which replays
// each commit's own recorded changeset as a patch).
//
// The receiver carries a committed edit of its own to the same file, on a
// different line, so the two possible failure modes are distinguishable: a
// refusal shows up as a CONFLICT pick and an error from withCommitsFrom, while
// a clobber shows up as a successful replay whose ReceiverContents has lost the
// receiver's line.
//
// mode selects the order the source's two commits are staged in:
//
//   - "after-prior-commit": write scratch, commit, THEN first-edit the tracked
//     file, commit. The second commit's recorded changeset is the one under
//     investigation.
//   - "control": write both, then commit them one path-scoped commit at a
//     time. Same two commits, same content, same order of messages — only the
//     point at which the tracked path was first touched differs.
func (m *Editor) Harvest(
	ctx context.Context,
	ws *dagger.Workspace,
	// Workspace-relative path of the long-tracked file both sides edit.
	path string,
	// Line the source replaces, which must be present.
	oldLine string,
	// The source's replacement line.
	newLine string,
	// Line the RECEIVER replaces in its own committed edit; must be present
	// and must differ from oldLine, so a clobber is detectable.
	receiverOldLine string,
	// The receiver's replacement line.
	receiverNewLine string,
	// Workspace-relative path of the unrelated new file the first commit adds.
	scratch string,
	// Fixed commit date, so hashes never depend on a clock.
	date string,
	// "after-prior-commit" or "control".
	mode string,
) (string, error) {
	report := harvestReport{Mode: mode, ReceiverCommits: []string{}, Picks: []harvestPick{}}

	original, err := ws.File(path).Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if !strings.Contains(original, oldLine) {
		return "", fmt.Errorf("%s does not contain %q", path, oldLine)
	}
	if !strings.Contains(original, receiverOldLine) {
		return "", fmt.Errorf("%s does not contain %q", path, receiverOldLine)
	}
	sourceEdited := strings.Replace(original, oldLine, newLine, 1)
	receiverEdited := strings.Replace(original, receiverOldLine, receiverNewLine, 1)

	// The receiver: the same checkout as a value workspace, plus a committed
	// edit of its own. Committed rather than pending on purpose — a pending
	// edit on the same path is refused earlier, as a DIRTY conflict, which
	// would mask what the patch itself does.
	receiver := ws.Directory(".").AsWorkspace().
		WithNewFile(path, receiverEdited).
		WithCommit("receiver work", date)

	var source *dagger.Workspace
	switch mode {
	case "after-prior-commit":
		source = ws.
			WithNewFile(scratch, "scratch\n").
			WithCommit("add scratch", date).
			WithNewFile(path, sourceEdited).
			WithCommit("edit tracked", date)
	case "control":
		source = ws.
			WithNewFile(scratch, "scratch\n").
			WithNewFile(path, sourceEdited).
			WithCommit("add scratch", date, dagger.WorkspaceWithCommitOpts{Paths: []string{scratch}}).
			WithCommit("edit tracked", date, dagger.WorkspaceWithCommitOpts{Paths: []string{path}})
	default:
		return "", fmt.Errorf("unknown mode %q", mode)
	}

	staged, err := source.Git().StagedCommits(ctx)
	if err != nil {
		return "", fmt.Errorf("source staged commits: %w", err)
	}
	for _, commit := range staged {
		entry := harvestCommit{Stats: []harvestDiffStat{}}
		entry.Message, err = commit.Message(ctx)
		if err != nil {
			return "", err
		}
		stats, err := commit.Changes().DiffStats(ctx)
		if err != nil {
			return "", err
		}
		for _, stat := range stats {
			var s harvestDiffStat
			if s.Path, err = stat.Path(ctx); err != nil {
				return "", err
			}
			kind, err := stat.Kind(ctx)
			if err != nil {
				return "", err
			}
			s.Kind = string(kind)
			if s.AddedLines, err = stat.AddedLines(ctx); err != nil {
				return "", err
			}
			if s.RemovedLines, err = stat.RemovedLines(ctx); err != nil {
				return "", err
			}
			entry.Stats = append(entry.Stats, s)
		}
		report.SourceCommits = append(report.SourceCommits, entry)
	}

	// The plan first: commitsFrom classifies without applying, and never fails
	// on a conflict, so it records the verdict even when the apply refuses.
	picks, err := receiver.CommitsFrom(ctx, source)
	if err != nil {
		return "", fmt.Errorf("commitsFrom: %w", err)
	}
	for _, pick := range picks {
		var p harvestPick
		if p.Message, err = pick.Message(ctx); err != nil {
			return "", err
		}
		status, err := pick.Status(ctx)
		if err != nil {
			return "", err
		}
		p.Status = string(status)
		reason, err := pick.Reason(ctx)
		if err != nil {
			return "", err
		}
		p.Reason = string(reason)
		if p.ConflictPaths, err = pick.ConflictPaths(ctx); err != nil {
			return "", err
		}
		report.Picks = append(report.Picks, p)
	}

	// Then the apply. A refusal is a *result* here, not a test failure: which
	// of the two it is — refusal or silent clobber — is the measurement.
	pulled := receiver.WithCommitsFrom(source)
	pulledCommits, err := pulled.Git().StagedCommits(ctx)
	if err != nil {
		report.ApplyError = err.Error()
	} else {
		for _, commit := range pulledCommits {
			message, err := commit.Message(ctx)
			if err != nil {
				return "", err
			}
			report.ReceiverCommits = append(report.ReceiverCommits, message)
		}
		if report.ReceiverContents, err = pulled.File(path).Contents(ctx); err != nil {
			return "", fmt.Errorf("read replayed %s: %w", path, err)
		}
	}

	out, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
