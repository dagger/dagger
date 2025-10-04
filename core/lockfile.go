package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/session/lockfile"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/util/gitutil"
)

func LockfileGetHTTP(ctx context.Context, url string) (string, error) {
	args := []any{url}
	result, err := lockfileGet(ctx, "core", "http.get", args)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	if s, ok := result.(string); ok {
		return s, nil
	}
	return "", nil
}

func LockfileSetHTTP(ctx context.Context, url, digest string) error {
	args := []any{url}
	return lockfileSet(ctx, "core", "http.get", args, digest)
}

func LockfileSetGitLsRemote(ctx context.Context, url string, info *gitutil.Remote) error {
	// Set the head
	if info.Head != nil {
		if err := lockfileSet(ctx, "core", "git.head", []any{url}, info.Head.Name); err != nil {
			return err
		}
		if err := lockfileSet(ctx, "core", "git.ref", []any{url, info.Head.Name}, info.Head.SHA); err != nil {
			return err
		}
	}

	// Set all refs
	refNames := make([]string, len(info.Refs))
	for i, ref := range info.Refs {
		refNames[i] = ref.Name
		if err := lockfileSet(ctx, "core", "git.ref", []any{url, ref.Name}, ref.SHA); err != nil {
			return err
		}
	}
	if err := lockfileSet(ctx, "core", "git.refs", []any{url}, refNames); err != nil {
		return err
	}

	// Set symrefs
	if err := lockfileSetStringMap(ctx, "core", "git.symrefs", []any{url}, info.Symrefs); err != nil {
		return err
	}

	return nil
}

func LockfileGetGitLsRemote(ctx context.Context, url string) (*gitutil.Remote, error) {
	var result gitutil.Remote
	headName, err := lockfileGetString(ctx, "core", "git.head", []any{url})
	if err != nil {
		return nil, err
	}
	if headName != nil {
		headSHA, err := lockfileGetString(ctx, "core", "git.ref", []any{url, *headName})
		if err != nil {
			return nil, err
		}
		if headSHA != nil {
			result.Head = &gitutil.Ref{
				Name: *headName,
				SHA:  *headSHA,
			}
		}
	}
	refs, err := lockfileGetStringArray(ctx, "core", "git.refs", []any{url})
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		sha, err := lockfileGetString(ctx, "core", "git.ref", []any{url, ref})
		if err != nil {
			return nil, err
		}
		if sha != nil {
			result.Refs = append(result.Refs, &gitutil.Ref{
				Name: ref,
				SHA:  *sha,
			})
		}
	}
	symrefs, err := lockfileGetStringMap(ctx, "core", "git.symrefs", []any{url})
	if err != nil {
		return nil, err
	}
	result.Symrefs = symrefs
	if result.Head == nil && len(result.Refs) == 0 && len(result.Symrefs) == 0 {
		return nil, nil
	}
	return &result, nil
}

func LockfileGetGitPublic(ctx context.Context, url string) (*bool, error) {
	args := []any{url}
	result, err := lockfileGet(ctx, "core", "git.isPublic", args)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	if s, ok := result.(string); ok {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, err
		}
		return &b, nil
	}
	if b, ok := result.(bool); ok {
		return &b, nil
	}
	return nil, nil
}

func LockfileSetGitPublic(ctx context.Context, url string, public bool) error {
	args := []any{url}
	publicStr := strconv.FormatBool(public)
	return lockfileSet(ctx, "core", "git.isPublic", args, publicStr)
}

func LockfileGetGitRef(ctx context.Context, url, ref string) (string, error) {
	args := []any{url, ref}
	result, err := lockfileGet(ctx, "core", "git.resolve", args)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	if s, ok := result.(string); ok {
		return s, nil
	}
	return "", nil
}

func LockfileSetGitRef(ctx context.Context, url, ref, digest string) error {
	args := []any{url, ref}
	return lockfileSet(ctx, "core", "git.resolve", args, digest)
}

func LockfileGetContainer(ctx context.Context, ref, platform string) (string, error) {
	args := []any{ref, platform}
	result, err := lockfileGet(ctx, "core", "container.from", args)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	if s, ok := result.(string); ok {
		return s, nil
	}
	return "", nil
}

func LockfileSetContainer(ctx context.Context, ref, platform, digest string) error {
	args := []any{ref, platform}
	return lockfileSet(ctx, "core", "container.from", args, digest)
}

