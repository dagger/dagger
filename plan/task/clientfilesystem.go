package task

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"github.com/gofrs/flock"
	"github.com/mitchellh/go-homedir"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
)

const clientFSTempKey = "client-filesystem-lock"

// clientFSPath extracts the absolute file path from a client filesystem path value.
func clientFSPath(v *compiler.Value) (string, error) {
	path, err := v.String()
	if err != nil {
		return "", err
	}
	expanded, err := homedir.Expand(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

// clientFSLock acquires a file lock to read/write a client filesystem resource.
// Every path gets it's own lock. This allows multiple read/write to the same
// resource, protecting against race conditions.
func clientFSLock(ctx context.Context, pctx *plancontext.Context, fsPath string) (*flock.Flock, error) {
	lg := log.Ctx(ctx)

	// Use a temporary dir for this session so it gets cleaned up in the end
	dir, err := pctx.TempDirs.GetOrCreate(clientFSTempKey)
	if err != nil {
		return nil, err
	}

	lp := filepath.Join(dir, fmt.Sprintf("clientfs-%x.lock", sha256.Sum256([]byte(fsPath))))

	lg.Trace().Str("path", fsPath).Str("lockPath", lp).Msg("acquiring client filesystem lock")

	fileLock := flock.New(lp)
	if err := fileLock.Lock(); err != nil {
		return nil, fmt.Errorf("failed to acquire lock: %w", err)
	}

	lg.Trace().Str("path", fsPath).Msg("acquired client filesystem lock")

	return fileLock, nil
}
