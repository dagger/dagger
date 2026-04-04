package schema

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/distribution/reference"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	lockCoreNamespace           = ""
	lockModulesResolveOperation = "modules.resolve"
)

type workspaceLookupLock struct {
	ctx   context.Context
	query *core.Query
	lock  *workspace.Lock
}

func loadWorkspaceLookupLock(ctx context.Context, query *core.Query) (*workspaceLookupLock, error) {
	lock, ok, err := query.CurrentWorkspaceLock(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	return &workspaceLookupLock{
		ctx:   ctx,
		query: query,
		lock:  lock,
	}, nil
}

func (l *workspaceLookupLock) SetLookup(namespace, operation string, inputs []any, result workspace.LookupResult) error {
	if l == nil {
		return fmt.Errorf("workspace lock is required")
	}
	if err := l.query.SetCurrentWorkspaceLookup(l.ctx, namespace, operation, inputs, result); err != nil {
		return err
	}
	if err := l.lock.SetLookup(namespace, operation, inputs, result); err != nil {
		return err
	}
	return nil
}

func currentLookupLockMode(ctx context.Context) (workspace.LockMode, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", fmt.Errorf("client metadata: %w", err)
	}
	return workspace.ResolveLockMode(clientMetadata.LockMode)
}

type lookupLockResolution struct {
	Pin         string
	Policy      workspace.LockPolicy
	ShouldWrite bool
	Found       bool
}

func resolveLookupFromLock(
	lockMode workspace.LockMode,
	lock *workspace.Lock,
	operation string,
	inputs []any,
	requestedPolicy workspace.LockPolicy,
) (lookupLockResolution, error) {
	resolution := lookupLockResolution{
		Policy: requestedPolicy,
	}

	if lockMode == workspace.LockModeDisabled {
		return resolution, nil
	}

	if lock != nil {
		if lockResult, ok, err := lock.GetLookup(lockCoreNamespace, operation, inputs); err != nil {
			return resolution, fmt.Errorf("invalid lock entry for %s %v: %w", operation, inputs, err)
		} else if ok {
			resolution.Found = true
			resolution.Policy = lockResult.Policy
			switch lockMode {
			case workspace.LockModeLive:
				resolution.ShouldWrite = true
				return resolution, nil
			case workspace.LockModeFrozen:
				resolution.Pin = lockResult.Value
				return resolution, nil
			case workspace.LockModePinned:
				if resolution.Policy == workspace.PolicyPin {
					resolution.Pin = lockResult.Value
				} else {
					resolution.ShouldWrite = true
				}
				return resolution, nil
			default:
				return resolution, fmt.Errorf("unsupported lock mode %q", lockMode)
			}
		}
	}

	switch lockMode {
	case workspace.LockModeLive:
		resolution.ShouldWrite = true
		return resolution, nil
	case workspace.LockModePinned:
		resolution.ShouldWrite = true
		return resolution, nil
	case workspace.LockModeFrozen:
		return resolution, fmt.Errorf("missing lock entry for %s %v", operation, inputs)
	default:
		return resolution, fmt.Errorf("unsupported lock mode %q", lockMode)
	}
}

func lockHostPath(ws *core.Workspace) (string, error) {
	return workspaceHostPath(ws, workspace.LockDirName, workspace.LockFileName)
}

func readWorkspaceLock(ctx context.Context, bk interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
}, ws *core.Workspace) (*workspace.Lock, error) {
	lock, _, err := readWorkspaceLockState(ctx, bk, ws)
	return lock, err
}

func readWorkspaceLockState(ctx context.Context, bk interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
}, ws *core.Workspace) (*workspace.Lock, bool, error) {
	lockPath, err := lockHostPath(ws)
	if err != nil {
		return nil, false, err
	}

	data, err := bk.ReadCallerHostFile(ctx, lockPath)
	if err != nil {
		if isWorkspaceLockNotFound(err) {
			return workspace.NewLock(), false, nil
		}
		return nil, false, fmt.Errorf("reading lock: %w", err)
	}

	lock, err := workspace.ParseLock(data)
	if err != nil {
		return nil, false, fmt.Errorf("parsing lock: %w", err)
	}
	return lock, true, nil
}

func isWorkspaceLockNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist) || status.Code(err) == codes.NotFound
}

