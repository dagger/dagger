package schema

import (
	"context"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// withUserConfig re-reads the calling host's user-level config and returns
// the workspace with the overlay matching its origin remote, or with none
// when nothing matches.
//
// A live workspace reads user config once, when the session loads it, and a
// snapshot carries that overlay in its recipe from then on, so nothing on the
// hot path ever touches the host's config file. This is the explicit refresh:
// an agent reloading its modules calls it to pick up config edits made since.
// The result is the parent plus __withUserConfigOverlay, so everything derived
// from it keys on the overlay through its ID, like any other workspace input.
func (s *workspaceSchema) withUserConfig(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var overlay *workspace.UserWorkspaceOverlay
	if key := core.WorkspaceOrigin(parent.Self()); key != "" {
		userCfg, err := callerUserConfig(ctx)
		if err != nil {
			return inst, err
		}
		overlay = userCfg.MatchWorkspaceOverlay(key)
	}
	return workspaceWithUserConfigOverlay(ctx, srv, parent, overlay)
}

// workspaceWithUserConfigOverlay records overlay on ws as a real selector, so
// the overlay is part of the result's identity and survives rebuilding the
// workspace from its recipe.
func workspaceWithUserConfigOverlay(
	ctx context.Context,
	srv *dagql.Server,
	ws dagql.ObjectResult[*core.Workspace],
	overlay *workspace.UserWorkspaceOverlay,
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	encoded, err := workspace.EncodeUserWorkspaceOverlay(overlay)
	if err != nil {
		return inst, err
	}
	err = srv.Select(ctx, ws, &inst, dagql.Selector{
		Field: "__withUserConfigOverlay",
		Args:  []dagql.NamedInput{{Name: "overlay", Value: dagql.NewString(encoded)}},
	})
	return inst, err
}

func (s *workspaceSchema) withUserConfigOverlay(
	_ context.Context,
	parent *core.Workspace,
	args struct{ Overlay string },
) (*core.Workspace, error) {
	overlay, err := workspace.DecodeUserWorkspaceOverlay(args.Overlay)
	if err != nil {
		return nil, err
	}
	ws := parent.Clone()
	ws.SetUserConfigOverlay(overlay)
	return ws, nil
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
