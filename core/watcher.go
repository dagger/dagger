package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/session/fswatch"
	"github.com/vektah/gqlparser/v2/ast"
)

type Watcher struct {
	ClientMetadata engine.ClientMetadata
	Path           string
	Filter         CopyFilter

	Current dagql.ObjectResult[*Directory]
}

func NewWatcher(ctx context.Context, path string, filter CopyFilter) (*Watcher, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	watcher := &Watcher{
		ClientMetadata: *clientMetadata,
		Path:           path,
		Filter:         filter,
	}
	watcher.Current, err = watcher.refresh(ctx)
	if err != nil {
		return nil, err
	}

	return watcher, nil
}

func (*Watcher) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Watcher",
		NonNull:   true,
	}
}

func (*Watcher) TypeDescription() string {
	return "A directory watcher."
}

func (w *Watcher) refresh(ctx context.Context) (inst dagql.ObjectResult[*Directory], _ error) {
	srv := dagql.CurrentDagqlServer(ctx)
	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return inst, err
	}

	ctx = engine.ContextWithClientMetadata(ctx, &w.ClientMetadata)

	var dir dagql.ObjectResult[*Directory]
	err = srv.Select(ctx, srv.Root(), &dir, queryLocalDirectory(w.Path, w.Filter, true)...)
	if err != nil {
		return inst, err
	}

	dir, err = MakeDirectoryContentHashed(ctx, bk, dir)
	if err != nil {
		return inst, err
	}

	return dir, nil
}

func (w *Watcher) Watcher(ctx context.Context) (fswatch.FSWatch_WatchClient, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, err
	}

	ctx = engine.ContextWithClientMetadata(ctx, &w.ClientMetadata)
	client, err := bk.Watcher(ctx)
	if err != nil {
		return nil, err
	}
	watcher, err := client.Watch(ctx)
	if err != nil {
		return nil, err
	}

	err = watcher.Send(&fswatch.WatchRequest{
		Request: &fswatch.WatchRequest_UpdateWatch{
			UpdateWatch: &fswatch.UpdateWatch{
				Path:      w.Path,
				Gitignore: w.Filter.Gitignore,
				Include:   w.Filter.Include,
				Exclude:   w.Filter.Exclude,
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return watcher, nil
}

func (w *Watcher) Wait(ctx context.Context, watcher fswatch.FSWatch_WatchClient) (*Watcher, error) {
	// XXX: if there are multiple events to receive, we should consume them all
	prevDgst := w.Current.ID().Digest()
	for {
		resp, err := watcher.Recv()
		if err != nil {
			return nil, err
		}
		dt, _ := json.Marshal(resp)
		fmt.Println("watcher event:", string(dt))

		current, err := w.refresh(ctx)
		if err != nil {
			return nil, err
		}
		currentDgst := current.ID().Digest()
		if currentDgst != prevDgst {
			next := *w
			next.Current = current
			return &next, nil
		}
	}
}

// XXX: dedupe
func queryLocalDirectory(path string, filter CopyFilter, noCache bool) []dagql.Selector {
	args := []dagql.NamedInput{
		{
			Name:  "path",
			Value: dagql.NewString(path),
		},
	}
	if len(filter.Exclude) > 0 {
		args = append(args, dagql.NamedInput{
			Name:  "exclude",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(filter.Exclude...)),
		})
	}
	if len(filter.Include) > 0 {
		args = append(args, dagql.NamedInput{
			Name:  "include",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(filter.Include...)),
		})
	}
	if filter.Gitignore {
		args = append(args, dagql.NamedInput{
			Name:  "gitignore",
			Value: dagql.Boolean(true),
		})
	}
	if noCache {
		args = append(args, dagql.NamedInput{
			Name:  "noCache",
			Value: dagql.Boolean(true),
		})
	}
	return []dagql.Selector{
		{Field: "host"},
		{Field: "directory", Args: args},
	}
}
