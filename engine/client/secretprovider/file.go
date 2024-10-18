package secretprovider

import (
	"context"
	"os"
)

func fileProvider(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}
