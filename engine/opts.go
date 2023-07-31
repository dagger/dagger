package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core/pipeline"
	controlapi "github.com/moby/buildkit/api/services/control"
	"google.golang.org/grpc/metadata"
)

const (
	EngineVersionMetaKey = "x-dagger-engine"

	clientMetadataMetaKey  = "x-dagger-client-metadata"
	localImportOptsMetaKey = "x-dagger-local-import-opts"
	localExportOptsMetaKey = "x-dagger-local-export-opts"

	// local dir import (set by buildkit, can't change)
	localDirImportDirNameMetaKey         = "dir-name"
	localDirImportIncludePatternsMetaKey = "include-patterns"
	localDirImportExcludePatternsMetaKey = "exclude-patterns"
	localDirImportFollowPathsMetaKey     = "followpaths"
)

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

type LocalImportOpts struct {
	OwnerClientID      string   `json:"owner_client_id"`
	Path               string   `json:"path"`
	IncludePatterns    []string `json:"include_patterns"`
	ExcludePatterns    []string `json:"exclude_patterns"`
	FollowPaths        []string `json:"follow_paths"`
	ReadSingleFileOnly bool     `json:"read_single_file_only"`
}

func (o LocalImportOpts) ToGRPCMD() metadata.MD {
	b, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	// set both the dagger metadata and the ones used by buildkit
	md := metadata.Pairs(
		localImportOptsMetaKey, string(b),
		localDirImportDirNameMetaKey, o.Path,
	)
	md[localDirImportIncludePatternsMetaKey] = o.IncludePatterns
	md[localDirImportExcludePatternsMetaKey] = o.ExcludePatterns
	md[localDirImportFollowPathsMetaKey] = o.FollowPaths
	return md
}

func (o LocalImportOpts) AppendToOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = make(metadata.MD)
	}
	for k, v := range o.ToGRPCMD() {
		md[k] = append(md[k], v...)
	}
	return metadata.NewOutgoingContext(ctx, md)
}

func LocalImportOptsFromContext(ctx context.Context) (*LocalImportOpts, error) {
	incomingMD, incomingOk := metadata.FromIncomingContext(ctx)
	outgoingMD, outgoingOk := metadata.FromOutgoingContext(ctx)
	if !incomingOk && !outgoingOk {
		return nil, fmt.Errorf("failed to get metadata from context")
	}
	md := metadata.Join(incomingMD, outgoingMD)

	opts := &LocalImportOpts{}
	vals, ok := md[localImportOptsMetaKey]
	if ok {
		// we have the dagger set metadata, so we can just unmarshal it
		if len(vals) != 1 {
			return nil, fmt.Errorf("expected exactly one %s value, got %d", localImportOptsMetaKey, len(vals))
		}
		if err := json.Unmarshal([]byte(vals[0]), opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal %s: %v", localImportOptsMetaKey, err)
		}
		return opts, nil
	}

	// otherwise, this is coming from buildkit directly
	dirNameVals := md[localDirImportDirNameMetaKey]
	if len(dirNameVals) != 1 {
		return nil, fmt.Errorf("expected exactly one %s, got %d", localDirImportDirNameMetaKey, len(dirNameVals))
	}
	opts.Path = dirNameVals[0]
	opts.IncludePatterns = md[localDirImportIncludePatternsMetaKey]
	opts.ExcludePatterns = md[localDirImportExcludePatternsMetaKey]
	opts.FollowPaths = md[localDirImportFollowPathsMetaKey]
	return opts, nil
}

type LocalExportOpts struct {
	DestClientID       string `json:"dest_client_id"`
	Path               string `json:"path"`
	IsFileStream       bool   `json:"is_file_stream"`
	FileOriginalName   string `json:"file_original_name"`
	AllowParentDirPath bool   `json:"allow_parent_dir_path"`
}

func (o LocalExportOpts) ToGRPCMD() metadata.MD {
	b, err := json.Marshal(o)
	if err != nil {
		panic(err)
	}
	return metadata.Pairs(localExportOptsMetaKey, string(b))
}

func (o LocalExportOpts) AppendToOutgoingContext(ctx context.Context) context.Context {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = make(metadata.MD)
	}
	for k, v := range o.ToGRPCMD() {
		md[k] = append(md[k], v...)
	}
	return metadata.NewOutgoingContext(ctx, md)
}

func LocalExportOptsFromContext(ctx context.Context) (*LocalExportOpts, error) {
	incomingMD, incomingOk := metadata.FromIncomingContext(ctx)
	outgoingMD, outgoingOk := metadata.FromOutgoingContext(ctx)
	if !incomingOk && !outgoingOk {
		return nil, fmt.Errorf("failed to get metadata from context")
	}
	md := metadata.Join(incomingMD, outgoingMD)

	opts := &LocalExportOpts{}
	vals, ok := md[localExportOptsMetaKey]
	if !ok {
		return nil, fmt.Errorf("failed to get %s from metadata", localExportOptsMetaKey)
	}
	if len(vals) != 1 {
		return nil, fmt.Errorf("expected exactly one %s value, got %d", localExportOptsMetaKey, len(vals))
	}
	if err := json.Unmarshal([]byte(vals[0]), opts); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s: %v", localExportOptsMetaKey, err)
	}
	return opts, nil
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
