package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/opencontainers/go-digest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// workspaceUserConfigOverlay returns the user-level config overlay that
// applies to ws for the calling client.
//
// A live workspace carries the overlay its owning client resolved when the
// session loaded it. A value workspace (a snapshot, or Directory/GitRef
// asWorkspace) carries none: it is portable, and user config holds personal
// values that must not ride in its recipe. Its overlay is resolved at use
// instead, from the calling host's user config, keyed by the workspace's
// origin remote. That keeps a snapshot — the workspace an agent works on —
// configured the same as the checkout it was taken from.
func workspaceUserConfigOverlay(ctx context.Context, ws *core.Workspace) (*workspace.UserWorkspaceOverlay, error) {
	if ws == nil {
		return nil, nil
	}
	if !ws.IsValueWorkspace() {
		return ws.UserConfigOverlay(), nil
	}
	key := core.WorkspaceOrigin(ws)
	if key == "" {
		return nil, nil
	}
	userCfg, err := callerUserConfig(ctx)
	if err != nil || userCfg == nil {
		return nil, err
	}
	return userCfg.MatchWorkspaceOverlay(key), nil
}

// callerUserConfig reads the user-level config of the host the call comes
// from: the nearest non-module client, since module clients have no host of
// their own. Nil when that client sent no config path or the file is absent.
func callerUserConfig(ctx context.Context) (*workspace.UserConfig, error) {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	hostMD, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve user config client: %w", err)
	}
	if hostMD.UserConfigPath == "" {
		return nil, nil
	}
	hostCtx := engine.ContextWithClientMetadata(ctx, hostMD)
	bk, err := query.Engine(hostCtx)
	if err != nil {
		return nil, fmt.Errorf("engine client: %w", err)
	}
	data, err := bk.ReadCallerHostFile(hostCtx, hostMD.UserConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("reading user config %s: %w", hostMD.UserConfigPath, err)
	}
	userCfg, err := workspace.ParseUserConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parsing user config %s: %w", hostMD.UserConfigPath, err)
	}
	return userCfg, nil
}

// workspaceUserConfigCacheInput keys a cached call on a value workspace by
// the user-level overlay the caller resolves for it, since that overlay is
// read from the caller's host rather than carried in the workspace's ID.
// Callers with the same overlay (or none) share results; a caller with
// different user settings never sees another's. Live workspaces carry their
// overlay with them and need no extra key.
func workspaceUserConfigCacheInput[A any](
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ A,
	req *dagql.CallRequest,
) error {
	ws := parent.Self()
	if ws == nil || !ws.IsValueWorkspace() {
		return nil
	}
	overlay, err := workspaceUserConfigOverlay(ctx, ws)
	if err != nil || overlay == nil {
		return err
	}
	// encoding/json sorts map keys, so equal overlays digest equally.
	data, err := json.Marshal(overlay)
	if err != nil {
		return fmt.Errorf("digest user config overlay: %w", err)
	}
	return req.SetImplicitInput(ctx, "userConfig", dagql.NewString(digest.FromBytes(data).String()))
}
