package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"unicode"

	controlapi "github.com/moby/buildkit/api/services/control"
	"google.golang.org/grpc/metadata"
)

const (
	EngineVersionMetaKey = "X-Dagger-Engine"

	ClientMetadataMetaKey  = "X-Dagger-Client-Metadata"
	localImportOptsMetaKey = "X-Dagger-Local-Import-Opts"
	localExportOptsMetaKey = "X-Dagger-Local-Export-Opts"

	// local dir import (set by buildkit, can't change)
	localDirImportDirNameMetaKey         = "dir-name"
	localDirImportIncludePatternsMetaKey = "include-patterns"
	localDirImportExcludePatternsMetaKey = "exclude-patterns"
	localDirImportFollowPathsMetaKey     = "followpaths"
)

type ClientMetadata struct {
	// ClientID is unique to each client, randomly generated each time a client initializes.
	// It's also used as the *buildkit* session ID (as opposed to the dagger session ID), which
	// is created for each client.
	ClientID string `json:"client_id"`

	// ClientSecretToken is a secret token that is unique to every client.
	// Every request w/ that client ID must also include the same token.
	ClientSecretToken string `json:"client_secret_token"`

	// SessionID is the id of the dagger session that a client and any of its nested
	// module clients connect to
	SessionID string `json:"session_id"`

	// ClientHostname is the hostname of the client that made the request. It's
	// used opportunistically as a best-effort, semi-stable identifier for the
	// client across multiple sessions, which can be useful for debugging and for
	// minimizing occurrences of both excessive cache misses and excessive cache
	// matches.
	ClientHostname string `json:"client_hostname"`

	// ClientVersion is the version string of the client that make the request.
	ClientVersion string `json:"client_version"`

	// (Optional) Pipeline labels for e.g. vcs info like branch, commit, etc.
	Labels map[string]string `json:"labels"`

	// Import configuration for Buildkit's remote cache
	UpstreamCacheImportConfig []*controlapi.CacheOptionsEntry

	// Export configuration for Buildkit's remote cache
	UpstreamCacheExportConfig []*controlapi.CacheOptionsEntry

	// Dagger Cloud Token
	CloudToken string

	// Disable analytics
	DoNotTrack bool
}

type clientMetadataCtxKey struct{}

func ContextWithClientMetadata(ctx context.Context, clientMetadata *ClientMetadata) context.Context {
	return context.WithValue(ctx, clientMetadataCtxKey{}, clientMetadata)
}

func ClientMetadataFromContext(ctx context.Context) (*ClientMetadata, error) {
	md, ok := ctx.Value(clientMetadataCtxKey{}).(*ClientMetadata)
	if !ok {
		return nil, fmt.Errorf("failed to get client metadata from context")
	}

	return md, nil
}

func ClientMetadataFromHTTPHeaders(h http.Header) (*ClientMetadata, error) {
	bs, err := base64.StdEncoding.DecodeString(h.Get(ClientMetadataMetaKey))
	if err != nil {
		return nil, fmt.Errorf("failed to base64-decode %s: %w", ClientMetadataMetaKey, err)
	}

	m := &ClientMetadata{}
	if err := json.Unmarshal(bs, m); err != nil {
		return nil, fmt.Errorf("failed to JSON-unmarshal %s: %w", ClientMetadataMetaKey, err)
	}

	if m.ClientVersion == "" {
		// fallback for old clients that don't send a client version!
		m.ClientVersion = m.Labels["dagger.io/client.version"]
	}

	return m, nil
}

func (m ClientMetadata) AppendToHTTPHeaders(h http.Header) http.Header {
	h = h.Clone()

	bs, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	h.Set(ClientMetadataMetaKey, base64.StdEncoding.EncodeToString(bs))

	return h
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
	o.Path = filepath.ToSlash(o.Path)
	md := encodeMeta(localImportOptsMetaKey, o)
	md[localDirImportDirNameMetaKey] = []string{o.Path}
	md[localDirImportIncludePatternsMetaKey] = o.IncludePatterns
	md[localDirImportExcludePatternsMetaKey] = o.ExcludePatterns
	md[localDirImportFollowPathsMetaKey] = o.FollowPaths
	return encodeOpts(md)
}

func (o *LocalImportOpts) FromGRPCMD(md metadata.MD) error {
	if v := md.Get(localImportOptsMetaKey); v != nil {
		err := decodeMeta(md, localImportOptsMetaKey, o)
		if err != nil {
			return err
		}
	} else {
		// otherwise, this is coming from buildkit directly
		dirNameVals := md.Get(localDirImportDirNameMetaKey)
		if len(dirNameVals) != 1 {
			return fmt.Errorf("expected exactly one %s, got %d", localDirImportDirNameMetaKey, len(dirNameVals))
		}
		o.Path = dirNameVals[0]
		o.IncludePatterns = md[localDirImportIncludePatternsMetaKey]
		o.ExcludePatterns = md[localDirImportExcludePatternsMetaKey]
		o.FollowPaths = md[localDirImportFollowPathsMetaKey]
	}
	o.Path = filepath.FromSlash(o.Path)
	return nil
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
	if err := opts.FromGRPCMD(md); err != nil {
		return nil, err
	}
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
	o.Path = filepath.ToSlash(o.Path)
	return encodeMeta(localExportOptsMetaKey, o)
}

func (o *LocalExportOpts) FromGRPCMD(md metadata.MD) error {
	if err := decodeMeta(md, localExportOptsMetaKey, o); err != nil {
		return err
	}
	o.Path = filepath.FromSlash(o.Path)
	return nil
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
	if err := opts.FromGRPCMD(md); err != nil {
		return nil, err
	}
	return opts, nil
}

func encodeMeta(key string, v interface{}) metadata.MD {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return metadata.Pairs(key, base64.StdEncoding.EncodeToString(b))
}

func decodeMeta(md metadata.MD, key string, dest interface{}) error {
	vals := md.Get(key)
	if len(vals) != 1 {
		return fmt.Errorf("expected exactly one %s value, got %d", key, len(vals))
	}
	jsonPayload, err := base64.StdEncoding.DecodeString(vals[0])
	if err != nil {
		return fmt.Errorf("failed to base64-decode %s: %w", key, err)
	}
	if err := json.Unmarshal(jsonPayload, dest); err != nil {
		return fmt.Errorf("failed to JSON-unmarshal %s: %w", key, err)
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
