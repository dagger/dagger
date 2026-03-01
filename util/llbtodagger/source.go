package llbtodagger

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	srctypes "github.com/dagger/dagger/internal/buildkit/source/types"
)

const (
	// DockerfileMainContextSentinelLocalName is the deterministic local source
	// name used when dockerBuild injects a synthetic Dockerfile2LLB MainContext.
	DockerfileMainContextSentinelLocalName = "__dagger_dockerbuild_main_context_sentinel__"
	// DockerfileMainContextSentinelSharedKeyHint pairs with
	// DockerfileMainContextSentinelLocalName to avoid accidental matches.
	DockerfileMainContextSentinelSharedKeyHint = "__dagger_dockerbuild_main_context_sentinel__"
)

// DockerfileMainContextSentinelState returns the synthetic MainContext state
// used by dockerBuild before llbtodagger rebinding.
func DockerfileMainContextSentinelState() llb.State {
	return llb.Local(
		DockerfileMainContextSentinelLocalName,
		llb.SharedKeyHint(DockerfileMainContextSentinelSharedKeyHint),
	)
}

func (c *converter) convertImageSource(op *buildkit.ImageOp) (*call.ID, error) {
	ref, err := sourceIdentifierWithoutScheme(op.SourceOp.Identifier, srctypes.DockerImageScheme)
	if err != nil {
		return nil, err
	}

	ctrID, err := queryContainerID(op.Platform)
	if err != nil {
		return nil, fmt.Errorf("llbtodagger: image source %s: %w", opDigest(op.OpDAG), err)
	}

	return appendCall(ctrID, containerType(), "from", argString("address", ref)), nil
}

func (c *converter) convertGitSource(op *buildkit.GitOp) (*call.ID, error) {
	gitID, err := sourceIdentifierWithoutScheme(op.SourceOp.Identifier, srctypes.GitScheme)
	if err != nil {
		return nil, err
	}
	remoteID, refName, _ := strings.Cut(gitID, "#")

	attrs := op.SourceOp.Attrs
	fullURL := attrs[pb.AttrFullRemoteURL]
	if fullURL == "" {
		if remoteID == "" {
			return nil, unsupported(opDigest(op.OpDAG), "source(git)", "missing remote URL")
		}
		fullURL = "https://" + strings.TrimPrefix(remoteID, "/")
	}

	keepGitDir := attrs[pb.AttrKeepGitDir] == "true"

	for k, v := range attrs {
		switch k {
		case pb.AttrKeepGitDir, pb.AttrFullRemoteURL:
			// handled
		case pb.AttrAuthTokenSecret:
			if v != "" && v != llb.GitAuthTokenKey {
				return nil, unsupported(opDigest(op.OpDAG), "source(git)", "custom auth token secret is unsupported")
			}
		case pb.AttrAuthHeaderSecret:
			if v != "" && v != llb.GitAuthHeaderKey {
				return nil, unsupported(opDigest(op.OpDAG), "source(git)", "custom auth header secret is unsupported")
			}
		case pb.AttrKnownSSHHosts:
			if v != "" {
				return nil, unsupported(opDigest(op.OpDAG), "source(git)", "known SSH hosts override is unsupported")
			}
		case pb.AttrMountSSHSock:
			if v != "" && v != "default" {
				return nil, unsupported(opDigest(op.OpDAG), "source(git)", "custom ssh socket mount is unsupported")
			}
		default:
			return nil, unsupported(opDigest(op.OpDAG), "source(git)", fmt.Sprintf("unsupported git attr %q", k))
		}
	}

	gitArgs := []*call.Argument{argString("url", fullURL)}
	if keepGitDir {
		gitArgs = append(gitArgs, argBool("keepGitDir", true))
	}
	repoID := appendCall(call.New(), gitRepoType(), "git", gitArgs...)

	var refID *call.ID
	if refName == "" {
		refID = appendCall(repoID, gitRefType(), "head")
	} else {
		refID = appendCall(repoID, gitRefType(), "ref", argString("name", refName))
	}

	return appendCall(
		refID,
		directoryType(),
		"tree",
		argBool("discardGitDir", !keepGitDir),
		argInt("depth", 1),
	), nil
}

