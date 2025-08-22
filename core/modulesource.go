package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	fsutiltypes "github.com/tonistiigi/fsutil/types"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/cache"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
)

type ModuleSourceKind string

var ModuleSourceKindEnum = dagql.NewEnum[ModuleSourceKind]()

var (
	ModuleSourceKindLocal = ModuleSourceKindEnum.Register("LOCAL_SOURCE")
	_                     = ModuleSourceKindEnum.AliasView("LOCAL", "LOCAL_SOURCE", enumView)
	ModuleSourceKindGit   = ModuleSourceKindEnum.Register("GIT_SOURCE")
	_                     = ModuleSourceKindEnum.AliasView("GIT", "GIT_SOURCE", enumView)
	ModuleSourceKindDir   = ModuleSourceKindEnum.Register("DIR_SOURCE")
	_                     = ModuleSourceKindEnum.AliasView("DIR", "DIR_SOURCE", enumView)
)

func (proto ModuleSourceKind) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleSourceKind",
		NonNull:   true,
	}
}

func (proto ModuleSourceKind) TypeDescription() string {
	return "The kind of module source."
}

func (proto ModuleSourceKind) Decoder() dagql.InputDecoder {
	return ModuleSourceKindEnum
}

func (proto ModuleSourceKind) ToLiteral() call.Literal {
	return ModuleSourceKindEnum.Literal(proto)
}

func (proto ModuleSourceKind) HumanString() string {
	switch proto {
	case ModuleSourceKindLocal:
		return "local"
	case ModuleSourceKindGit:
		return "git"
	case ModuleSourceKindDir:
		return "directory"
	default:
		return string(proto)
	}
}

type SDKConfig struct {
	Source string `field:"true" name:"source" doc:"Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation."`
	Config map[string]any
}

func (*SDKConfig) Type() *ast.Type {
	return &ast.Type{
		NamedType: "SDKConfig",
		NonNull:   false,
	}
}

func (*SDKConfig) TypeDescription() string {
	return "The SDK config of the module."
}

func (sdk SDKConfig) Clone() *SDKConfig {
	cp := sdk
	return &cp
}

type ModuleSource struct {
	ConfigExists           bool   `field:"true" name:"configExists" doc:"Whether an existing dagger.json for the module was found."`
	ModuleName             string `field:"true" name:"moduleName" doc:"The name of the module, including any setting via the withName API."`
	ModuleOriginalName     string `field:"true" name:"moduleOriginalName" doc:"The original name of the module as read from the module's dagger.json (or set for the first time with the withName API)."`
	EngineVersion          string `field:"true" name:"engineVersion" doc:"The engine version of the module."`
	CodegenConfig          *modules.ModuleCodegenConfig
	ModuleConfigUserFields modules.ModuleConfigUserFields

	// The SDK configuration of the module as read from the module's dagger.json or set by withSDK
	SDK *SDKConfig `field:"true" name:"sdk" doc:"The SDK configuration of the module."`
	// The implementation of the SDK with codegen and related operations. Reloaded when SDK changes.
	SDKImpl SDK

	// IncludePaths are the includes as read from the module's dagger.json
	IncludePaths []string
	// RebasedIncludePaths are the include paths with the source root subpath prepended
	RebasedIncludePaths []string

	// ConfigDependencies are the dependencies as read from the module's dagger.json
	// NOTE: this is currently not updated by withDependencies and related APIs, only Dependencies will be updated
	ConfigDependencies []*modules.ModuleConfigDependency

	// Dependencies are the loaded sources for the module's dependencies
	Dependencies    dagql.ObjectResultArray[*ModuleSource] `field:"true" name:"dependencies" doc:"The dependencies of the module source."`
	ConfigBlueprint *modules.ModuleConfigDependency
	Blueprint       dagql.ObjectResult[*ModuleSource] `field:"true" name:"blueprint" doc:"The blueprint referenced by the module source."`
	// Clients are the clients generated for the module.
	ConfigClients []*modules.ModuleConfigClient `field:"true" name:"configClients" doc:"The clients generated for the module."`

	// SourceRootSubpath is the relative path from the context dir to the dir containing the module's dagger.json
	SourceRootSubpath string `field:"true" name:"sourceRootSubpath" doc:"The path, relative to the context directory, that contains the module's dagger.json."`
	// SourceSubpath is the relative path from the context dir to the dir containing the module's source code
	SourceSubpath string

	OriginalSubpath string

	ContextDirectory dagql.ObjectResult[*Directory] `field:"true" name:"contextDirectory" doc:"The full directory loaded for the module source, including the source code as a subdirectory."`

	Digest string `field:"true" name:"digest" doc:"A content-hash of the module source. Module sources with the same digest will output the same generated context and convert into the same module instance."`

	Kind  ModuleSourceKind `field:"true" name:"kind" doc:"The kind of module source (currently local, git or dir)."`
	Local *LocalModuleSource
	Git   *GitModuleSource
}

