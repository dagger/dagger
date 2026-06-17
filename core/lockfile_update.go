package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core/workspace"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/distribution/reference"
)

const (
	lockCoreNamespace          = ""
	lockContainerFromOperation = "container.from"
	lockGitHeadOperation       = "git.head"
	lockGitRefOperation        = "git.ref"
	lockGitBranchOperation     = "git.branch"
	lockGitTagOperation        = "git.tag"
	lockGitLatestOperation     = "git.latest"
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
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitHeadOperation:
		return updateGitHeadLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitRefOperation:
		return updateGitRefLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitBranchOperation:
		return updateGitBranchLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitTagOperation:
		return updateGitTagLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitLatestOperation:
		return updateGitLatestLockEntry(ctx, entry)
	default:
		return workspace.LookupResult{}, fmt.Errorf("unsupported lock entry %q %q", entry.Namespace, entry.Operation)
	}
}

func updateContainerFromLockEntry(ctx context.Context, query *Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	if len(entry.Inputs) < 2 {
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

	startTransportInput := 2
	latestIncludeSubreleases := false
	var marker string
	if len(entry.Inputs) > 2 {
		marker, _ = entry.Inputs[2].(string)
	}
	latestRelease := marker == ContainerLatestReleaseLockInput
	if latestRelease {
		if len(entry.Inputs) < 4 {
			return workspace.LookupResult{}, fmt.Errorf("missing container.from latestIncludeSubreleases input")
		}
		include, ok := entry.Inputs[3].(bool)
		if !ok {
			return workspace.LookupResult{}, fmt.Errorf("invalid container.from latestIncludeSubreleases %v", entry.Inputs[3])
		}
		latestIncludeSubreleases = include
		startTransportInput = 4
	}

	registryTransport, err := parseContainerFromRegistryTransportInputs(entry.Inputs[startTransportInput:])
	if err != nil {
		return workspace.LookupResult{}, err
	}

	var resolvedRef string
	if latestRelease {
		resolvedRef, err = resolveLatestContainerFromRef(ctx, query, ref, platform, latestIncludeSubreleases, registryTransport)
	} else {
		resolvedRef, err = resolveContainerFromRef(ctx, query, ref, platform, registryTransport)
	}
	if err != nil {
		return workspace.LookupResult{}, err
	}

	return workspace.LookupResult{
		Value:  resolvedRef,
		Policy: entry.Result.Policy,
	}, nil
}

func parseContainerFromRegistryTransportInputs(inputs []any) (serverresolver.RegistryTransport, error) {
	var transport serverresolver.RegistryTransport
	for _, input := range inputs {
		value, ok := input.(string)
		if !ok || value == "" {
			return serverresolver.RegistryTransport{}, fmt.Errorf("invalid container.from transport input %v", input)
		}
		switch value {
		case string(serverresolver.RegistryProtocolHTTPS):
			transport.Protocol = serverresolver.RegistryProtocolHTTPS
		case string(serverresolver.RegistryProtocolHTTP):
			transport.Protocol = serverresolver.RegistryProtocolHTTP
		case "insecureSkipTLSVerify":
			transport.InsecureSkipTLSVerify = true
		default:
			return serverresolver.RegistryTransport{}, fmt.Errorf("invalid container.from transport input %q", value)
		}
	}
	return transport, nil
}

func resolveLatestContainerFromRef(ctx context.Context, query *Query, refString, platformString string, includeSubreleases bool, registryTransport serverresolver.RegistryTransport) (string, error) {
	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get registry resolver: %w", err)
	}

	refName, err := reference.ParseNormalizedNamed(refString)
	if err != nil {
		return "", fmt.Errorf("parse image address %q: %w", refString, err)
	}
	if _, ok := refName.(reference.NamedTagged); ok {
		return "", fmt.Errorf("latest-release image address %q must not include a tag", refString)
	}
	if _, ok := refName.(reference.Canonical); ok {
		return "", fmt.Errorf("latest-release image address %q must not include a digest", refString)
	}

	tags, err := rslvr.ListTags(ctx, refName.String(), serverresolver.ListTagsOpts{
		RegistryTransport: registryTransport,
	})
	if err != nil {
		return "", fmt.Errorf("list tags for image %q: %w", refName.String(), err)
	}
	tag, ok := SelectLatestReleaseTag(tags, includeSubreleases)
	if !ok {
		tag = "latest"
	}
	taggedRef, err := reference.WithTag(refName, tag)
	if err != nil {
		return "", fmt.Errorf("select image tag %q for %q: %w", tag, refString, err)
	}
	return resolveContainerFromRef(ctx, query, taggedRef.String(), platformString, registryTransport)
}

