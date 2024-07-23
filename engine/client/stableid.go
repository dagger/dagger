package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/dagger/dagger/engine/slog"
	"github.com/moby/buildkit/identity"
)

const StableIDFileName = "stable_client_id"

// GetHostStableID returns a random ID that's persisted in the caller's XDG state directory.
// It's currently used to identify clients that are executing on the same host in order to
// tell buildkit which filesync cache ref to re-use when syncing dirs+files to the engine.
func GetHostStableID(lg *slog.Logger) string {
	id, err := internalGetStableID(filepath.Join(xdg.StateHome, "dagger"))
	if err != nil {
		lg.Warn("failed to get stable ID, defaulting to random value", "error", err)
		return identity.NewID()
	}
	return id
}

func internalGetStableID(parentDirPath string) (string, error) {
	if parentDirPath == "" {
		return "", errors.New("parentDirPath is not set")
	}

	stableIDPath := filepath.Join(parentDirPath, StableIDFileName)
	if stableID, err := os.ReadFile(stableIDPath); err == nil {
		// already exists
		return string(stableID), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read stable ID: %w", err)
	}

	if err := os.MkdirAll(parentDirPath, 0o700); err != nil {
		return "", fmt.Errorf("failed to create parent directory: %w", err)
	}

	// there's a race in the (obscure) case of clients concurrently running here,
	// but the worst case is some of the clients using different IDs, which is
	// just a temporary mild performance deficiency, so not worth more complication
	tmpFile, err := os.CreateTemp(parentDirPath, StableIDFileName)
	if err != nil {
		return "", fmt.Errorf("failed to create stable ID temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	stableID := identity.NewID()
	if _, err := tmpFile.WriteString(stableID); err != nil {
		return "", fmt.Errorf("failed to write stable ID: %w", err)
	}

	if err := os.Rename(tmpFile.Name(), stableIDPath); err != nil {
		return "", fmt.Errorf("failed to rename stable ID temp file: %w", err)
	}

	return stableID, nil
}