func (src *ModuleSource) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleSource",
		NonNull:   true,
	}
}

func (src *ModuleSource) TypeDescription() string {
	return "The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc."
}

func (src ModuleSource) Clone() *ModuleSource {
	if src.CodegenConfig != nil {
		src.CodegenConfig = src.CodegenConfig.Clone()
	}

	if src.SDK != nil {
		src.SDK = src.SDK.Clone()
	}

	origIncludePaths := src.IncludePaths
	src.IncludePaths = make([]string, len(origIncludePaths))
	copy(src.IncludePaths, origIncludePaths)
	origFullIncludePaths := src.RebasedIncludePaths
	src.RebasedIncludePaths = make([]string, len(origFullIncludePaths))
	copy(src.RebasedIncludePaths, origFullIncludePaths)

	origConfigDependencies := src.ConfigDependencies
	src.ConfigDependencies = make([]*modules.ModuleConfigDependency, len(origConfigDependencies))
	copy(src.ConfigDependencies, origConfigDependencies)
	origDependencies := src.Dependencies
	src.Dependencies = make([]dagql.ObjectResult[*ModuleSource], len(origDependencies))
	copy(src.Dependencies, origDependencies)

	if src.Local != nil {
		src.Local = src.Local.Clone()
	}

	if src.Git != nil {
		src.Git = src.Git.Clone()
	}

	oriConfigClients := src.ConfigClients
	src.ConfigClients = make([]*modules.ModuleConfigClient, len(oriConfigClients))
	copy(src.ConfigClients, oriConfigClients)

	return &src
}

func (src *ModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	var pbDefs []*pb.Definition
	if src.ContextDirectory.Self() != nil {
		defs, err := src.ContextDirectory.Self().PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		pbDefs = append(pbDefs, defs...)
	}
	for _, dep := range src.Dependencies {
		if dep.Self() == nil {
			continue
		}
		defs, err := dep.Self().PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		pbDefs = append(pbDefs, defs...)
	}
	return pbDefs, nil
}

func (src *ModuleSource) Evaluate(context.Context) (*buildkit.Result, error) {
	return nil, nil
}

func (src *ModuleSource) AsString() string {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return filepath.Join(src.Local.ContextDirectoryPath, src.SourceRootSubpath)

	case ModuleSourceKindGit:
		return GitRefString(src.Git.CloneRef, src.SourceRootSubpath, src.Git.Version)

	default:
		return ""
	}
}

func GitRefString(cloneRef, sourceRootSubpath, version string) string {
	refPath := cloneRef
	subPath := filepath.Join("/", sourceRootSubpath)
	if subPath != "/" {
		refPath += subPath
	}
	if version != "" {
		refPath += "@" + version
	}
	return refPath
}

func (src *ModuleSource) Pin() string {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return ""
	case ModuleSourceKindGit:
		return src.Git.Pin
	default:
		return ""
	}
}

// we mix this into digest hashes to ensure they don't accidentally collide
// with any others
const moduleSourceHashMix = "moduleSource"

