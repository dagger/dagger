package dagql

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/internal/buildkit/identity"

	"github.com/dagger/dagger/engine"
)

// CachePerClient scopes a call ID per client by mixing in the client ID as
// an implicit call input.
var CachePerClient = ImplicitInput{
	Name: "cachePerClient",
	Resolver: func(ctx context.Context, _ map[string]Input) (Input, error) {
		clientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata: %w", err)
		}
		if clientMD.ClientID == "" {
			return nil, fmt.Errorf("client ID not found in context")
		}
		return NewString(clientMD.ClientID), nil
	},
}

// CachePerSession scopes a call ID per session by mixing in the session ID as
// an implicit call input.
var CachePerSession = ImplicitInput{
	Name: "cachePerSession",
	Resolver: func(ctx context.Context, _ map[string]Input) (Input, error) {
		clientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata: %w", err)
		}
		if clientMD.SessionID == "" {
			return nil, fmt.Errorf("session ID not found in context")
		}
		return NewString(clientMD.SessionID), nil
	},
}

// CachePerCall scopes a call ID per invocation by mixing in a random value as
// an implicit call input.
var CachePerCall = ImplicitInput{
	Name: "cachePerCall",
	Resolver: func(context.Context, map[string]Input) (Input, error) {
		return NewString(identity.NewID()), nil
	},
}

// CachePerSchema scopes a call ID to the server schema digest.
func CachePerSchema(srv *Server) ImplicitInput {
	return ImplicitInput{
		Name: "cachePerSchema",
		Resolver: func(context.Context, map[string]Input) (Input, error) {
			return NewString(srv.SchemaDigest().String()), nil
		},
	}
}

// CacheAsRequested scopes a call ID according to a boolean argument:
// false => CachePerClient, true => CachePerCall.
func CacheAsRequested(argName string) ImplicitInput {
	return ImplicitInput{
		Name: "cacheAsRequested:" + argName,
		Resolver: func(ctx context.Context, args map[string]Input) (Input, error) {
			noCache, err := inputBoolArg(args, argName)
			if err != nil {
				return nil, err
			}
			if noCache {
				return CachePerCall.Resolver(ctx, args)
			}
			return CachePerClient.Resolver(ctx, args)
		},
	}
}

func inputBoolArg(args map[string]Input, argName string) (bool, error) {
	raw, ok := args[argName]
	if !ok || raw == nil {
		return false, nil
	}
	switch val := raw.(type) {
	case Boolean:
		return val.Bool(), nil
	case Optional[Boolean]:
		if !val.Valid {
			return false, nil
		}
		return val.Value.Bool(), nil
	case DynamicOptional:
		if !val.Valid {
			return false, nil
		}
		booleanVal, ok := val.Value.(Boolean)
		if !ok {
			return false, fmt.Errorf("cacheAsRequested input %q must wrap Boolean, got %T", argName, val.Value)
		}
		return booleanVal.Bool(), nil
	default:
		return false, fmt.Errorf("cacheAsRequested input %q must be Boolean, got %T", argName, raw)
	}
}
