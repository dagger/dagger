package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/session/fswatch"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/identity"
)

// Watch watches for changes to host-mutable resources referenced in the
// provided object, and sends updated versions of the object into a channel.
//
// XXX: how should we handle events being sent faster than they can be processed?
func Watch[T dagql.Typed](ctx context.Context, srv *dagql.Server, watcher fswatch.FSWatchClient, base dagql.ObjectResult[T], future chan<- dagql.ObjectResult[T]) error {
	client, err := watcher.Watch(ctx)
	if err != nil {
		return err
	}

	// send the initial ID is sent before any events are received
	// this handles the case where a host resource changes between
	// evaluating the original ID and starting the watch - without this, those
	// changes won't be processed
	obj, err := WatchStep(ctx, srv, client, base)
	if err != nil {
		return err
	}
	if obj != nil {
		base = *obj
		future <- base
	}

	for {
		msg, err := client.Recv()
		if err != nil {
			return err
		}
		switch msg.Event.(type) {
		case *fswatch.WatchResponse_FileEvents:
			slog.Info("got event", "event", msg.String())

			obj, err := WatchStep(ctx, srv, client, base)
			if err != nil {
				return err
			}
			if obj != nil {
				base = *obj
				future <- base
			}
		}
	}
}

func WatchStep[T dagql.Typed](ctx context.Context, srv *dagql.Server, client fswatch.FSWatch_WatchClient, original dagql.ObjectResult[T]) (*dagql.ObjectResult[T], error) {
	id, updateWatch, err := updateWatch(ctx, srv, original.ID())
	if err != nil {
		return nil, err
	}

	// TODO: avoid sending an update if nothing changed
	err = client.Send(&fswatch.WatchRequest{
		Request: &fswatch.WatchRequest_UpdateWatch{
			UpdateWatch: updateWatch,
		},
	})
	slog.Debug("sent update watch", "paths", updateWatch.Paths)
	if err != nil {
		return nil, err
	}

	if id == nil {
		slog.Info("no changes detected", "id", original.ID().Display(), "digest", original.ID().Digest())
		return nil, nil
	}

	res, err := srv.Load(ctx, id)
	if err != nil {
		return nil, err
	}

	slog.Info("sending new id", "id", id.Display(), "digest", id.Digest())
	obj, ok := res.(dagql.ObjectResult[T])
	if !ok {
		return nil, fmt.Errorf("expected result of type %T, got %T", obj, res)
	}

	return &obj, nil
}

// updateWatch creates a new ID and watch request based on the state of
// host-mutable resources.
// It returns an ID that reflects the new state of the host, as well as a watch
// request that will notify of future changes in the host.
func updateWatch(ctx context.Context, srv *dagql.Server, base *call.ID) (*call.ID, *fswatch.UpdateWatch, error) {
	var dirNewIDs []dagql.ID[*Directory]
	var dirOldIDs []dagql.ID[*Directory]
	var dirPaths []string

	var fileNewIDs []dagql.ID[*File]
	var fileOldIDs []dagql.ID[*File]
	var filePaths []string

	next, err := dagql.VisitID(base, func(id *call.ID) (*call.ID, error) {
		switch id.Type().NamedType() {
		case "Directory":
			dirID := dagql.NewID[*Directory](id)
			if dirID.ID().Receiver().Type().NamedType() != "Host" {
				break
			}
			if dirID.ID().Field() != "directory" {
				break
			}
			path := id.Arg("path").Value().(*call.LiteralString).Value()

			next := id.
				WithArgument(call.NewArgument("noCache", call.NewLiteralBool(true), false)).
				WithDigest(dagql.HashFrom(identity.NewID()))

			dirOldIDs = append(dirOldIDs, dagql.NewID[*Directory](id))
			dirNewIDs = append(dirNewIDs, dagql.NewID[*Directory](next))
			dirPaths = append(dirPaths, path)

			return next, nil
		case "File":
			// NOTE: this path is unused since Host.file rewrites it's ID

			fileID := dagql.NewID[*File](id)
			if fileID.ID().Receiver().Type().NamedType() != "Host" {
				break
			}
			if fileID.ID().Field() != "file" {
				break
			}
			path := id.Arg("path").Value().(*call.LiteralString).Value()

			next := id.
				WithArgument(call.NewArgument("noCache", call.NewLiteralBool(true), false)).
				WithDigest(dagql.HashFrom(identity.NewID()))

			fileOldIDs = append(fileOldIDs, dagql.NewID[*File](id))
			fileNewIDs = append(fileNewIDs, dagql.NewID[*File](next))
			filePaths = append(filePaths, path)

			return next, nil
		}

		return nil, nil
	})
	if err != nil {
		return nil, nil, err
	}

	oldDirs, err := dagql.LoadIDResults(ctx, srv, dirOldIDs)
	if err != nil {
		return nil, nil, err
	}
	newDirs, err := dagql.LoadIDResults(ctx, srv, dirNewIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(oldDirs) != len(newDirs) {
		return nil, nil, fmt.Errorf("internal error: mismatched directory lengths")
	}

	oldFiles, err := dagql.LoadIDResults(ctx, srv, fileOldIDs)
	if err != nil {
		return nil, nil, err
	}
	newFiles, err := dagql.LoadIDResults(ctx, srv, fileNewIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(oldFiles) != len(newFiles) {
		return nil, nil, fmt.Errorf("internal error: mismatched file lengths")
	}

	// check to see if anything actually changed
	// even though we got an event, it might not actually have changed any of
	// the IDs, e.g. we created and immediately removed a file, if both events
	// go batched into a single update
	match := true
	for i := range oldDirs {
		if oldDirs[i].ID().Digest() != newDirs[i].ID().Digest() {
			match = false
			break
		}
	}
	for i := range oldFiles {
		if oldFiles[i].ID().Digest() != newFiles[i].ID().Digest() {
			match = false
			break
		}
	}

	var paths []string
	for i, dir := range newDirs {
		dirPath := dirPaths[i]
		dirPath = filepath.Clean(dirPath)
		if !strings.HasSuffix(dirPath, "/") {
			dirPath += "/"
		}
		paths = append(paths, dirPath)

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
			paths = append(paths, dirPath+p)
		}
	}
	for i := range newFiles {
		filePath := filePaths[i]
		filePath = filepath.Clean(filePath)
		paths = append(paths, filePath)
	}

	watch := &fswatch.UpdateWatch{
		Paths: paths,
	}
	if match {
		return nil, watch, nil
	}
	return next, watch, nil
}