// CalcDigest calculates a content-hash of the module source. It is used during codegen; two module
// sources with the same digest will share cache for codegen-related calls.
func (src *ModuleSource) CalcDigest() digest.Digest {
	inputs := []string{
		moduleSourceHashMix,
		src.ModuleOriginalName,
		src.SourceRootSubpath,
		src.SourceSubpath,
		src.ContextDirectory.ID().Digest().String(),
	}

	if src.SDK != nil {
		inputs = append(inputs, src.SDK.Source)
	}

	inputs = append(inputs, src.IncludePaths...)

	for _, dep := range src.Dependencies {
		if dep.Self() == nil {
			continue
		}
		inputs = append(inputs, dep.Self().Digest)
	}

	for _, client := range src.ConfigClients {
		inputs = append(inputs, client.Generator, client.Directory)
	}

	return dagql.HashFrom(inputs...)
}

// LoadContext loads addition files+directories from the module source's context, including those that
// may have not been included in the original module source load.
func (src *ModuleSource) LoadContext(
	ctx context.Context,
	dag *dagql.Server,
	path string,
	include []string,
	exclude []string,
) (inst dagql.ObjectResult[*Directory], err error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get buildkit api: %w", err)
	}

	filterInputs := []dagql.NamedInput{}
	if len(include) > 0 {
		filterInputs = append(filterInputs, dagql.NamedInput{
			Name:  "include",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(include...)),
		})
	}
	if len(exclude) > 0 {
		filterInputs = append(filterInputs, dagql.NamedInput{
			Name:  "exclude",
			Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(exclude...)),
		})
	}

	switch src.Kind {
	case ModuleSourceKindLocal:
		localSourceClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get client metadata: %w", err)
		}
		localSourceCtx := engine.ContextWithClientMetadata(ctx, localSourceClientMetadata)

		// Retrieve the absolute path to the context directory (.git or dagger.json)
		// and the module root directory (dagger.json)
		ctxPath := src.Local.ContextDirectoryPath
		modPath := filepath.Join(ctxPath, src.SourceRootSubpath)

		// If path is not absolute, it's relative to the module root directory.
		// If path is absolute, it's relative to the context directory.
		if !filepath.IsAbs(path) {
			path = filepath.Join(modPath, path)
		} else {
			path = filepath.Join(ctxPath, path)
		}

		// We just check if the path is relative to the context directory,
		// if not, that means it's a path that target an outside directory
		// which is not allowed.
		relativePathToCtx, err := filepath.Rel(ctxPath, path)
		if err != nil {
			return inst, fmt.Errorf("failed to get relative path to context: %w", err)
		}

		// If the relative path is outisde of the context directory, throw an error.
		if strings.HasPrefix(relativePathToCtx, "..") {
			return inst, fmt.Errorf("path %q is outside of context directory %q, path should be relative to the context directory", path, ctxPath)
		}

		err = dag.Select(localSourceCtx, dag.Root(), &inst,
			dagql.Selector{
				Field: "host",
			},
			dagql.Selector{
				Field: "directory",
				Args: append([]dagql.NamedInput{
					{Name: "path", Value: dagql.String(path)},
					{Name: "noCache", Value: dagql.Boolean(true)},
				}, filterInputs...),
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to select directory: %w", err)
		}

	case ModuleSourceKindGit:
		slog.Debug("moduleSource.LoadContext: loading contextual directory from git", "path", path, "kind", src.Kind, "repo", src.Git.HTMLURL)

		if !filepath.IsAbs(path) {
			path = filepath.Join("/", src.SourceRootSubpath, path)
		}

		// Use the Git context directory without dagger.json includes applied.
		ctxDir := src.Git.UnfilteredContextDir

		if path != "/" {
			if err := dag.Select(ctx, ctxDir, &ctxDir,
				dagql.Selector{
					Field: "directory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(path)},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		if len(filterInputs) > 0 {
			if err := dag.Select(ctx, dag.Root(), &ctxDir,
				dagql.Selector{
					Field: "directory",
				},
				dagql.Selector{
					Field: "withDirectory",
					Args: append([]dagql.NamedInput{
						{Name: "path", Value: dagql.String("/")},
						{Name: "directory", Value: dagql.NewID[*Directory](ctxDir.ID())},
					}, filterInputs...),
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		inst = ctxDir

	case ModuleSourceKindDir:
		if !filepath.IsAbs(path) {
			path = filepath.Join("/", src.SourceRootSubpath, path)
		}

		// Use the Dir context directory.
		ctxDir := src.ContextDirectory

		if path != "/" {
			if err := dag.Select(ctx, ctxDir, &ctxDir,
				dagql.Selector{
					Field: "directory",
					Args: []dagql.NamedInput{
						{Name: "path", Value: dagql.String(path)},
					},
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		if len(filterInputs) > 0 {
			if err := dag.Select(ctx, dag.Root(), &ctxDir,
				dagql.Selector{
					Field: "directory",
				},
				dagql.Selector{
					Field: "withDirectory",
					Args: append([]dagql.NamedInput{
						{Name: "path", Value: dagql.String("/")},
						{Name: "directory", Value: dagql.NewID[*Directory](ctxDir.ID())},
					}, filterInputs...),
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select context directory subpath: %w", err)
			}
		}

		inst, err = MakeDirectoryContentHashed(ctx, bk, ctxDir)
		if err != nil {
			return inst, err
		}

	default:
		return inst, fmt.Errorf("unsupported module src kind: %q", src.Kind)
	}

	mainClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata: %w", err)
	}
	if err := query.AddClientResourcesFromID(ctx, &resource.ID{ID: *inst.ID()}, mainClientMetadata.ClientID, false); err != nil {
		return inst, fmt.Errorf("failed to add client resources from directory source: %w", err)
	}

	return inst, nil
}

type LocalModuleSource struct {
	ContextDirectoryPath string
}

func (src LocalModuleSource) Clone() *LocalModuleSource {
	return &src
}

type GitModuleSource struct {
	// The ref to clone the root of the git repo from
	CloneRef string

	// Symbolic is the CloneRef plus the SourceRootSubpath (no version)
	Symbolic string

	// The URL to the source's git repo in a web browser, at the root of the repo
	HTMLRepoURL string

	// The URL to the source's git repo in a web browser, including to the source root subpath
	HTMLURL string

	// The import path corresponding to the root of the git repo this source points to
	RepoRootPath string

	// The version of the source; may be a branch, tag, or commit hash
	Version string

	// The resolved commit hash of the source
	Commit string
	Pin    string

	// The full git repo for the module source without any include filtering
	UnfilteredContextDir dagql.ObjectResult[*Directory]
}

func (src GitModuleSource) Link(filepath string, line int, column int) (string, error) {
	parsedURL, err := url.Parse(src.HTMLRepoURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse git repo URL %q: %w", src.HTMLRepoURL, err)
	}

	switch parsedURL.Host {
	case "github.com", "gitlab.com":
		result := src.HTMLRepoURL + path.Join("/tree", src.Commit, filepath)
		if line > 0 {
			result += fmt.Sprintf("#L%d", line)
		}
		return result, nil
	case "dev.azure.com":
		result := src.HTMLRepoURL + path.Join("/commit", src.Commit)
		if filepath != "." {
			result += "?path=/" + filepath
		}
		return result, nil
	default:
		return src.HTMLRepoURL + path.Join("/src", src.Commit, filepath), nil
	}
}

func (src GitModuleSource) Clone() *GitModuleSource {
	return &src
}

func (src *GitModuleSource) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	if src.UnfilteredContextDir.Self() == nil {
		return nil, nil
	}
	return src.UnfilteredContextDir.Self().PBDefinitions(ctx)
}

type SchemeType int

const (
	NoScheme SchemeType = iota
	SchemeHTTP
	SchemeHTTPS
	SchemeSSH
	SchemeSCPLike
)

func (s SchemeType) Prefix() string {
	switch s {
	case SchemeHTTP:
		return "http://"
	case SchemeHTTPS:
		return "https://"
	case SchemeSSH:
		return "ssh://"
	default:
		return ""
	}
}

func (s SchemeType) IsSSH() bool {
	return s == SchemeSSH
}

// ResolveDepToSource given a parent module source, load a dependency of it
// from the given depSrcRef, depPin and depName.
func ResolveDepToSource(
	ctx context.Context,
	bk *buildkit.Client,
	dag *dagql.Server,
	parentSrc *ModuleSource,
	depSrcRef string,
	depPin string,
	depName string,
) (inst dagql.ObjectResult[*ModuleSource], err error) {
	// sanity checks
	if parentSrc != nil {
		if parentSrc.SourceRootSubpath == "" {
			return inst, fmt.Errorf("source root path must be set")
		}
		if parentSrc.ModuleName == "" {
			return inst, fmt.Errorf("module name must be set")
		}
	}

	parsedDepRef, err := ParseRefString(
		ctx,
		ModuleSourceStatFS{bk, parentSrc},
		depSrcRef,
		depPin,
	)
	if err != nil {
		return inst, fmt.Errorf("failed to parse dep ref string: %w", err)
	}

	switch parsedDepRef.Kind {
	case ModuleSourceKindLocal:
		if parentSrc == nil {
			// it's okay if there's no parent when the dep is git, but we can't find a local dep relative to nothing
			return inst, fmt.Errorf("local module dep source path %q must be relative to a parent module", depSrcRef)
		}

		if filepath.IsAbs(depSrcRef) {
			// they need to be relative to the parent module's source root
			return inst, fmt.Errorf("local module dep source path %q is absolute", depSrcRef)
		}

		switch parentSrc.Kind {
		case ModuleSourceKindLocal:
			// parent=local, dep=local
			// load the dep relative to the parent's source root, from the caller's filesystem
			depPath := filepath.Join(parentSrc.Local.ContextDirectoryPath, parentSrc.SourceRootSubpath, depSrcRef)
			depRelPath, err := pathutil.LexicalRelativePath(parentSrc.Local.ContextDirectoryPath, depPath)
			if err != nil {
				return inst, fmt.Errorf("failed to get relative path from context to dep: %w", err)
			}
			if !filepath.IsLocal(depRelPath) {
				return inst, fmt.Errorf("local module dep source path %q escapes context %q", depRelPath, parentSrc.Local.ContextDirectoryPath)
			}

			selectors := []dagql.Selector{{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(depPath)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			}}
			if depName != "" {
				selectors = append(selectors, dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(depName)},
					},
				})
			}
			err = dag.Select(ctx, dag.Root(), &inst, selectors...)
			if err != nil {
				if errors.Is(err, cache.ErrCacheRecursiveCall) {
					return inst, fmt.Errorf("module %q has a circular dependency on itself through dependency %q", parentSrc.ModuleName, depName)
				}
				return inst, fmt.Errorf("failed to load local dep: %w", err)
			}
			return inst, nil

		case ModuleSourceKindGit:
			// parent=git, dep=local
			// load the dep relative to the parent's source root, from the parent source's git repo
			refString := GitRefString(
				parentSrc.Git.CloneRef,
				filepath.Join(parentSrc.SourceRootSubpath, depSrcRef),
				parentSrc.Git.Version,
			)
			selectors := []dagql.Selector{{
				Field: "moduleSource",
				Args: []dagql.NamedInput{
					{Name: "refString", Value: dagql.String(refString)},
					{Name: "refPin", Value: dagql.String(parentSrc.Git.Commit)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			}}
			if depName != "" {
				selectors = append(selectors, dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(depName)},
					},
				})
			}
			err := dag.Select(ctx, dag.Root(), &inst, selectors...)
			if err != nil {
				return inst, fmt.Errorf("failed to load local dep: %w", err)
			}
			return inst, nil

		case ModuleSourceKindDir:
			// parent=dir, dep=local
			depPath := filepath.Join(parentSrc.SourceRootSubpath, depSrcRef)
			selectors := []dagql.Selector{{
				Field: "asModuleSource",
				Args: []dagql.NamedInput{
					{Name: "sourceRootPath", Value: dagql.String(depPath)},
					{Name: "disableFindUp", Value: dagql.Boolean(true)},
				},
			}}
			if depName != "" {
				selectors = append(selectors, dagql.Selector{
					Field: "withName",
					Args: []dagql.NamedInput{
						{Name: "name", Value: dagql.String(depName)},
					},
				})
			}
			err := dag.Select(ctx, parentSrc.ContextDirectory, &inst, selectors...)
			if err != nil {
				return inst, fmt.Errorf("failed to load local dep: %w", err)
			}
			return inst, nil

		default:
			return inst, fmt.Errorf("unsupported parent module source kind: %s", parentSrc.Kind)
		}

	case ModuleSourceKindGit:
		// parent=*, dep=git
		selectors := []dagql.Selector{{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "refString", Value: dagql.String(depSrcRef)},
				{Name: "refPin", Value: dagql.String(depPin)},
			},
		}}
		if depName != "" {
			selectors = append(selectors, dagql.Selector{
				Field: "withName",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(depName)},
				},
			})
		}
		err := dag.Select(ctx, dag.Root(), &inst, selectors...)
		if err != nil {
			return inst, fmt.Errorf("failed to load git dep: %w", err)
		}
		return inst, nil

	default:
		return inst, fmt.Errorf("unsupported module source kind: %s", parsedDepRef.Kind)
	}
}