func lockfileSet(ctx context.Context, module, function string, args []any, result any) error {
	// Convert args to JSON strings for the RPC
	jsonArgs := make([]string, len(args))
	for i, arg := range args {
		argJSON, err := json.Marshal(arg)
		if err != nil {
			return err
		}
		jsonArgs[i] = string(argJSON)
	}

	// Convert result to JSON
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}

	return withLockFile(ctx, func(ctx context.Context, lf lockfile.LockfileClient) error {
		_, err := lf.Set(ctx, &lockfile.SetRequest{
			Module:   module,
			Function: function,
			Args:     jsonArgs,
			Result:   string(resultJSON),
		})
		return err
	})
}

func lockfileGetString(ctx context.Context, module, function string, args []any) (*string, error) {
	raw, err := lockfileGet(ctx, module, function, args)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	if s, ok := raw.(string); ok {
		return &s, nil
	}
	return nil, nil
}

func lockfileSetStringMap(ctx context.Context, module, function string, args []any, data map[string]string) error {
	// Convert map to sorted array of tuples
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	tuples := make([][]string, len(keys))
	for i, key := range keys {
		tuples[i] = []string{key, data[key]}
	}

	return lockfileSet(ctx, module, function, args, tuples)
}

func lockfileGetStringMap(ctx context.Context, module, function string, args []any) (map[string]string, error) {
	raw, err := lockfileGet(ctx, module, function, args)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	if arr, ok := raw.([]interface{}); ok {
		result := make(map[string]string)
		for i, item := range arr {
			if tuple, ok := item.([]interface{}); ok && len(tuple) == 2 {
				if key, ok := tuple[0].(string); ok {
					if value, ok := tuple[1].(string); ok {
						result[key] = value
					} else {
						return nil, fmt.Errorf("failed to decode lockfile string map: tuple value at index %d is not a string, got %T: %v", i, tuple[1], tuple[1])
					}
				} else {
					return nil, fmt.Errorf("failed to decode lockfile string map: tuple key at index %d is not a string, got %T: %v", i, tuple[0], tuple[0])
				}
			} else {
				return nil, fmt.Errorf("failed to decode lockfile string map: item at index %d is not a 2-element tuple, got %T with length %d: %v", i, item, len(tuple), item)
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("failed to decode lockfile string map: raw data is not an array, got %T: %v", raw, raw)
}

func lockfileGetStringArray(ctx context.Context, module, function string, args []any) ([]string, error) {
	raw, err := lockfileGet(ctx, module, function, args)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	if arr, ok := raw.([]interface{}); ok {
		result := make([]string, len(arr))
		for i, item := range arr {
			if s, ok := item.(string); ok {
				result[i] = s
			} else {
				return nil, nil
			}
		}
		return result, nil
	}
	return nil, nil
}

func lockfileGet(ctx context.Context, module, function string, args []any) (any, error) {
	var result any

	// Convert args to JSON strings for the RPC
	jsonArgs := make([]string, len(args))
	for i, arg := range args {
		argJSON, err := json.Marshal(arg)
		if err != nil {
			return nil, err
		}
		jsonArgs[i] = string(argJSON)
	}

	err := withLockFile(ctx, func(ctx context.Context, lf lockfile.LockfileClient) error {
		resp, err := lf.Get(ctx, &lockfile.GetRequest{
			Module:   module,
			Function: function,
			Args:     jsonArgs,
		})
		if err != nil {
			return err
		}

		// Empty string means cache miss
		if resp.Result == "" {
			result = nil
			return nil
		}

		// Unmarshal the result
		if err := json.Unmarshal([]byte(resp.Result), &result); err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func withLockFile(ctx context.Context, fn func(context.Context, lockfile.LockfileClient) error) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	// Elevate context to nearest non-module client
	mainClient, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return err
	}
	currentClient, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	ctx = engine.ContextWithClientMetadata(ctx, mainClient)

	bkSessionGroup, hasbkSessionGroup := buildkit.CurrentBuildkitSessionGroup(ctx)

	sessionManager := query.BuildkitSession()
	if !hasbkSessionGroup {
		// Create one from client metadata (during GraphQL resolution)
		bkSessionGroup = bksession.NewGroup(mainClient.ClientID, currentClient.ClientID)
	}
	return sessionManager.Any(
		ctx,
		bkSessionGroup,
		func(ctx context.Context, id string, c bksession.Caller) error {
			return fn(ctx, lockfile.NewLockfileClient(c.Conn()))
		},
	)
}
