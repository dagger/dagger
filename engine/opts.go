package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core/pipeline"
	controlapi "github.com/moby/buildkit/api/services/control"
	"google.golang.org/grpc/metadata"
)

const clientMetadataMetaKey = "x-dagger-client-metadata"

type ClientMetadata struct {
	// ClientID is unique to every session created by every client
	ClientID string `json:"client_id"`

	// ClientSecretToken is a secret token that is unique to every client. It's
	// initially provided to the server in the controller.Solve request. Every
	// other request w/ that client ID must also include the same token.
	ClientSecretToken string `json:"client_secret_token"`

	// ServerID is the id of the server that a client and any of its nested
	// environment clients connect to
	ServerID string `json:"server_id"`

	// If RegisterClient is true, then a call to Session will initialize the
	// server if it hasn't already been initialized and register the session's
	// attachables with it either way. If false, then the session conn will be
	// forwarded to the server
	RegisterClient bool `json:"register_client"`

	// ClientHostname is the hostname of the client that made the request. It's
	// used opportunisticly as a best-effort, semi-stable identifier for the
	// client across multiple sessions, which can be useful for debugging and for
	// minimizing occurrences of both excessive cache misses and excessive cache
	// matches.
	ClientHostname string `json:"client_hostname"`

	// (Optional) Pipeline labels for e.g. vcs info like branch, commit, etc.
	Labels []pipeline.Label `json:"labels"`

	// ParentClientIDs is a list of session ids that are parents of the current
	// session. The first element is the direct parent, the second element is the
	// parent of the parent, and so on.
	ParentClientIDs []string `json:"parent_client_ids"`

	// Import configuration for Buildkit's remote cache
	UpstreamCacheConfig []*controlapi.CacheOptionsEntry
}

// ClientIDs returns the ClientID followed by ParentClientIDs.
func (m ClientMetadata) ClientIDs() []string {
	return append([]string{m.ClientID}, m.ParentClientIDs...)
}

func (m ClientMetadata) ToGRPCMD() metadata.MD {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return metadata.Pairs(clientMetadataMetaKey, string(b))
}

func (m ClientMetadata) AppendToMD(md metadata.MD) metadata.MD {
	for k, v := range m.ToGRPCMD() {
		md[k] = append(md[k], v...)
	}
	return md
}

func contextWithMD(ctx context.Context, mds ...metadata.MD) context.Context {
	incomingMD, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		incomingMD = metadata.MD{}
	}
	outgoingMD, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		outgoingMD = metadata.MD{}
	}
	for _, md := range mds {
		for k, v := range md {
			incomingMD[k] = v
			outgoingMD[k] = v
		}
	}
	ctx = metadata.NewIncomingContext(ctx, incomingMD)
	ctx = metadata.NewOutgoingContext(ctx, outgoingMD)
	return ctx
}

func ContextWithClientMetadata(ctx context.Context, clientMetadata *ClientMetadata) context.Context {
	return contextWithMD(ctx, clientMetadata.ToGRPCMD())
}

func ClientMetadataFromContext(ctx context.Context) (*ClientMetadata, error) {
	clientMetadata := &ClientMetadata{}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("failed to get metadata from context")
	}
	vals, ok := md[clientMetadataMetaKey]
	if !ok {
		return nil, fmt.Errorf("failed to get %s from metadata", clientMetadataMetaKey)
	}
	if len(vals) != 1 {
		return nil, fmt.Errorf("expected exactly one %s value, got %d", clientMetadataMetaKey, len(vals))
	}
	if err := json.Unmarshal([]byte(vals[0]), clientMetadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s: %v", clientMetadataMetaKey, err)
	}
	return clientMetadata, nil
}

func OptionalClientMetadataFromContext(ctx context.Context) (*ClientMetadata, bool) {
	clientMetadata, err := ClientMetadataFromContext(ctx)
	if err != nil {
		// TODO: should check actual err types
		return nil, false
	}
	return clientMetadata, true
}