type StatFS interface {
	Stat(ctx context.Context, path string) (*fsutiltypes.Stat, error)
}

type StatFSFunc func(ctx context.Context, path string) (*fsutiltypes.Stat, error)

func (f StatFSFunc) Stat(ctx context.Context, path string) (*fsutiltypes.Stat, error) {
	return f(ctx, path)
}

type CallerStatFS struct {
	bk *buildkit.Client
}

func NewCallerStatFS(bk *buildkit.Client) *CallerStatFS {
	return &CallerStatFS{bk}
}

func (fs CallerStatFS) Stat(ctx context.Context, path string) (*fsutiltypes.Stat, error) {
	stat, err := fs.bk.StatCallerHostPath(ctx, path, true)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return stat, nil
}

type CoreDirStatFS struct {
	dir *Directory
	bk  *buildkit.Client
}

func NewCoreDirStatFS(dir *Directory, bk *buildkit.Client) *CoreDirStatFS {
	return &CoreDirStatFS{dir, bk}
}

func (fs CoreDirStatFS) Stat(ctx context.Context, path string) (*fsutiltypes.Stat, error) {
	stat, err := fs.dir.Stat(ctx, fs.bk, path)
	if err != nil {
		return nil, err
	}
	stat.Path = path // otherwise stat.Path is just the basename
	return stat, nil
}

