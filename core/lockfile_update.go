package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/distribution/reference"
	digest "github.com/opencontainers/go-digest"
)

const (
	lockCoreNamespace          = ""
	lockContainerFromOperation = "container.from"
	lockGitLatestOperation     = "git.latest"
	lockGitRefOperation        = "git.ref"
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
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitLatestOperation:
		return updateGitLatestLockEntry(ctx, entry)
	case entry.Namespace == lockCoreNamespace && entry.Operation == lockGitRefOperation:
		return updateGitRefLockEntry(ctx, entry)
	default:
		return workspace.LookupResult{}, fmt.Errorf("unsupported lock entry %q %q", entry.Namespace, entry.Operation)
	}
}

type containerFromLockInputs struct {
	ref                      string
	platform                 string
	latestRelease            bool
	latestIncludeSubreleases bool
	registryTransport        serverresolver.RegistryTransport
}

func parseContainerFromLockInputs(inputs []any) (containerFromLockInputs, error) {
	var parsed containerFromLockInputs
	if len(inputs) < 2 {
		return parsed, fmt.Errorf("invalid %s inputs %v", lockContainerFromOperation, inputs)
	}

	ref, ok := inputs[0].(string)
	if !ok || ref == "" {
		return parsed, fmt.Errorf("invalid %s ref %v", lockContainerFromOperation, inputs[0])
	}
	parsed.ref = ref

	refName, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return parsed, fmt.Errorf("invalid %s ref %q: %w", lockContainerFromOperation, ref, err)
	}
	parsed.latestRelease = reference.IsNameOnly(refName)

	minInputs, maxInputs := 2, 4
	transportOffset := 2
	if parsed.latestRelease {
		minInputs, maxInputs = 3, 5
		transportOffset = 3
	}
	if len(inputs) < minInputs || len(inputs) > maxInputs {
		return parsed, fmt.Errorf("invalid %s inputs %v", lockContainerFromOperation, inputs)
	}

	platform, ok := inputs[1].(string)
	if !ok || platform == "" {
		return parsed, fmt.Errorf("invalid %s platform %v", lockContainerFromOperation, inputs[1])
	}
	parsed.platform = platform

	if parsed.latestRelease {
		includeSubreleases, ok := inputs[2].(bool)
		if !ok {
			return parsed, fmt.Errorf(
				"invalid %s latestIncludeSubreleases %v",
				lockContainerFromOperation,
				inputs[2],
			)
		}
		parsed.latestIncludeSubreleases = includeSubreleases
	}

	if len(inputs) > transportOffset {
		protocol, ok := inputs[transportOffset].(string)
		if !ok {
			return parsed, fmt.Errorf(
				"invalid %s registry protocol %v",
				lockContainerFromOperation,
				inputs[transportOffset],
			)
		}
		switch serverresolver.RegistryProtocol(protocol) {
		case serverresolver.RegistryProtocolHTTP, serverresolver.RegistryProtocolHTTPS:
			parsed.registryTransport.Protocol = serverresolver.RegistryProtocol(protocol)
		default:
			return parsed, fmt.Errorf("invalid %s registry protocol %q", lockContainerFromOperation, protocol)
		}
	}

	if len(inputs) == transportOffset+2 {
		marker, ok := inputs[transportOffset+1].(string)
		if !ok || marker != "insecureSkipTLSVerify" {
			return parsed, fmt.Errorf(
				"invalid %s registry transport option %v",
				lockContainerFromOperation,
				inputs[transportOffset+1],
			)
		}
		if parsed.registryTransport.Protocol != serverresolver.RegistryProtocolHTTPS {
			return parsed, fmt.Errorf(
				"invalid %s registry transport options %v",
				lockContainerFromOperation,
				inputs[transportOffset:],
			)
		}
		parsed.registryTransport.InsecureSkipTLSVerify = true
	}

	return parsed, nil
}

