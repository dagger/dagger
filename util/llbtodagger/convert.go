package llbtodagger

import (
	"fmt"
	"path"
	"strings"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	srctypes "github.com/dagger/dagger/internal/buildkit/source/types"
)

// DefinitionToID converts an LLB definition and image config metadata to a
// Dagger Container ID.
//
// The conversion is strict and fail-fast: if any part of the definition cannot
// be represented faithfully as Dagger API calls, an error is returned.
func DefinitionToID(def *pb.Definition, img *dockerspec.DockerOCIImage) (*call.ID, error) {
	return definitionToID(def, img)
}

func definitionToID(def *pb.Definition, img *dockerspec.DockerOCIImage) (*call.ID, error) {
	if def == nil || len(def.Def) == 0 {
		return applyDockerImageConfig(scratchContainerID(), img)
	}

	dag, err := buildkit.DefToDAG(def)
	if err != nil {
		return nil, fmt.Errorf("llbtodagger: parse definition: %w", err)
	}
	if dag == nil {
		return applyDockerImageConfig(scratchContainerID(), img)
	}

	for dag != nil && dag.Op != nil && dag.Op.Op == nil {
		switch len(dag.Inputs) {
		case 0:
			return applyDockerImageConfig(scratchContainerID(), img)
		case 1:
			dag = dag.Inputs[0]
		default:
			return nil, fmt.Errorf("llbtodagger: unsupported synthetic root with %d inputs", len(dag.Inputs))
		}
	}

	conv := converter{memo: map[*buildkit.OpDAG]*call.ID{}}
	id, err := conv.convertOp(dag)
	if err != nil {
		return nil, err
	}
	ctrID, err := ensureContainerResult(id)
	if err != nil {
		return nil, err
	}
	return applyDockerImageConfig(ctrID, img)
}

type converter struct {
	memo map[*buildkit.OpDAG]*call.ID
}

func (c *converter) convertOp(dag *buildkit.OpDAG) (*call.ID, error) {
	if dag == nil {
		return scratchDirectoryID(), nil
	}

	if id, ok := c.memo[dag]; ok {
		return id, nil
	}

	var (
		id  *call.ID
		err error
	)

	switch {
	case isBlob(dag):
		err = unsupported(opDigest(dag), "source(blob)", "blob:// source is explicitly unsupported")
	case dag.GetBuild() != nil:
		err = unsupported(opDigest(dag), "build", "BuildOp is explicitly unsupported")
	case hasExec(dag):
		id, err = c.convertExec(mustExec(dag))
	case hasFile(dag):
		id, err = c.convertFile(mustFile(dag))
	case hasMerge(dag):
		id, err = c.convertMerge(mustMerge(dag))
	case hasDiff(dag):
		id, err = c.convertDiff(mustDiff(dag))
	case hasImage(dag):
		id, err = c.convertImageSource(mustImage(dag))
	case hasGit(dag):
		id, err = c.convertGitSource(mustGit(dag))
	case hasLocal(dag):
		id, err = c.convertLocalSource(mustLocal(dag))
	case hasHTTP(dag):
		id, err = c.convertHTTPSource(mustHTTP(dag))
	case hasOCI(dag):
		id, err = c.convertOCISource(mustOCI(dag))
	case dag.GetSource() != nil:
		err = unsupported(opDigest(dag), "source", "unsupported source scheme")
	default:
		err = unsupported(opDigest(dag), "unknown", "unsupported op type")
	}

	if err != nil {
		return nil, err
	}
	c.memo[dag] = id
	return id, nil
}

func opDigest(dag *buildkit.OpDAG) digest.Digest {
	if dag == nil || dag.OpDigest == nil {
		return ""
	}
	return *dag.OpDigest
}

func appendCall(base *call.ID, ret *ast.Type, field string, args ...*call.Argument) *call.ID {
	opts := []call.IDOpt{}
	if len(args) > 0 {
		opts = append(opts, call.WithArgs(args...))
	}
	return base.Append(ret, field, opts...)
}

func scratchDirectoryID() *call.ID {
	return appendCall(call.New(), directoryType(), "directory")
}

func scratchContainerID() *call.ID {
	return appendCall(call.New(), containerType(), "container")
}

func ensureContainerResult(id *call.ID) (*call.ID, error) {
	if id == nil {
		return scratchContainerID(), nil
	}

	switch id.Type().NamedType() {
	case containerType().NamedType:
		return id, nil
	case directoryType().NamedType:
		return appendCall(scratchContainerID(), containerType(), "withRootfs", argID("directory", id)), nil
	default:
		return nil, fmt.Errorf("llbtodagger: top-level result type %q cannot be converted to Container", id.Type().NamedType())
	}
}

func asDirectoryID(opDigest digest.Digest, opType string, id *call.ID) (*call.ID, error) {
	if id == nil {
		return scratchDirectoryID(), nil
	}

	switch id.Type().NamedType() {
	case directoryType().NamedType:
		return id, nil
	case containerType().NamedType:
		return appendCall(id, directoryType(), "rootfs"), nil
	default:
		return nil, unsupported(opDigest, opType, fmt.Sprintf("input type %q is not Directory/Container", id.Type().NamedType()))
	}
}

func queryContainerID(platform *pb.Platform) (*call.ID, error) {
	args := []*call.Argument{}
	if platform != nil {
		platformStr, err := platformToLiteral(platform)
		if err != nil {
			return nil, err
		}
		args = append(args, argString("platform", platformStr))
	}
	return appendCall(call.New(), containerType(), "container", args...), nil
}