func resolveContainerFromRef(ctx context.Context, query *Query, refString, platformString string, registryTransport serverresolver.RegistryTransport) (string, error) {
	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get registry resolver: %w", err)
	}

	refName, err := reference.ParseNormalizedNamed(refString)
	if err != nil {
		return "", fmt.Errorf("parse image address %q: %w", refString, err)
	}
	if _, ok := refName.(reference.Canonical); !ok {
		refName = reference.TagNameOnly(refName)
	}

	platform, err := platforms.Parse(platformString)
	if err != nil {
		return "", fmt.Errorf("parse platform %q: %w", platformString, err)
	}

	_, resolvedDigest, _, err := rslvr.ResolveImageConfig(ctx, refName.String(), serverresolver.ResolveImageConfigOpts{
		Platform:          ptr(platform),
		ResolveMode:       serverresolver.ResolveModeDefault,
		RegistryTransport: registryTransport,
	})
	if err != nil {
		return "", fmt.Errorf("resolve image %q (platform: %q): %w", refName.String(), platformString, err)
	}
	refName, err = reference.WithDigest(refName, resolvedDigest)
	if err != nil {
		return "", fmt.Errorf("apply digest to image %q: %w", refName.String(), err)
	}

	return refName.String(), nil
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

func updateGitLatestLockEntry(ctx context.Context, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	if len(entry.Inputs) != 2 {
		return workspace.LookupResult{}, fmt.Errorf("invalid git.latest inputs %v", entry.Inputs)
	}
	remoteURL, ok := entry.Inputs[0].(string)
	if !ok || remoteURL == "" {
		return workspace.LookupResult{}, fmt.Errorf("invalid git.latest remote %v", entry.Inputs[0])
	}
	includeSubreleases, ok := entry.Inputs[1].(bool)
	if !ok {
		return workspace.LookupResult{}, fmt.Errorf("invalid git.latest includeSubreleases %v", entry.Inputs[1])
	}

	remote, err := loadRemoteGitMetadata(ctx, remoteURL)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	ref, err := SelectLatestGitReleaseRef(remote, includeSubreleases)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("resolve latest release for %q: %w", remoteURL, err)
	}
	return workspace.LookupResult{Value: FormatGitRefLockPin(ref), Policy: entry.Result.Policy}, nil
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

// SelectLatestGitReleaseRef returns the greatest semver tag in a remote, or
// HEAD when the remote has no release tags.
func SelectLatestGitReleaseRef(remote *gitutil.Remote, includeSubreleases bool) (*gitutil.Ref, error) {
	tag, ok := SelectLatestReleaseTag(remote.Tags().ShortNames(), includeSubreleases)
	if ok {
		return remote.Lookup("refs/tags/" + tag)
	}
	return remote.Lookup("HEAD")
}

// FormatGitRefLockPin stores both the Git ref name and the commit it resolved to.
func FormatGitRefLockPin(ref *gitutil.Ref) string {
	return ref.Name + "@" + ref.SHA
}

// ParseGitRefLockPin loads a ref name and commit from a lockfile value.
func ParseGitRefLockPin(pin string) (*gitutil.Ref, error) {
	i := strings.LastIndex(pin, "@")
	if i <= 0 || i == len(pin)-1 {
		return nil, fmt.Errorf("invalid git ref lock pin %q", pin)
	}
	name, sha := pin[:i], pin[i+1:]
	if !gitutil.IsCommitSHA(sha) {
		return nil, fmt.Errorf("invalid commit sha %q for %q", sha, name)
	}
	return &gitutil.Ref{Name: name, SHA: sha}, nil
}

// ParseGitLatestLockPin loads a git.latest lock pin and verifies that its ref
// has a shape produced by latest-release selection.
func ParseGitLatestLockPin(pin string, includeSubreleases bool) (*gitutil.Ref, error) {
	ref, err := ParseGitRefLockPin(pin)
	if err != nil {
		return nil, err
	}

	switch {
	case strings.HasPrefix(ref.Name, "refs/tags/"):
		tag := strings.TrimPrefix(ref.Name, "refs/tags/")
		if _, ok := SelectLatestReleaseTag([]string{tag}, includeSubreleases); !ok {
			return nil, fmt.Errorf("git latest lock pin ref %q is not a release tag", ref.Name)
		}
		return ref, nil
	case ref.Name == "HEAD" || strings.HasPrefix(ref.Name, "refs/heads/"):
		return ref, nil
	default:
		return nil, fmt.Errorf("git latest lock pin ref %q must be a release tag or HEAD branch", ref.Name)
	}
}

func loadRemoteGitMetadata(ctx context.Context, remoteURL string) (*gitutil.Remote, error) {
	candidates, err := gitutil.ParseCloneURL(remoteURL)
	if err != nil {
		return nil, fmt.Errorf("parse git URL %q: %w", remoteURL, err)
	}

	var lastErr error
	for _, gitURL := range candidates {
		repo := &RemoteGitRepository{URL: gitURL}
		remote, err := repo.Remote(ctx)
		if err != nil {
			if errors.Is(err, gitutil.ErrGitAuthFailed) {
				lastErr = err
				continue
			}
			return nil, fmt.Errorf("load git remote %q: %w", remoteURL, err)
		}
		return remote, nil
	}
	return nil, fmt.Errorf("load git remote %q: %w", remoteURL, lastErr)
}
