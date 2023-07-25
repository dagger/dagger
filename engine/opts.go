package engine

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/dagger/dagger/core/pipeline"
	"google.golang.org/grpc/metadata"
)

type ClientMetadata struct {
	// ClientID is unique to every session created by every client
	ClientID string
	// RouterID is the id of the router that a client and any of its nested environment clients
	// connect to
	RouterID string
	// ClientHostname is the hostname of the client that made the request. It's used opportunisticly
	// as a best-effort, semi-stable identifier for the client across multiple sessions, which can
	// be useful for debugging and for minimizing occurences of both excessive cache misses and
	// excessive cache matches.
	ClientHostname string
	// (Optional) Pipeline labels for e.g. vcs info like branch, commit, etc.
	Labels []pipeline.Label
	// ParentClientIDs is a list of session ids that are parents of the current session. The first
	// element is the direct parent, the second element is the parent of the parent, and so on.
	ParentClientIDs []string
}

// ClientIDs returns the ClientID followed by ParentClientIDs.
func (m ClientMetadata) ClientIDs() []string {
	return append([]string{m.ClientID}, m.ParentClientIDs...)
}

func (m ClientMetadata) ToGRPCMD() metadata.MD {
	md := metadata.Pairs(
		ClientIDMetaKey, m.ClientID,
		RouterIDMetaKey, m.RouterID,
		ClientHostnameMetaKey, m.ClientHostname,
		ParentClientIDsMetaKey, strings.Join(m.ParentClientIDs, " "),
	)
	var labelStrings []string
	for _, label := range m.Labels {
		labelStrings = append(labelStrings, fmt.Sprintf("%s=%s", label.Name, label.Value))
	}
	if len(labelStrings) > 0 {
		md[ClientLabelsMetaKey] = labelStrings
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

	if len(md[ClientIDMetaKey]) != 1 {
		return nil, fmt.Errorf("failed to get %s from metadata", ClientIDMetaKey)
	}
	clientMetadata.ClientID = md[ClientIDMetaKey][0]

	if len(md[RouterIDMetaKey]) != 1 {
		return nil, fmt.Errorf("failed to get %s from metadata", RouterIDMetaKey)
	}
	clientMetadata.RouterID = md[RouterIDMetaKey][0]

	if len(md[ClientHostnameMetaKey]) != 1 {
		return nil, fmt.Errorf("failed to get %s from metadata", ClientHostnameMetaKey)
	}
	clientMetadata.ClientHostname = md[ClientHostnameMetaKey][0]

	for _, kv := range md[ClientLabelsMetaKey] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("failed to parse label string %s", kv)
		}
		clientMetadata.Labels = append(clientMetadata.Labels, pipeline.Label{Name: k, Value: v})
	}

	if len(md[ParentClientIDsMetaKey]) != 1 {
		return nil, fmt.Errorf("failed to get %s from metadata", ParentClientIDsMetaKey)
	}
	clientMetadata.ParentClientIDs = strings.Fields(md[ParentClientIDsMetaKey][0])

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

		if len(md[ClientHostnameMetaKey]) != 1 {
			return nil, fmt.Errorf("failed to get %s from metadata", ClientHostnameMetaKey)
		}
		opts.ClientHostname = md[ClientHostnameMetaKey][0]

		if len(md[RouterIDMetaKey]) != 1 {
			return nil, fmt.Errorf("failed to get %s from metadata", RouterIDMetaKey)
		}
		opts.RouterID = md[RouterIDMetaKey][0]

		opts.ParentClientIDs = md[ParentClientIDsMetaKey]
		log.Println("!!! GOT PARENT CLIENT IDS", opts.RouterID, opts.ClientID, opts.ParentClientIDs)
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
