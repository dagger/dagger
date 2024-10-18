package secretprovider

import (
	"context"
	"fmt"
	"os"
)

func envProvider(_ context.Context, name string) ([]byte, error) {
	v, ok := os.LookupEnv(name)
	if !ok {
		return nil, fmt.Errorf("secret env var not found: %q", name)
	}
	return []byte(v), nil
}
