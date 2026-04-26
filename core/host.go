package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/dagql"
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

func (*Host) EncodePersistedObject(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (*Host) DecodePersistedObject(context.Context, *dagql.Server, uint64, *dagql.ResultCall, json.RawMessage) (dagql.Typed, error) {
	return &Host{}, nil
}

// Lookup an environment variable in the host system from the current context
func (Host) GetEnv(ctx context.Context, name string) string {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return ""
	}
	plaintext, err := (&Secret{
		URIVal:         "env://" + name,
		SourceClientID: clientMetadata.ClientID,
	}).Plaintext(ctx)
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
			dirName, exists, err := StatFSExists(ctx, statFS, filepath.Join(curDirPath, soughtName))
			if err != nil {
				return nil, fmt.Errorf("failed to lstat %s: %w", soughtName, err)
			}
			if exists {
				delete(soughtNames, soughtName)
				// NOTE: important that we use dirName here rather than curDirPath since the stat also
				// does some normalization of paths when the client is using case-insensitive filesystems
				// and we are stat'ing caller host filesystems
				found[soughtName] = dirName
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

func (h *Host) Clone() *Host {
	cp := *h

	return &cp
}
