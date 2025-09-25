package core

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/sources/local"

	"github.com/dagger/dagger/engine"
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

// Lookup an environment variable in the host system from the current context
func (Host) GetEnv(ctx context.Context, name string) string {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return ""
	}
	secretStore, err := query.Secrets(ctx)
	if err != nil {
		return ""
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return ""
	}
	plaintext, err := secretStore.GetSecretPlaintextDirect(ctx, &Secret{
		URI:               "env://" + name,
		BuildkitSessionID: clientMetadata.ClientID,
	})
	if err != nil {
		return ""
	}
	return string(plaintext)
}

// find-up a given soughtName in curDirPath and its parent directories,
// return the absolute path to the dir it was found in, if any
func (Host) FindUp(
	ctx context.Context,
	statFS StatFS,
	curDirPath string,
	soughtName string,
) (string, bool, error) {
	found, err := Host{}.FindUpAll(ctx, statFS, curDirPath, map[string]struct{}{soughtName: {}})
	if err != nil {
		return "", false, err
	}
	p, ok := found[soughtName]
	return p, ok, nil
}

// find-up a set of soughtNames in curDirPath and its parent directories return what
// was found (name -> absolute path of dir containing it)
func (Host) FindUpAll(
	ctx context.Context,
	statFS StatFS,
	curDirPath string,
	soughtNames map[string]struct{},
) (map[string]string, error) {
	found := make(map[string]string, len(soughtNames))
	for {
		for soughtName := range soughtNames {
			stat, err := statFS.Stat(ctx, filepath.Join(curDirPath, soughtName))
			if err == nil {
				delete(soughtNames, soughtName)
				// NOTE: important that we use stat.Path here rather than curDirPath since the stat also
				// does some normalization of paths when the client is using case-insensitive filesystems
				// and we are stat'ing caller host filesystems
				found[soughtName] = filepath.Dir(stat.Path)
				continue
			}
			if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("failed to lstat %s: %w", soughtName, err)
			}
		}

		if len(soughtNames) == 0 {
			// found everything
			break
		}

		nextDirPath := filepath.Dir(curDirPath)
		if curDirPath == nextDirPath {
			// hit root, nowhere else to look
			break
		}
		curDirPath = nextDirPath
	}

	return found, nil
}

func (*Host) Directory(ctx context.Context, rootPath string, filter CopyFilter, gitIgnore bool, noCache bool, relPath string) (*Directory, error) {
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
		RelativePath:    relPath,
	}

	if noCache {
		snapshotOpts.CacheBuster = rand.Text()
	}

	ref, err := query.LocalSource().Snapshot(ctx, bkGroupSession, query.BuildkitSession(), rootPath, snapshotOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot: %w", err)
	}

	dir := NewDirectory(nil, "/", query.Platform(), nil)
	dir.Result = ref

	return dir, nil
}

func (h *Host) Clone() *Host {
	cp := *h

	return &cp
}
