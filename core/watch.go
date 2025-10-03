package core

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/session/fswatch"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

// XXX: how should we handle events being sent faster than they can be processed?
func Watch[T dagql.Typed](ctx context.Context, srv *dagql.Server, watcher fswatch.FSWatchClient, target dagql.ObjectResult[T], future chan<- dagql.ObjectResult[T]) error {
	client, err := watcher.Watch(ctx)
	if err != nil {
		return err
	}

	id, updateWatch, err := watchStep(ctx, srv, target.ID())
	if err != nil {
		return err
	}
	err = client.Send(&fswatch.WatchRequest{
		Request: &fswatch.WatchRequest_UpdateWatch{
			UpdateWatch: updateWatch,
		},
	})
	if err != nil {
		return err
	}
	// send the initial ID is sent before any events are received
	// this handles the case where a host resource changes between
	// evaluating the original ID and starting the watch - without this, those
	// changes won't be processed
	slog.Info("sending new id", "id", id.Display(), "digest", id.Digest())
	res, err := srv.Load(ctx, id)
	if err != nil {
		return err
	}
	target, ok := res.(dagql.ObjectResult[T])
	if !ok {
		return fmt.Errorf("expected result of type %T, got %T", target, res)
	}
	future <- target

	for {
		msg, err := client.Recv()
		if err != nil {
			return err
		}
		switch msg.Event.(type) {
		case *fswatch.WatchResponse_FileEvents:
			slog.Info("got event", "event", msg.String())

			id, updateWatch, err := watchStep(ctx, srv, target.ID())
			if err != nil {
				return err
			}
			err = client.Send(&fswatch.WatchRequest{
				Request: &fswatch.WatchRequest_UpdateWatch{
					UpdateWatch: updateWatch,
				},
			})
			if err != nil {
				return err
			}

			slog.Info("sending new id", "id", id.Display(), "digest", id.Digest())
			res, err := srv.Load(ctx, id)
			if err != nil {
				return err
			}
			target, ok := res.(dagql.ObjectResult[T])
			if !ok {
				return fmt.Errorf("expected result of type %T, got %T", target, res)
			}
			future <- target
		}
	}
}

// watchStep creates a new ID and watch request based on the state of
// host-mutable resources.
// It returns an ID that reflects the new state of the host, as well as a watch
// request that will notify of future changes in the host.
func watchStep(ctx context.Context, srv *dagql.Server, base *call.ID) (*call.ID, *fswatch.UpdateWatch, error) {
	// TODO: watch other host resources:
	// - Host.file
	var dirIDs []dagql.ID[*Directory]
	var dirSources []string
	next, err := dagql.VisitID(base, func(id *call.ID) (*call.ID, error) {
		if id.Type().NamedType() != "Directory" {
			return nil, nil
		}
		dirID := dagql.NewID[*Directory](id)
		if dirID.ID().Receiver().Type().NamedType() != "Host" {
			return nil, nil
		}
		if dirID.ID().Field() != "directory" {
			return nil, nil
		}
		path := id.Arg("path").Value().(*call.LiteralString).Value()

		next := id.
			WithArgument(call.NewArgument("noCache", call.NewLiteralBool(true), false)).
			WithDigest(dagql.HashFrom(identity.NewID()))

		dirIDs = append(dirIDs, dagql.NewID[*Directory](next))
		dirSources = append(dirSources, path)
		return next, nil
	})
	if err != nil {
		return nil, nil, err
	}

	dirs, err := dagql.LoadIDResults(ctx, srv, dirIDs)
	if err != nil {
		return nil, nil, err
	}
	var paths []string
	for i, dir := range dirs {
		dirSource := dirSources[i]
		dirSource = filepath.Clean(dirSource)
		paths = append(paths, dirSource)

		var globs []string
		err := srv.Select(ctx, dir, &globs,
			dagql.Selector{
				Field: "glob",
				Args: []dagql.NamedInput{
					{Name: "pattern", Value: dagql.NewString("**")},
				},
			},
		)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range globs {
			// don't use filepath.Join, preserve the glob's (potentially)
			// trailing slash
			paths = append(paths, dirSource+"/"+p)
		}
	}

	return next, &fswatch.UpdateWatch{
		Paths: paths,
	}, nil
}