func exportLockToHost(ctx context.Context, bk *buildkit.Client, ws *core.Workspace, lock *workspace.Lock) error {
	lockBytes, err := lock.Marshal()
	if err != nil {
		return fmt.Errorf("marshal lock: %w", err)
	}

	lockPath, err := lockHostPath(ws)
	if err != nil {
		return err
	}

	return exportWorkspaceFileToHost(ctx, bk, lockPath, lockBytes)
}

func ExportLockToHost(ctx context.Context, bk *buildkit.Client, ws *core.Workspace, lock *workspace.Lock) error {
	return exportLockToHost(ctx, bk, ws, lock)
}

func updateWorkspaceLock(ctx context.Context, query *core.Query, lock *workspace.Lock) error {
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

func updateWorkspaceLockEntry(ctx context.Context, query *core.Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
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

func updateContainerFromLockEntry(ctx context.Context, query *core.Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
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

func updateModuleResolveLockEntry(ctx context.Context, query *core.Query, entry workspace.LookupEntry) (workspace.LookupResult, error) {
	if len(entry.Inputs) != 1 {
		return workspace.LookupResult{}, fmt.Errorf("invalid %s inputs %v", lockModulesResolveOperation, entry.Inputs)
	}

	source, ok := entry.Inputs[0].(string)
	if !ok || source == "" {
		return workspace.LookupResult{}, fmt.Errorf("invalid %s source %v", lockModulesResolveOperation, entry.Inputs[0])
	}

	return resolveModuleSourceLookupResult(ctx, query, source, entry.Result.Policy)
}

func resolveContainerFromDigest(ctx context.Context, query *core.Query, refString, platformString string) (string, error) {
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

func resolveModuleSourceCommit(ctx context.Context, query *core.Query, source string) (string, error) {
	result, err := resolveModuleSourceLookupResult(ctx, query, source, "")
	if err != nil {
		return "", err
	}
	return result.Value, nil
}

func resolveModuleSourceLookupResult(
	ctx context.Context,
	query *core.Query,
	source string,
	policy workspace.LockPolicy,
) (workspace.LookupResult, error) {
	ctx = lookupRefreshContext(ctx)

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("buildkit client: %w", err)
	}

	parsedRef, err := core.ParseRefString(ctx, core.NewCallerStatFS(bk), source, "")
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("parse module source %q: %w", source, err)
	}
	if parsedRef.Kind != core.ModuleSourceKindGit {
		return workspace.LookupResult{}, fmt.Errorf("module source %q is not a git source", source)
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("query server: %w", err)
	}

	gitRef, err := parsedRef.Git.GitRef(ctx, dag, "")
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("resolve module source %q: %w", source, err)
	}

	if policy == "" {
		policy = moduleResolveLockPolicy(gitRef.Self().Ref)
	}

	return workspace.LookupResult{
		Value:  gitRef.Self().Ref.SHA,
		Policy: policy,
	}, nil
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
	ctx = lookupRefreshContext(ctx)

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", fmt.Errorf("query server: %w", err)
	}

	var repo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, srv.Root(), &repo,
		dagql.Selector{
			Field: "git",
			Args: []dagql.NamedInput{
				{Name: "url", Value: dagql.NewString(remoteURL)},
			},
		},
	); err != nil {
		return "", fmt.Errorf("load git repo %q: %w", remoteURL, err)
	}

	var ref dagql.ObjectResult[*core.GitRef]
	refSelector := dagql.Selector{Field: field}
	if name != "" {
		refSelector.Args = []dagql.NamedInput{{Name: "name", Value: dagql.NewString(name)}}
	}
	if err := srv.Select(ctx, repo, &ref, refSelector); err != nil {
		return "", fmt.Errorf("resolve %s %q for %q: %w", field, name, remoteURL, err)
	}

	var commit dagql.String
	if err := srv.Select(ctx, ref, &commit, dagql.Selector{Field: "commit"}); err != nil {
		return "", fmt.Errorf("load commit for %s %q: %w", field, name, err)
	}
	return commit.String(), nil
}

func lookupRefreshContext(ctx context.Context) context.Context {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return ctx
	}

	refreshed := *clientMetadata
	refreshed.LockMode = string(workspace.LockModeDisabled)
	return engine.ContextWithClientMetadata(ctx, &refreshed)
}