func (c *converter) convertLocalSource(op *buildkit.LocalOp) (*call.ID, error) {
	name, err := sourceIdentifierWithoutScheme(op.SourceOp.Identifier, srctypes.LocalScheme)
	if err != nil {
		return nil, err
	}

	attrs := op.SourceOp.Attrs
	if isDockerfileMainContextSentinel(name, attrs) {
		if c.mainContextDirectory == nil {
			return nil, unsupported(opDigest(op.OpDAG), "source(local)", "dockerBuild main-context sentinel requires a context directory rebinding input")
		}
		if c.mainContextDirectory.Type().NamedType() != directoryType().NamedType {
			return nil, unsupported(opDigest(op.OpDAG), "source(local)", fmt.Sprintf("dockerBuild main-context rebinding type %q is not Directory", c.mainContextDirectory.Type().NamedType()))
		}
		return c.mainContextDirectory, nil
	}

	sourcePath := attrs[pb.AttrSharedKeyHint]
	if sourcePath == "" {
		sourcePath = name
	}
	if sourcePath == "" {
		return nil, unsupported(opDigest(op.OpDAG), "source(local)", "empty local source path")
	}

	includePatterns, err := parseJSONPatternList(attrs[pb.AttrIncludePatterns])
	if err != nil {
		return nil, unsupported(opDigest(op.OpDAG), "source(local)", fmt.Sprintf("invalid include patterns: %v", err))
	}
	excludePatterns, err := parseJSONPatternList(attrs[pb.AttrExcludePatterns])
	if err != nil {
		return nil, unsupported(opDigest(op.OpDAG), "source(local)", fmt.Sprintf("invalid exclude patterns: %v", err))
	}
	followPaths, err := parseJSONPatternList(attrs[pb.AttrFollowPaths])
	if err != nil {
		return nil, unsupported(opDigest(op.OpDAG), "source(local)", fmt.Sprintf("invalid follow paths: %v", err))
	}
	if differ := attrs[pb.AttrLocalDiffer]; differ != "" && differ != pb.AttrLocalDifferMetadata {
		return nil, unsupported(opDigest(op.OpDAG), "source(local)", "unsupported local differ mode")
	}

	for k := range attrs {
		switch k {
		case pb.AttrLocalSessionID,
			pb.AttrLocalUniqueID,
			pb.AttrIncludePatterns,
			pb.AttrExcludePatterns,
			pb.AttrFollowPaths,
			pb.AttrSharedKeyHint,
			pb.AttrLocalDiffer:
			// handled/accepted
		default:
			return nil, unsupported(opDigest(op.OpDAG), "source(local)", fmt.Sprintf("unsupported local attr %q", k))
		}
	}

	hostID := appendCall(call.New(), hostType(), "host")
	args := []*call.Argument{argString("path", sourcePath)}
	if len(includePatterns) > 0 {
		args = append(args, argStringList("include", includePatterns))
	}
	if len(excludePatterns) > 0 {
		args = append(args, argStringList("exclude", excludePatterns))
	}
	if len(followPaths) > 0 {
		args = append(args, argStringList("followPaths", followPaths))
	}
	return appendCall(hostID, directoryType(), "directory", args...), nil
}

func isDockerfileMainContextSentinel(name string, attrs map[string]string) bool {
	if name != DockerfileMainContextSentinelLocalName {
		return false
	}
	return attrs[pb.AttrSharedKeyHint] == DockerfileMainContextSentinelSharedKeyHint
}

func (c *converter) convertHTTPSource(op *buildkit.HTTPOp) (*call.ID, error) {
	identifier := op.SourceOp.Identifier
	if !strings.HasPrefix(identifier, srctypes.HTTPScheme+"://") && !strings.HasPrefix(identifier, srctypes.HTTPSScheme+"://") {
		return nil, unsupported(opDigest(op.OpDAG), "source(http)", "invalid HTTP source identifier")
	}

	attrs := op.SourceOp.Attrs
	for k := range attrs {
		switch k {
		case pb.AttrHTTPChecksum, pb.AttrHTTPFilename, pb.AttrHTTPPerm, pb.AttrHTTPUID, pb.AttrHTTPGID:
			// handled below
		default:
			return nil, unsupported(opDigest(op.OpDAG), "source(http)", fmt.Sprintf("unsupported HTTP attr %q", k))
		}
	}

	if attrs[pb.AttrHTTPChecksum] != "" {
		return nil, unsupported(opDigest(op.OpDAG), "source(http)", "http checksum enforcement is unsupported")
	}
	if attrs[pb.AttrHTTPUID] != "" || attrs[pb.AttrHTTPGID] != "" {
		return nil, unsupported(opDigest(op.OpDAG), "source(http)", "http uid/gid override is unsupported")
	}

	name := attrs[pb.AttrHTTPFilename]
	if name == "" {
		parsed, err := url.Parse(identifier)
		if err != nil {
			return nil, unsupported(opDigest(op.OpDAG), "source(http)", fmt.Sprintf("invalid URL %q", identifier))
		}
		name = path.Base(parsed.Path)
		if name == "" || name == "." || name == "/" {
			name = "index"
		}
	}

	httpArgs := []*call.Argument{argString("url", identifier)}
	httpArgs = append(httpArgs, argString("name", name))

	permStr := attrs[pb.AttrHTTPPerm]
	if permStr != "" {
		perm, err := strconv.ParseInt(permStr, 0, 64)
		if err != nil {
			return nil, unsupported(opDigest(op.OpDAG), "source(http)", fmt.Sprintf("invalid permissions %q", permStr))
		}
		httpArgs = append(httpArgs, argInt("permissions", perm))
	}

	fileID := appendCall(call.New(), fileType(), "http", httpArgs...)
	dirID := scratchDirectoryID()
	return appendCall(dirID, directoryType(), "withFile", argString("path", name), argID("source", fileID)), nil
}

func (c *converter) convertOCISource(op *buildkit.OCIOp) (*call.ID, error) {
	_ = op
	return nil, unsupported(opDigest(op.OpDAG), "source(oci-layout)", "oci-layout source is not yet supported")
}

func parseJSONPatternList(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var patterns []string
	if err := json.Unmarshal([]byte(raw), &patterns); err != nil {
		return nil, err
	}
	return patterns, nil
}
