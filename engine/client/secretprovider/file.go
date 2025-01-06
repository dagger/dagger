package secretprovider

import (
	"context"
	"fmt"
	"os"

	"github.com/mitchellh/go-homedir"
)

func fileProvider(_ context.Context, path string) ([]byte, error) {
	path, err := homedir.Expand(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret file %q: %w", path, err)
	}

	return data, nil
}