type ModuleSourceStatFS struct {
	bk  *buildkit.Client
	src *ModuleSource
}

func NewModuleSourceStatFS(bk *buildkit.Client, src *ModuleSource) *ModuleSourceStatFS {
	return &ModuleSourceStatFS{bk, src}
}

func (fs ModuleSourceStatFS) Stat(ctx context.Context, path string) (*fsutiltypes.Stat, error) {
	if fs.src == nil {
		return nil, os.ErrNotExist
	}

	switch fs.src.Kind {
	case ModuleSourceKindLocal:
		path = filepath.Join(fs.src.Local.ContextDirectoryPath, fs.src.SourceRootSubpath, path)
		return CallerStatFS{fs.bk}.Stat(ctx, path)
	case ModuleSourceKindGit:
		path = filepath.Join("/", fs.src.SourceRootSubpath, path)
		return CoreDirStatFS{
			dir: fs.src.Git.UnfilteredContextDir.Self(),
			bk:  fs.bk,
		}.Stat(ctx, path)
	case ModuleSourceKindDir:
		path = filepath.Join("/", fs.src.SourceRootSubpath, path)
		return CoreDirStatFS{
			dir: fs.src.ContextDirectory.Self(),
			bk:  fs.bk,
		}.Stat(ctx, path)
	default:
		return nil, fmt.Errorf("unsupported module source kind: %s", fs.src.Kind)
	}
}
