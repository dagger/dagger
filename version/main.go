// Shared logic for managing Dagger versions
//
// In general, it attempts to follow go's psedudoversioning:
// https://go.dev/doc/modules/version-numbers
package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dagger/dagger/version/internal/dagger"
	"golang.org/x/mod/semver"
)

// daggerRepoURL is only used by the no-local-git fallback path.
//
// The normal version path should use workspace git metadata. The fallback keeps
// worktree builds alive until core exposes a worktree-aware Workspace.git API.
const daggerRepoURL = "https://github.com/dagger/dagger.git"

// New builds the Version helper from the contextual git metadata Dagger can
// currently provide.
//
// Today that metadata arrives as a Directory filtered down to .git. That works
// for ordinary clones where .git is a directory, but not for linked worktrees
// where .git is a pointer file. In that case Git remains nil and the public
// methods use their documented best-effort fallback behavior.
func New(
	ctx context.Context,

	// A directory containing the git metadata for the artifact to be versioned.
	// +optional
	// +defaultPath="/"
	// +ignore=["*", "!.git"]
	gitParent *dagger.Directory,
) *Version {
	v := &Version{}

	if gitParent != nil {
		if gitDir, err := gitParent.Directory(".git").Sync(ctx); err == nil {
			v.Git = gitDir.AsGit()
		}
	}

	return v
}

// Version contains the local git repository used for version decisions.
//
// Git is private because callers should not depend on how the module currently
// obtains workspace git state. The intended replacement is core Workspace.git().
type Version struct {
	// +private
	Git *dagger.GitRepository
}

// Version generates the release or dev version string for the current checkout.
//
// Release commits return their semver tag. Clean untagged commits use the next
// patch version, the HEAD commit timestamp, and the HEAD commit SHA. Dirty
// checkouts use the next patch version, wall-clock time, and a digest of the
// uncommitted patch.
//
// If local git metadata cannot be loaded, this falls back to a digestless dirty
// dev version. That fallback exists for linked worktrees until Workspace.git()
// can provide worktree-aware git state.
func (v Version) Version(ctx context.Context) (string, error) {
	git := v.workspaceGit()
	if !git.found() {
		return v.fallbackVersion(ctx)
	}

	dirty, err := v.Dirty(ctx)
	if err != nil {
		return "", err
	}

	if dirty {
		// this is a dirty version - git state is dirty
		// (v<major>.<minor>.<patch>-<timestamp>-dev-<patchdigest>)
		next, err := v.NextPatchVersion(ctx)
		if err != nil {
			return "", err
		}
		// FIXME: Dirty already checked git.uncommitted().IsEmpty above. Keep the
		// Changeset around so dirty detection and dirty digest generation share
		// one local value instead of asking the facade for the same changes twice.
		rawDigest, err := git.uncommitted().AsPatch().Digest(ctx, dagger.FileDigestOpts{
			ExcludeMetadata: true,
		})
		if err != nil {
			return "", err
		}
		_, digest, ok := strings.Cut(rawDigest, ":")
		if !ok {
			return "", fmt.Errorf("invalid digest: %s", rawDigest)
		}
		return fmt.Sprintf("%s-%s-dev-%s", next, pseudoversionTimestamp(time.Now()), digest[:12]), nil
	}

	if tag, err := v.CurrentTag(ctx); err != nil {
		return "", err
	} else if tag != "" {
		// this is a tagged release
		// (v<major>.<minor>.<patch>)
		return tag, nil
	}

	// this is a clean, untagged version - git state is clean, but no tag
	// (v<major>.<minor>.<patch>-<timestamp>-dev-<commit>)
	next, err := v.NextPatchVersion(ctx)
	if err != nil {
		return "", err
	}
	head := git.head()
	commit, err := head.Commit(ctx)
	if err != nil {
		return "", err
	}
	commitDate, err := refTimestamp(ctx, head)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-dev-%s", next, pseudoversionTimestamp(commitDate), commit[:12]), nil
}

// fallbackVersion returns a best-effort dirty dev version when workspace git
// metadata is unavailable.
//
// This path deliberately does not try to fingerprint source files. The old
// input-directory digest required a separate manually maintained ignore list,
// which was too easy to let drift from real build inputs. Until Workspace.git()
// exists, the fallback only uses the upstream release tags to choose the next
// patch baseline, plus the current time to make the build visibly non-release.
func (v Version) fallbackVersion(ctx context.Context) (string, error) {
	next, err := fallbackNextPatchVersion(ctx)
	if err != nil {
		return "", err
	}

	// Without local git metadata, fall back to a best-effort dirty dev version.
	return fmt.Sprintf("%s-%s-dev", next, pseudoversionTimestamp(time.Now())), nil
}

