package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core/workspace"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/distribution/reference"
	digest "github.com/opencontainers/go-digest"
)

const (
	lockCoreNamespace                = ""
	lockContainerFromOperation       = "container.from"
	lockContainerFromLatestOperation = "container.from.latest"
	lockGitHeadOperation             = "git.head"
	lockGitLatestOperation           = "git.latest"
	lockGitRefOperation              = "git.ref"
	lockGitBranchOperation           = "git.branch"
	lockGitTagOperation              = "git.tag"
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
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockContainerFromLatestOperation:
		return updateContainerFromLatestLockEntry(ctx, query, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitHeadOperation:
		return updateGitHeadLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitLatestOperation:
		return updateGitLatestLockEntry(ctx, entry)
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

type containerFromLockInputs struct {
	ref               string
	platform          string
	registryTransport serverresolver.RegistryTransport
}

func parseContainerFromLockInputs(operation string, inputs []any) (containerFromLockInputs, error) {
	var parsed containerFromLockInputs
	if len(inputs) < 2 || len(inputs) > 4 {
		return parsed, fmt.Errorf("invalid %s inputs %v", operation, inputs)
	}

	ref, ok := inputs[0].(string)
	if !ok || ref == "" {
		return parsed, fmt.Errorf("invalid %s ref %v", operation, inputs[0])
	}
	parsed.ref = ref

	platform, ok := inputs[1].(string)
	if !ok || platform == "" {
		return parsed, fmt.Errorf("invalid %s platform %v", operation, inputs[1])
	}
	parsed.platform = platform

	if len(inputs) >= 3 {
		protocol, ok := inputs[2].(string)
		if !ok {
			return parsed, fmt.Errorf("invalid %s registry protocol %v", operation, inputs[2])
		}
		switch serverresolver.RegistryProtocol(protocol) {
		case serverresolver.RegistryProtocolHTTP, serverresolver.RegistryProtocolHTTPS:
			parsed.registryTransport.Protocol = serverresolver.RegistryProtocol(protocol)
		default:
			return parsed, fmt.Errorf("invalid %s registry protocol %q", operation, protocol)
		}
	}

	if len(inputs) == 4 {
		marker, ok := inputs[3].(string)
		if !ok || marker != "insecureSkipTLSVerify" {
			return parsed, fmt.Errorf("invalid %s registry transport option %v", operation, inputs[3])
		}
		if parsed.registryTransport.Protocol != serverresolver.RegistryProtocolHTTPS {
			return parsed, fmt.Errorf("invalid %s registry transport options %v", operation, inputs[2:])
		}
		parsed.registryTransport.InsecureSkipTLSVerify = true
	}

	return parsed, nil
}

func updateContainerFromLockEntry(ctx context.Context, query *Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	inputs, err := parseContainerFromLockInputs(lockContainerFromOperation, entry.Inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}

	resolvedDigest, err := resolveContainerFromDigest(ctx, query, inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}

	return workspace.LookupResult{
		Value:  resolvedDigest.String(),
		Policy: entry.Result.Policy,
	}, nil
}

func updateContainerFromLatestLockEntry(ctx context.Context, query *Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	inputs, err := parseContainerFromLockInputs(lockContainerFromLatestOperation, entry.Inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}

	refName, err := reference.ParseNormalizedNamed(inputs.ref)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("parse image address %q: %w", inputs.ref, err)
	}
	if !reference.IsNameOnly(refName) {
		return workspace.LookupResult{}, fmt.Errorf(
			"invalid %s ref %q: expected an image repository without a tag or digest",
			lockContainerFromLatestOperation,
			inputs.ref,
		)
	}
	refName = reference.TrimNamed(refName)

	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("failed to get registry resolver: %w", err)
	}
	tags, err := rslvr.ListImageTags(ctx, refName.String(), serverresolver.ListImageTagsOpts{
		RegistryTransport: inputs.registryTransport,
	})
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("list image tags for %q: %w", refName.String(), err)
	}
	refName, err = reference.WithTag(refName, SelectLatestContainerTag(tags))
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("select latest release for image %q: %w", inputs.ref, err)
	}

	inputs.ref = refName.String()
	resolvedDigest, err := resolveContainerFromDigest(ctx, query, inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	refName, err = reference.WithDigest(refName, resolvedDigest)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("pin image %q: %w", inputs.ref, err)
	}

	return workspace.LookupResult{
		Value:  refName.String(),
		Policy: workspace.PolicyPin,
	}, nil
}

func resolveContainerFromDigest(ctx context.Context, query *Query, inputs containerFromLockInputs) (digest.Digest, error) {
	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get registry resolver: %w", err)
	}

	refName, err := reference.ParseNormalizedNamed(inputs.ref)
	if err != nil {
		return "", fmt.Errorf("parse image address %q: %w", inputs.ref, err)
	}
	refName = reference.TagNameOnly(refName)

	platform, err := platforms.Parse(inputs.platform)
	if err != nil {
		return "", fmt.Errorf("parse platform %q: %w", inputs.platform, err)
	}

	_, resolvedDigest, _, err := rslvr.ResolveImageConfig(ctx, refName.String(), serverresolver.ResolveImageConfigOpts{
		Platform:          ptr(platform),
		ResolveMode:       serverresolver.ResolveModeDefault,
		RegistryTransport: inputs.registryTransport,
	})
	if err != nil {
		return "", fmt.Errorf("resolve image %q (platform: %q): %w", refName.String(), inputs.platform, err)
	}

	return resolvedDigest, nil
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

func updateGitLatestLockEntry(ctx context.Context, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	if len(entry.Inputs) != 1 {
		return workspace.LookupResult{}, fmt.Errorf("invalid git.latest inputs %v", entry.Inputs)
	}
	remoteURL, ok := entry.Inputs[0].(string)
	if !ok || remoteURL == "" {
		return workspace.LookupResult{}, fmt.Errorf("invalid git.latest remote %v", entry.Inputs[0])
	}
	remote, err := loadRemoteGitMetadata(ctx, remoteURL)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	ref, err := SelectLatestGitRef(remote)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("resolve latest git release for %q: %w", remoteURL, err)
	}
	pin, err := EncodeGitRefPin(ref)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	return workspace.LookupResult{Value: pin, Policy: workspace.PolicyPin}, nil
}
