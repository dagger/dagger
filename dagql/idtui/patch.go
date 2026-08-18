package idtui

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/util/patchpreview"
)

const previewPatchQuery = `
query PreviewPatch($changeset: ID!) {
	changeset: node(id: $changeset) {
		... on Changeset {
			diffStats {
				path
				oldPath
				kind
				addedLines
				removedLines
			}
		}
	}
}
`

func PreviewPatch(ctx context.Context, dag *dagger.Client, changeset *dagger.Changeset) ([]patchpreview.Entry, error) {
	changesetID, err := changeset.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("query diff stat: get changeset id: %w", err)
	}

	var res struct {
		Changeset struct {
			DiffStats []diffStat
		}
	}

	err = dag.Do(ctx, &dagger.Request{
		Query: previewPatchQuery,
		Variables: map[string]any{
			"changeset": changesetID,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, fmt.Errorf("query diff stat: %w", err)
	}

	diffStat := res.Changeset.DiffStats
	return toEntries(diffStat), nil
}

type diffStat struct {
	Path         string
	OldPath      *string
	Kind         string
	AddedLines   int
	RemovedLines int
}

func toEntries(stats []diffStat) []patchpreview.Entry {
	entries := make([]patchpreview.Entry, len(stats))
	for i, s := range stats {
		entries[i] = patchpreview.Entry{Path: s.Path, Kind: s.Kind, Added: s.AddedLines, Removed: s.RemovedLines}
		if s.OldPath != nil {
			entries[i].OldPath = *s.OldPath
		}
	}
	return entries
}

// WorkspaceChanges is everything the "Changes" preview shows: the workspace's
// Git-visible and unmanaged pending edits, plus commits staged engine-side but
// not yet saved to the local checkout.
type WorkspaceChanges struct {
	// Uncommitted combines WorkspaceGit.uncommitted with WorkspaceGit.unmanaged.
	// The two views are disjoint: unmanaged is the pending overlay remainder Git
	// cannot see, such as ignored files and edits inside nested repositories.
	Uncommitted []patchpreview.Entry
	// StagedCommits is WorkspaceGit.stagedCommits, oldest first (the order the
	// API reports them in). Rendering reverses it.
	StagedCommits []patchpreview.Commit
}

// Empty reports whether there is nothing at all to preview.
func (c WorkspaceChanges) Empty() bool {
	return len(c.Uncommitted) == 0 && len(c.StagedCommits) == 0
}

const previewWorkspaceQuery = `
query PreviewWorkspaceChanges($workspace: ID!) {
	workspace: node(id: $workspace) {
		... on Workspace {
			git {
				uncommitted {
					diffStats {
						path
						oldPath
						kind
						addedLines
						removedLines
					}
				}
				unmanaged {
					diffStats {
						path
						oldPath
						kind
						addedLines
						removedLines
					}
				}
				stagedCommits {
					sha
					message
					changes {
						diffStats {
							path
							oldPath
							kind
							addedLines
							removedLines
						}
					}
				}
			}
		}
	}
}
`

// PreviewWorkspaceChanges fetches the workspace's complete pending Git state
// and staged commit stack in a single round-trip. WorkspaceGit.uncommitted is
// measured from the effective staged HEAD, so committed content does not leak
// into the pending section; WorkspaceGit.unmanaged adds the pending overlay
// edits Git cannot see.
func PreviewWorkspaceChanges(ctx context.Context, dag *dagger.Client, workspace *dagger.Workspace) (WorkspaceChanges, error) {
	var changes WorkspaceChanges

	workspaceID, err := workspace.ID(ctx)
	if err != nil {
		return changes, fmt.Errorf("query workspace changes: get workspace id: %w", err)
	}

	var res struct {
		Workspace struct {
			Git struct {
				Uncommitted struct {
					DiffStats []diffStat
				}
				Unmanaged struct {
					DiffStats []diffStat
				}
				StagedCommits []struct {
					Sha     string
					Message string
					Changes struct {
						DiffStats []diffStat
					}
				}
			}
		}
	}

	err = dag.Do(ctx, &dagger.Request{
		Query: previewWorkspaceQuery,
		Variables: map[string]any{
			"workspace": workspaceID,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		// A workspace with no Git root cannot expose Workspace.git, but its
		// pending overlay is still worth showing. Such a workspace cannot stage
		// Git commits, so Workspace.changes is the complete fallback view.
		entries, fallbackErr := PreviewPatch(ctx, dag, workspace.Changes())
		if fallbackErr != nil {
			return changes, fmt.Errorf("query workspace changes: %w", err)
		}
		changes.Uncommitted = entries
		return changes, nil
	}

	changes.Uncommitted = append(
		toEntries(res.Workspace.Git.Uncommitted.DiffStats),
		toEntries(res.Workspace.Git.Unmanaged.DiffStats)...,
	)
	for _, c := range res.Workspace.Git.StagedCommits {
		changes.StagedCommits = append(changes.StagedCommits, patchpreview.Commit{
			SHA:     c.Sha,
			Message: c.Message,
			Entries: toEntries(c.Changes.DiffStats),
		})
	}
	return changes, nil
}
