package secretprovider

import (
	"context"
	"fmt"
	"os"

	"github.com/dagger/dagger/engine/client/pathutil"
)

func fileProvider(_ context.Context, path string) ([]byte, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path, err = pathutil.ExpandHomeDir(homeDir, path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret file %q: %w", path, err)
	}
	return data, nil
}