func updateContainerFromLockEntry(ctx context.Context, query *Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	inputs, err := parseContainerFromLockInputs(entry.Inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}

	if !inputs.latestRelease {
		resolvedDigest, err := resolveContainerFromDigest(ctx, query, inputs)
		if err != nil {
			return workspace.LookupResult{}, err
		}

		return workspace.LookupResult{
			Value:  resolvedDigest.String(),
			Policy: entry.Result.Policy,
		}, nil
	}

	refName, err := reference.ParseNormalizedNamed(inputs.ref)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("parse image address %q: %w", inputs.ref, err)
	}
	refName = reference.TrimNamed(refName)

	rslvr, err := query.RegistryResolver(ctx)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("failed to get registry resolver: %w", err)
	}
	// Encapsulate so the tag listing's raw HTTP spans stay out of the default
	// TUI; they surface if the listing fails.
	listCtx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("select latest release for %s", refName.String()),
		telemetry.Internal(), telemetry.Encapsulate())
	tags, err := rslvr.ListImageTags(listCtx, refName.String(), serverresolver.ListImageTagsOpts{
		RegistryTransport: inputs.registryTransport,
	})
	telemetry.EndWithCause(span, &err)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("list image tags for %q: %w", refName.String(), err)
	}
	refName, err = reference.WithTag(
		refName,
		SelectLatestContainerTag(tags, inputs.latestIncludeSubreleases),
	)
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

func updateGitRefLockEntry(ctx context.Context, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	remoteURL, name, err := parseGitLookupInputs("git.ref", entry.Inputs)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	result, err := resolveGitRef(ctx, remoteURL, name)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	return workspace.LookupResult{Value: result, Policy: entry.Result.Policy}, nil
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

func resolveGitRef(ctx context.Context, remoteURL, name string) (workspace.GitRefLockResult, error) {
	remote, err := loadRemoteGitMetadata(ctx, remoteURL)
	if err != nil {
		return workspace.GitRefLockResult{}, err
	}

	ref, err := remote.Lookup(name)
	if err != nil {
		return workspace.GitRefLockResult{}, fmt.Errorf("resolve git ref %q for %q: %w", name, remoteURL, err)
	}
	result := workspace.GitRefLockResult{SHA: ref.SHA}
	if ref.Name != "" && !gitutil.IsCommitSHA(ref.Name) {
		result.Ref = ref.Name
	}
	return result, nil
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
	if len(entry.Inputs) < 2 || len(entry.Inputs) > 3 {
		return workspace.LookupResult{}, fmt.Errorf("invalid git.latest inputs %v", entry.Inputs)
	}
	remoteURL, ok := entry.Inputs[0].(string)
	if !ok || remoteURL == "" {
		return workspace.LookupResult{}, fmt.Errorf("invalid git.latest remote %v", entry.Inputs[0])
	}
	includeSubreleases, ok := entry.Inputs[1].(bool)
	if !ok {
		return workspace.LookupResult{}, fmt.Errorf(
			"invalid git.latest includeSubreleases %v",
			entry.Inputs[1],
		)
	}
	var tagPrefix string
	if len(entry.Inputs) == 3 {
		var ok bool
		tagPrefix, ok = entry.Inputs[2].(string)
		if !ok || tagPrefix == "" {
			return workspace.LookupResult{}, fmt.Errorf(
				"invalid git.latest tag prefix %v",
				entry.Inputs[2],
			)
		}
	}

	// Resolve through the schema's git resolver rather than a bare
	// RemoteGitRepository so the same access context that created the pin
	// (credential helpers, SSH sockets, protocol fallback) applies here too.
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return workspace.LookupResult{}, err
	}

	latestInputs := []dagql.NamedInput{
		{Name: "includeSubreleases", Value: dagql.Boolean(includeSubreleases)},
	}
	if tagPrefix != "" {
		latestInputs = append(latestInputs, dagql.NamedInput{
			Name:  "tagPrefix",
			Value: dagql.String(tagPrefix),
		})
	}

	var latest dagql.ObjectResult[*GitRef]
	if err := srv.Select(ctx, srv.Root(), &latest,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.String(remoteURL)},
			},
		},
		dagql.Selector{
			Field: "latest",
			Args:  latestInputs,
		},
	); err != nil {
		return workspace.LookupResult{}, fmt.Errorf("resolve latest git release for %q: %w", remoteURL, err)
	}

	pin, err := EncodeGitRefPin(latest.Self().Ref)
	if err != nil {
		return workspace.LookupResult{}, err
	}
	return workspace.LookupResult{Value: pin, Policy: workspace.PolicyPin}, nil
}
