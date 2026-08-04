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
// pending (uncommitted) overlay edits, plus the commits staged engine-side but
// not yet saved to the local checkout.
type WorkspaceChanges struct {
	// Uncommitted is the diffstat of Workspace.changes.
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
			changes {
				diffStats {
					path
					oldPath
					kind
					addedLines
					removedLines
				}
			}
			git {
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

// PreviewWorkspaceChanges fetches the workspace's pending changes and its
// staged commit stack in a single round-trip, so the "Changes" bubble can be
// refreshed once per loop without an extra query per commit.
func PreviewWorkspaceChanges(ctx context.Context, dag *dagger.Client, workspace *dagger.Workspace) (WorkspaceChanges, error) {
	var changes WorkspaceChanges

	workspaceID, err := workspace.ID(ctx)
	if err != nil {
		return changes, fmt.Errorf("query workspace changes: get workspace id: %w", err)
	}

	var res struct {
		Workspace struct {
			Changes struct {
				DiffStats []diffStat
			}
			Git struct {
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
		// A workspace with no Git root can't report staged commits
		// (Workspace.git errors), but its pending edits are still worth
		// showing: fall back to the changes-only query.
		entries, fallbackErr := PreviewPatch(ctx, dag, workspace.Changes())
		if fallbackErr != nil {
			return changes, fmt.Errorf("query workspace changes: %w", err)
		}
		changes.Uncommitted = entries
		return changes, nil
	}

	changes.Uncommitted = toEntries(res.Workspace.Changes.DiffStats)
	for _, c := range res.Workspace.Git.StagedCommits {
		changes.StagedCommits = append(changes.StagedCommits, patchpreview.Commit{
			SHA:     c.Sha,
			Message: c.Message,
			Entries: toEntries(c.Changes.DiffStats),
		})
	}
	return changes, nil
}

