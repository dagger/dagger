package secretprovider

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/grpc/metadata"
)

// rejectedSecretValueMetadata carries only a SHA-256 fingerprint, never the
// secret itself. A live credential consumer uses it to report a rejected
// value so the owning client can refresh it even when its recorded expiry is
// still ahead.
const rejectedSecretValueMetadata = "x-dagger-rejected-env-value-sha256"

// ContextWithRejectedSecretValue marks a secret lookup with the SHA-256 hex
// fingerprint of a value its consumer has rejected. An empty fingerprint
// clears a prior rejection, including one inherited from incoming RPC
// metadata.
func ContextWithRejectedSecretValue(ctx context.Context, fingerprint string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(rejectedSecretValueMetadata, fingerprint)
	return metadata.NewOutgoingContext(ctx, md)
}

// RejectedSecretValue returns the rejected value's fingerprint at either end
// of a secret RPC. Outgoing metadata takes precedence to allow clearing it.
func RejectedSecretValue(ctx context.Context) string {
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if values := md.Get(rejectedSecretValueMetadata); len(values) > 0 {
			return values[0]
		}
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get(rejectedSecretValueMetadata); len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func envProvider(_ context.Context, name string) ([]byte, error) {
	v, ok := os.LookupEnv(name)
	if !ok {
		return nil, fmt.Errorf("secret env var not found: %q", redactEnvName(name))
	}
	return []byte(v), nil
}

// redactEnvName shortens a requested variable name for display. Users
// originally had to pass the secret *value* here rather than its name, and
// some still do by accident, so the name itself may be a credential.
func redactEnvName(name string) string {
	if len(name) >= 4 {
		return name[:3] + "..."
	}
	return name
}