// ImageTag returns the tag used by CLI auto-downloads for engine images.
//
// This is still the legacy image-tag policy: release commits use the current
// semver tag, and untagged commits use a merge-base commit with main when one
// can be found. The longer-term plan is to remove this separate image tag path
// and use Version everywhere.
//
// FIXME: Remove ImageTag after engine/CLI build callers use Version for image
// tagging too. Keeping a separate merge-base-based tag policy makes version
// behavior harder to reason about and is not part of the Workspace.git facade.
func (v Version) ImageTag(ctx context.Context) (string, error) {
	git := v.workspaceGit()
	if !git.found() {
		return v.fallbackVersion(ctx)
	}

	if tag, err := v.CurrentTag(ctx); err != nil {
		return "", err
	} else if tag != "" {
		// this is a tagged release
		// (v<major>.<minor>.<patch>)
		return tag, nil
	}

	// For untagged builds, find merge-base with main
	// Try local main first, then origin/main for CI (detached HEAD)
	head := git.head()
	for _, ref := range []string{"main", "origin/main"} {
		if branch := git.branch(ref); branch != nil {
			if mergeBase, err := head.CommonAncestor(branch).Commit(ctx); err == nil {
				return mergeBase, nil
			}
		}
	}
	return head.Commit(ctx)
}

// Dirty reports whether the workspace has uncommitted git changes.
//
// When local git is unavailable, the module treats the checkout as dirty. That
// keeps all no-git/worktree fallback versions out of the release-version shape.
func (v Version) Dirty(ctx context.Context) (bool, error) {
	git := v.workspaceGit()
	if !git.found() {
		return true, nil
	}

	isEmpty, err := git.uncommitted().IsEmpty(ctx)
	if err != nil {
		return false, err
	}
	return !isEmpty, nil
}

// CurrentTag returns the semver tag currently pointing at HEAD.
//
// An empty string means HEAD is not a semver-tagged release commit, or local git
// metadata is unavailable.
func (v Version) CurrentTag(ctx context.Context) (string, error) {
	return v.workspaceGit().currentSemverTag(ctx, true)
}

// NextPatchVersion returns the next patch version after the latest stable semver tag.
//
// With workspace git available, tags come from the local repository. Without it,
// this explicitly uses the upstream Dagger repository as a temporary fallback so
// direct calls continue to work from linked worktrees.
func (v Version) NextPatchVersion(ctx context.Context) (string, error) {
	git := v.workspaceGit()
	if !git.found() {
		return fallbackNextPatchVersion(ctx)
	}

	tag, err := git.latestSemverTag(ctx, false)
	if err != nil {
		return "", err
	}
	return bumpPatchVersion(tag)
}

// fallbackNextPatchVersion computes the next patch version from upstream tags.
//
// Keep this fallback outside workspaceGit: workspaceGit should describe the
// local workspace repository only. The hardcoded upstream repository is version
// module policy for the current worktree stopgap.
func fallbackNextPatchVersion(ctx context.Context) (string, error) {
	tag, err := latestSemverTag(ctx, dag.Git(daggerRepoURL), false)
	if err != nil {
		return "", err
	}
	return bumpPatchVersion(tag)
}

// bumpPatchVersion increments the patch component of a canonical semver tag.
//
// The version module uses this to turn the latest released tag into the base for
// dev versions, e.g. v1.2.3 becomes v1.2.4.
func bumpPatchVersion(version string) (string, error) {
	original := version
	version = semver.Canonical(version)
	if version == "" {
		return "", fmt.Errorf("invalid semver: %q", original)
	}

	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid canonical semver: %q", version)
	}

	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", fmt.Errorf("invalid patch version %q: %w", version, err)
	}

	return fmt.Sprintf("v%s.%s.%d", parts[0], parts[1], patch+1), nil
}

// refTimestamp returns the committer timestamp for a GitRef.
//
// GitRef does not currently expose commit timestamps directly, so this checks
// out the ref tree and asks git for the HEAD commit timestamp. Workspace.git()
// should eventually make this a direct GitRef field.
func refTimestamp(ctx context.Context, head *dagger.GitRef) (time.Time, error) {
	checkout := head.Tree()
	status, err := dag.Container().
		From("alpine/git:latest").
		WithWorkdir("/src").
		WithMountedDirectory(".", checkout).
		WithExec([]string{"git", "log", "-1", "--format=%cI"}).
		Stdout(ctx)
	if err != nil {
		return time.Time{}, err
	}
	status = strings.TrimSpace(status)
	t, err := time.Parse(time.RFC3339, status)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// pseudoversionTimestamp formats timestamps the same way Go pseudoversions do.
//
// The result is UTC yyyymmddhhmmss and is stable for clean commits because it
// uses the commit timestamp instead of wall-clock time.
func pseudoversionTimestamp(t time.Time) string {
	// go time formatting is bizarre - this translates to "yyyymmddhhmmss"
	// inspired from: https://cs.opensource.google/go/x/mod/+/refs/tags/v0.22.0:module/pseudo.go
	return t.UTC().Format("20060102150405")
}
