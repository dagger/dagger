package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"unicode"

	"github.com/dagger/dagger/core/pipeline"
	controlapi "github.com/moby/buildkit/api/services/control"
	"github.com/opencontainers/go-digest"
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
	// module clients connect to
	ServerID string `json:"server_id"`

	// If RegisterClient is true, then a call to Session will initialize the
	// server if it hasn't already been initialized and register the session's
	// attachables with it either way. If false, then the session conn will be
	// forwarded to the server
	RegisterClient bool `json:"register_client"`

	// ClientHostname is the hostname of the client that made the request. It's
	// used opportunistically as a best-effort, semi-stable identifier for the
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

	// If this client is for a module function, this digest will be set in the
	// grpc context metadata for any api requests back to the engine. It's used by the API
	// server to determine which schema to serve and other module context metadata.
	ModuleCallerDigest digest.Digest `json:"module_caller_digest"`

	// Import configuration for Buildkit's remote cache
	UpstreamCacheImportConfig []*controlapi.CacheOptionsEntry

	// Export configuration for Buildkit's remote cache
	UpstreamCacheExportConfig []*controlapi.CacheOptionsEntry

	// Dagger Cloud Token
	CloudToken string

	// Disable analytics
	DoNotTrack bool
}

// ClientIDs returns the ClientID followed by ParentClientIDs.
func (m ClientMetadata) ClientIDs() []string {
	return append([]string{m.ClientID}, m.ParentClientIDs...)
}

func (m ClientMetadata) ToGRPCMD() metadata.MD {
	return encodeMeta(clientMetadataMetaKey, m)
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
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("failed to get metadata from context")
	}
	clientMetadata := &ClientMetadata{}
	if err := decodeMeta(md, clientMetadataMetaKey, clientMetadata); err != nil {
		return nil, err
	}
	return clientMetadata, nil
}

type LocalImportOpts struct {
	Path               string   `json:"path"`
	IncludePatterns    []string `json:"include_patterns"`
	ExcludePatterns    []string `json:"exclude_patterns"`
	FollowPaths        []string `json:"follow_paths"`
	ReadSingleFileOnly bool     `json:"read_single_file_only"`
	MaxFileSize        int64    `json:"max_file_size"`
	StatPathOnly       bool     `json:"stat_path_only"`
	StatReturnAbsPath  bool     `json:"stat_return_abs_path"`
}

func (o LocalImportOpts) ToGRPCMD() metadata.MD {
	// set both the dagger metadata and the ones used by buildkit
	md := encodeMeta(localImportOptsMetaKey, o)
	md[localDirImportDirNameMetaKey] = []string{o.Path}
	md[localDirImportIncludePatternsMetaKey] = o.IncludePatterns
	md[localDirImportExcludePatternsMetaKey] = o.ExcludePatterns
	md[localDirImportFollowPathsMetaKey] = o.FollowPaths
	return encodeOpts(md)
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
	md := decodeOpts(metadata.Join(incomingMD, outgoingMD))

	opts := &LocalImportOpts{}
	_, ok := md[localImportOptsMetaKey]
	if ok {
		if err := decodeMeta(md, localImportOptsMetaKey, opts); err != nil {
			return nil, err
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
	Path               string      `json:"path"`
	IsFileStream       bool        `json:"is_file_stream"`
	FileOriginalName   string      `json:"file_original_name"`
	AllowParentDirPath bool        `json:"allow_parent_dir_path"`
	FileMode           os.FileMode `json:"file_mode"`
	// whether to just merge in contents of a directory to the target on the host
	// or to replace the target entirely such that it matches the source directory,
	// which includes deleting any files that are not in the source directory
	Merge bool
}

func (o LocalExportOpts) ToGRPCMD() metadata.MD {
	return encodeMeta(localExportOptsMetaKey, o)
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
	if err := decodeMeta(md, localExportOptsMetaKey, opts); err != nil {
		return nil, err
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

func encodeMeta(key string, v interface{}) metadata.MD {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return metadata.Pairs(key, base64.StdEncoding.EncodeToString(b))
}

func decodeMeta(md metadata.MD, key string, dest interface{}) error {
	vals, ok := md[key]
	if !ok {
		return fmt.Errorf("failed to get %s from metadata", key)
	}
	if len(vals) != 1 {
		return fmt.Errorf("expected exactly one %s value, got %d", key, len(vals))
	}
	jsonPayload, err := base64.StdEncoding.DecodeString(vals[0])
	if err != nil {
		return fmt.Errorf("failed to base64-decode %s: %v", key, err)
	}
	if err := json.Unmarshal(jsonPayload, dest); err != nil {
		return fmt.Errorf("failed to JSON-unmarshal %s: %v", key, err)
	}
	return nil
}

// encodeOpts comes from buildkit session/filesync/filesync.go
func encodeOpts(opts map[string][]string) map[string][]string {
	md := make(map[string][]string, len(opts))
	for k, v := range opts {
		out, encoded := encodeStringForHeader(v)
		md[k] = out
		if encoded {
			md[k+"-encoded"] = []string{"1"}
		}
	}
	return md
}

// decodeOpts comes from buildkit session/filesync/filesync.go
func decodeOpts(opts map[string][]string) map[string][]string {
	md := make(map[string][]string, len(opts))
	for k, v := range opts {
		out := make([]string, len(v))
		var isEncoded bool
		if v, ok := opts[k+"-encoded"]; ok && len(v) > 0 {
			if b, _ := strconv.ParseBool(v[0]); b {
				isEncoded = true
			}
		}
		if isEncoded {
			for i, s := range v {
				out[i], _ = url.QueryUnescape(s)
			}
		} else {
			copy(out, v)
		}
		md[k] = out
	}
	return md
}

// encodeStringForHeader encodes a string value so it can be used in grpc header. This encoding
// is backwards compatible and avoids encoding ASCII characters.
//
// encodeStringForHeader comes from buildkit session/filesync/filesync.go
func encodeStringForHeader(inputs []string) ([]string, bool) {
	var encode bool
loop:
	for _, input := range inputs {
		for _, runeVal := range input {
			// Only encode non-ASCII characters, and characters that have special
			// meaning during decoding.
			if runeVal > unicode.MaxASCII {
				encode = true
				break loop
			}
		}
	}
	if !encode {
		return inputs, false
	}
	for i, input := range inputs {
		inputs[i] = url.QueryEscape(input)
	}
	return inputs, true
}
