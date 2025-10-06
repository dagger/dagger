package secretprovider

import (
	"context"
	"fmt"
	"os"
)

func envProvider(_ context.Context, name string) ([]byte, error) {
	v, ok := os.LookupEnv(name)
	if !ok {
		// Don't show the entire env var name, in case the user accidentally passed the value instead...
		// This is important because users originally *did* have to pass the value, before we changed to
		// passing by name instead.
		if len(name) >= 4 {
			name = name[:3] + "..."
		}
		return nil, fmt.Errorf("secret env var not found: %q", name)
	}
	return []byte(v), nil
}