func platformToLiteral(platform *pb.Platform) (string, error) {
	if platform == nil {
		return "", nil
	}
	if platform.OS == "" || platform.Architecture == "" {
		return "", fmt.Errorf("llbtodagger: invalid platform: missing OS or architecture")
	}
	if platform.OSVersion != "" || len(platform.OSFeatures) > 0 {
		return "", fmt.Errorf("llbtodagger: unsupported platform fields osVersion/osFeatures")
	}

	parts := []string{platform.OS, platform.Architecture}
	if platform.Variant != "" {
		parts = append(parts, platform.Variant)
	}
	return strings.Join(parts, "/"), nil
}

func mountSharingEnum(sh pb.CacheSharingOpt) (string, error) {
	switch sh {
	case pb.CacheSharingOpt_SHARED:
		return string(core.CacheSharingModeShared), nil
	case pb.CacheSharingOpt_PRIVATE:
		return string(core.CacheSharingModePrivate), nil
	case pb.CacheSharingOpt_LOCKED:
		return string(core.CacheSharingModeLocked), nil
	default:
		return "", fmt.Errorf("llbtodagger: unsupported cache sharing mode %v", sh)
	}
}

func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func hasExec(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsExec()
	return ok
}

func mustExec(dag *buildkit.OpDAG) *buildkit.ExecOp {
	exec, ok := dag.AsExec()
	if !ok {
		panic("unreachable")
	}
	return exec
}

func hasFile(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsFile()
	return ok
}

func mustFile(dag *buildkit.OpDAG) *buildkit.FileOp {
	file, ok := dag.AsFile()
	if !ok {
		panic("unreachable")
	}
	return file
}

func hasMerge(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsMerge()
	return ok
}

func mustMerge(dag *buildkit.OpDAG) *buildkit.MergeOp {
	op, ok := dag.AsMerge()
	if !ok {
		panic("unreachable")
	}
	return op
}

func hasDiff(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsDiff()
	return ok
}

func mustDiff(dag *buildkit.OpDAG) *buildkit.DiffOp {
	op, ok := dag.AsDiff()
	if !ok {
		panic("unreachable")
	}
	return op
}

func hasImage(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsImage()
	return ok
}

func mustImage(dag *buildkit.OpDAG) *buildkit.ImageOp {
	op, ok := dag.AsImage()
	if !ok {
		panic("unreachable")
	}
	return op
}

func hasGit(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsGit()
	return ok
}

func mustGit(dag *buildkit.OpDAG) *buildkit.GitOp {
	op, ok := dag.AsGit()
	if !ok {
		panic("unreachable")
	}
	return op
}

func hasLocal(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsLocal()
	return ok
}

func mustLocal(dag *buildkit.OpDAG) *buildkit.LocalOp {
	op, ok := dag.AsLocal()
	if !ok {
		panic("unreachable")
	}
	return op
}

func hasHTTP(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsHTTP()
	return ok
}

func mustHTTP(dag *buildkit.OpDAG) *buildkit.HTTPOp {
	op, ok := dag.AsHTTP()
	if !ok {
		panic("unreachable")
	}
	return op
}

func hasOCI(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsOCI()
	return ok
}

func mustOCI(dag *buildkit.OpDAG) *buildkit.OCIOp {
	op, ok := dag.AsOCI()
	if !ok {
		panic("unreachable")
	}
	return op
}

func isBlob(dag *buildkit.OpDAG) bool {
	_, ok := dag.AsBlob()
	return ok
}

func nilBlob() *buildkit.BlobOp {
	return nil
}

func containerType() *ast.Type   { return (&core.Container{}).Type() }
func directoryType() *ast.Type   { return (&core.Directory{}).Type() }
func fileType() *ast.Type        { return (&core.File{}).Type() }
func hostType() *ast.Type        { return (&core.Host{}).Type() }
func gitRepoType() *ast.Type     { return (&core.GitRepository{}).Type() }
func gitRefType() *ast.Type      { return (&core.GitRef{}).Type() }
func cacheVolumeType() *ast.Type { return (&core.CacheVolume{}).Type() }

func argString(name, val string) *call.Argument {
	return call.NewArgument(name, call.NewLiteralString(val), false)
}

func argInt(name string, val int64) *call.Argument {
	return call.NewArgument(name, call.NewLiteralInt(val), false)
}

func argBool(name string, val bool) *call.Argument {
	return call.NewArgument(name, call.NewLiteralBool(val), false)
}

func argEnum(name, val string) *call.Argument {
	return call.NewArgument(name, call.NewLiteralEnum(val), false)
}

func argID(name string, id *call.ID) *call.Argument {
	return call.NewArgument(name, call.NewLiteralID(id), false)
}

func argStringList(name string, vals []string) *call.Argument {
	lits := make([]call.Literal, 0, len(vals))
	for _, v := range vals {
		lits = append(lits, call.NewLiteralString(v))
	}
	return call.NewArgument(name, call.NewLiteralList(lits...), false)
}

func sourceIdentifierWithoutScheme(identifier, scheme string) (string, error) {
	prefix := scheme + "://"
	if !strings.HasPrefix(identifier, prefix) {
		return "", fmt.Errorf("llbtodagger: invalid source identifier %q for scheme %q", identifier, scheme)
	}
	value := strings.TrimPrefix(identifier, prefix)
	if value == "" {
		return "", fmt.Errorf("llbtodagger: empty source identifier for scheme %q", scheme)
	}
	return value, nil
}

func isSupportedSourceScheme(identifier string) bool {
	for _, scheme := range []string{
		srctypes.DockerImageScheme,
		srctypes.GitScheme,
		srctypes.LocalScheme,
		srctypes.HTTPScheme,
		srctypes.HTTPSScheme,
		srctypes.OCIScheme,
	} {
		if strings.HasPrefix(identifier, scheme+"://") {
			return true
		}
	}
	return false
}
