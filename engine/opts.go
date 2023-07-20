package engine

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"

	"google.golang.org/grpc/metadata"
)

type ClientMetadata struct {
	ClientID       string
	RouterID       string
	ParentSessions []string
}

func (m ClientMetadata) ToMD() metadata.MD {
	return metadata.Pairs(
		ClientIDMetaKey, m.ClientID,
		RouterIDMetaKey, m.RouterID,
		ParentSessionsMetaKey, strings.Join(m.ParentSessions, " "),
	)
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
	return contextWithMD(ctx, clientMetadata.ToMD())
}

func ClientMetadataFromContext(ctx context.Context) (*ClientMetadata, error) {
	clientMetadata := &ClientMetadata{}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("failed to get metadata from context")
	}

	if len(md[ClientIDMetaKey]) != 1 {
		// TODO:
		// return nil, fmt.Errorf("failed to get %s from metadata", ClientIDMetaKey)
		return nil, fmt.Errorf("failed to get %s from metadata: %s", ClientIDMetaKey, string(debug.Stack()))
	}
	clientMetadata.ClientID = md[ClientIDMetaKey][0]

	if len(md[RouterIDMetaKey]) != 1 {
		return nil, fmt.Errorf("failed to get %s from metadata", RouterIDMetaKey)
	}
	clientMetadata.RouterID = md[RouterIDMetaKey][0]

	if len(md[ParentSessionsMetaKey]) != 1 {
		return nil, fmt.Errorf("failed to get %s from metadata", ParentSessionsMetaKey)
	}
	clientMetadata.ParentSessions = strings.Fields(md[ParentSessionsMetaKey][0])

	return clientMetadata, nil
}

// opts when calling the Session method in Server (part of the controller grpc API)
type SessionAPIOpts struct {
	*ClientMetadata
	// If true, this session call is for buildkit attachables rather than the default
	// of connecting the the Dagger GraphQL API
	BuildkitAttachable bool
}

func SessionAPIOptsFromContext(ctx context.Context) (*SessionAPIOpts, error) {
	// first check to see if it's a header set by buildkit's session.Run method, in which
	// case it's an attempt to connect session attachables rather than connect to dagger's
	// graphql api
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("failed to get metadata from context")
	}
	if len(md[SessionIDMetaKey]) > 0 {
		opts := &SessionAPIOpts{
			ClientMetadata: &ClientMetadata{
				// client id is the session id
				ClientID: md[SessionIDMetaKey][0],
			},
			BuildkitAttachable: true,
		}
		if len(md[SessionNameMetaKey]) != 1 {
			return nil, fmt.Errorf("failed to get %s from metadata", SessionNameMetaKey)
		}
		// router id is the session name (uninterpreted by buildkit)
		opts.RouterID = md[SessionNameMetaKey][0]
		return opts, nil
	}

	clientMetadata, err := ClientMetadataFromContext(ctx)
	if err != nil {
		// TODO:
		// return nil, err
		return nil, fmt.Errorf("failed to get client metadata from context: %w: %+v", err, md)
	}
	return &SessionAPIOpts{ClientMetadata: clientMetadata}, nil
}
