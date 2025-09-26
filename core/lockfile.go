package core

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/session/lockfile"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
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
