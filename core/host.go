package core

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/sources/local"
	"github.com/vektah/gqlparser/v2/ast"
)

type Host struct{}

func (*Host) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Host",
		NonNull:   true,
	}
}

func (*Host) TypeDescription() string {
	return "Information about the host environment."
}

func (*Host) Directory(ctx context.Context, path string, filter CopyFilter, gitIgnore bool, noCache bool) (*Directory, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current query: %w", err)
	}

	bkGroupSession, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	snapshotOpts := local.SnapshotSyncOpts{
		IncludePatterns: filter.Include,
		ExcludePatterns: filter.Exclude,
		GitIgnore:       gitIgnore,
	}

	if noCache {
		snapshotOpts.CacheBuster = rand.Text()
	}

	ref, err := query.LocalSource().Snapshot(ctx, bkGroupSession, query.BuildkitSession(), path, snapshotOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot: %w", err)
	}

	dir := NewDirectory(nil, "/", query.Platform(), nil)
	dir.Result = ref

	return dir, nil

	// query.LocalSource().Snapshot(ctx, query.BuildkitSession(), query.BuildkitCache(), path, local.SnapshotSyncOpts{})
	// Should not impact performance
	// If args.IsDapOp -> do the actual snapshot
	// Otherwise:
	//
	//
	// Directory call Snapshot & manually set Directory.Result
	// Questions?
	// - How do I manually create a localSource or localSourceHandler to call snapshot?
	// - How do I create a raw Directory object to set Directory.Result = localSourceHandler.Snapshot?

	// Get the buildkit cache manager
	// 	bk, err := query.Buildkit(ctx)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	// }
	// cache := query.BuildkitCache()
	// session := query.BuildkitSession()
	//
	// Get the buildkit session
	// bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	// if !ok {
	// 	return nil, fmt.Errorf("no buildkit session group in context")
	// }
}

func (h *Host) Clone() *Host {
	cp := *h

	return &cp
}
