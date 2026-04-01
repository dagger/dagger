package core

import (
	"context"
	"fmt"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/distribution/reference"
	"golang.org/x/mod/semver"
)

const (
	lockCoreNamespace           = ""
	lockContainerFromOperation  = "container.from"
	lockModulesResolveOperation = "modules.resolve"
	lockGitHeadOperation        = "git.head"
	lockGitRefOperation         = "git.ref"
	lockGitBranchOperation      = "git.branch"
	lockGitTagOperation         = "git.tag"
)

// UpdateWorkspaceLock refreshes the existing entries in a workspace lockfile in place.
func UpdateWorkspaceLock(ctx context.Context, query *Query, lock *workspace.Lock) error {
	entries, err := lock.Entries()
	if err != nil {
		return fmt.Errorf("read lock entries: %w", err)
	}

	for _, entry := range entries {
		result, err := updateWorkspaceLockEntry(ctx, query, entry)
		if err != nil {
			return err
		}
		if err := lock.SetLookup(entry.Namespace, entry.Operation, entry.Inputs, result); err != nil {
			return fmt.Errorf("rewrite lock entry for %s %v: %w", entry.Operation, entry.Inputs, err)
		}
	}

	return nil
}

func updateWorkspaceLockEntry(ctx context.Context, query *Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	switch {
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockContainerFromOperation:
		return updateContainerFromLockEntry(ctx, query, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockModulesResolveOperation:
		return updateModuleResolveLockEntry(ctx, query, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitHeadOperation:
		return updateGitHeadLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitRefOperation:
		return updateGitRefLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitBranchOperation:
		return updateGitBranchLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitTagOperation:
		return updateGitTagLockEntry(ctx, entry)
	default:
		return workspace.LookupResult{}, fmt.Errorf("unsupported lock entry %q %q", entry.Namespace, entry.Operation)
	}
}

func updateContainerFromLockEntry(ctx context.Context, query *Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	if len(entry.Inputs) != 2 {
		return workspace.LookupResult{}, fmt.Errorf("invalid container.from inputs %v", entry.Inputs)
	}

	ref, ok := entry.Inputs[0].(string)
	if !ok || ref == "" {
		return workspace.LookupResult{}, fmt.Errorf("invalid container.from ref %v", entry.Inputs[0])
	}

	platform, ok := entry.Inputs[1].(string)
	if !ok || platform == "" {
		return workspace.LookupResult{}, fmt.Errorf("invalid container.from platform %v", entry.Inputs[1])
	}

	digest, err := resolveContainerFromDigest(ctx, query, ref, platform)
	if err != nil {
		return workspace.LookupResult{}, err
	}

	return workspace.LookupResult{
		Value:  digest,
		Policy: entry.Result.Policy,
	}, nil
}

func updateModuleResolveLockEntry(ctx context.Context, query *Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	if len(entry.Inputs) != 1 {
		return workspace.LookupResult{}, fmt.Errorf("invalid %s inputs %v", lockModulesResolveOperation, entry.Inputs)
	}

	source, ok := entry.Inputs[0].(string)
	if !ok || source == "" {
		return workspace.LookupResult{}, fmt.Errorf("invalid %s source %v", lockModulesResolveOperation, entry.Inputs[0])
	}

	commit, err := resolveModuleSourceCommit(ctx, query, source)
	if err != nil {
		return workspace.LookupResult{}, err
	}

	return workspace.LookupResult{
		Value:  commit,
		Policy: entry.Result.Policy,
	}, nil
}

func resolveContainerFromDigest(ctx context.Context, query *Query, refString, platformString string) (string, error) {
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("buildkit client: %w", err)
	}

	refName, err := reference.ParseNormalizedNamed(refString)
	if err != nil {
		return "", fmt.Errorf("parse image address %q: %w", refString, err)
	}
	refName = reference.TagNameOnly(refName)

	platform, err := platforms.Parse(platformString)
	if err != nil {
		return "", fmt.Errorf("parse platform %q: %w", platformString, err)
	}

	_, resolvedDigest, _, err := bk.ResolveImageConfig(ctx, refName.String(), sourceresolver.Opt{
		Platform: ptr(platform),
		ImageOpt: &sourceresolver.ResolveImageOpt{
			ResolveMode: llb.ResolveModeDefault.String(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("resolve image %q (platform: %q): %w", refName.String(), platformString, err)
	}

	return resolvedDigest.String(), nil
}

func updateGitHeadLockEntry(ctx context.Context, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	if len(entry.Inputs) != 1 {
		return workspace.LookupResult{}, fmt.Errorf("invalid git.head inputs %v", entry.Inputs)
	}
	remoteURL, ok := entry.Inputs[0].(string)
	if !ok || remoteURL == "" {
		return workspace.LookupResult{}, fmt.Errorf("invalid git.head remote %v", entry.Inputs[0])
	}
	commit, err := resolveGitRefCommit(ctx, remoteURL, "head", "")
	if err != nil {
		return workspace.LookupResult{}, err
	}
	return workspace.LookupResult{Value: commit, Policy: entry.Result.Policy}, nil
}

func updateGitRefLockEntry(ctx context.Context, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	remoteURL, name, err := parseGitLookupInputs("git.ref", entry.Inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	commit, err := resolveGitRefCommit(ctx, remoteURL, "ref", name)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	return workspace.LookupResult{Value: commit, Policy: entry.Result.Policy}, nil
}

func updateGitBranchLockEntry(ctx context.Context, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	remoteURL, name, err := parseGitLookupInputs("git.branch", entry.Inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	commit, err := resolveGitRefCommit(ctx, remoteURL, "branch", name)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	return workspace.LookupResult{Value: commit, Policy: entry.Result.Policy}, nil
}

func updateGitTagLockEntry(ctx context.Context, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	remoteURL, name, err := parseGitLookupInputs("git.tag", entry.Inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	commit, err := resolveGitRefCommit(ctx, remoteURL, "tag", name)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	return workspace.LookupResult{Value: commit, Policy: entry.Result.Policy}, nil
}

func resolveModuleSourceCommit(ctx context.Context, query *Query, source string) (string, error) {
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("buildkit client: %w", err)
	}

	parsedRef, err := ParseRefString(ctx, NewCallerStatFS(bk), source, "")
	if err != nil {
		return "", fmt.Errorf("parse module source %q: %w", source, err)
	}
	if parsedRef.Kind != ModuleSourceKindGit {
		return "", fmt.Errorf("module source %q is not a git source", source)
	}

	return resolveParsedGitRefCommit(ctx, parsedRef.Git, "")
}

func parseGitLookupInputs(operation string, inputs []any) (string, string, error) {
	if len(inputs) != 2 {
		return "", "", fmt.Errorf("invalid %s inputs %v", operation, inputs)
	}
	remoteURL, ok := inputs[0].(string)
	if !ok || remoteURL == "" {
		return "", "", fmt.Errorf("invalid %s remote %v", operation, inputs[0])
	}
	name, ok := inputs[1].(string)
	if !ok || name == "" {
		return "", "", fmt.Errorf("invalid %s name %v", operation, inputs[1])
	}
	return remoteURL, name, nil
}

func resolveGitRefCommit(ctx context.Context, remoteURL, field, name string) (string, error) {
	remote, err := loadRemoteGitMetadata(ctx, remoteURL)
	if err != nil {
		return "", err
	}

	target := "HEAD"
	switch field {
	case "head":
	case "ref", "branch", "tag":
		target = name
	default:
		return "", fmt.Errorf("unsupported git lock field %q", field)
	}

	ref, err := remote.Lookup(target)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q for %q: %w", field, name, remoteURL, err)
	}
	return ref.SHA, nil
}

func resolveParsedGitRefCommit(ctx context.Context, parsed *ParsedGitRefString, pinCommitRef string) (string, error) {
	remote, err := loadRemoteGitMetadata(ctx, parsed.cloneRef)
	if err != nil {
		return "", err
	}

	ref, err := resolveParsedGitRef(remote, parsed, pinCommitRef)
	if err != nil {
		return "", err
	}
	return ref.SHA, nil
}

func loadRemoteGitMetadata(ctx context.Context, remoteURL string) (*gitutil.Remote, error) {
	gitURL, err := gitutil.ParseURL(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("parse git URL %q: %w", remoteURL, err)
	}

	repo := &RemoteGitRepository{URL: gitURL}
	remote, err := repo.Remote(ctx)
	if err != nil {
		return nil, fmt.Errorf("load git remote %q: %w", remoteURL, err)
	}
	return remote, nil
}

func resolveParsedGitRef(remote *gitutil.Remote, parsed *ParsedGitRefString, pinCommitRef string) (*gitutil.Ref, error) {
	if gitutil.IsCommitSHA(pinCommitRef) {
		return &gitutil.Ref{SHA: pinCommitRef}, nil
	}

	target := "HEAD"
	switch {
	case parsed.hasVersion && semver.IsValid(parsed.ModVersion):
		matched, err := matchVersion(remote.Tags().ShortNames(), parsed.ModVersion, parsed.RepoRootSubdir)
		if err != nil {
			return nil, fmt.Errorf("matching version to tags: %w", err)
		}
		target = matched
	case parsed.hasVersion:
		target = parsed.ModVersion
	case pinCommitRef != "":
		target = pinCommitRef
	}

	ref, err := remote.Lookup(target)
	if err != nil {
		return nil, fmt.Errorf("resolve git ref %q: %w", target, err)
	}
	return ref, nil
}
