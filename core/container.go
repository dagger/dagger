package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"dagger.io/dagger/telemetry"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/transfer/archive"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core/containersource"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/client/llb/sourceresolver"
	"github.com/dagger/dagger/internal/buildkit/exporter/containerimage/exptypes"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerui"
	bkgw "github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/dagger/dagger/util/containerutil"
	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
)

var ErrMountNotExist = errors.New("mount does not exist")

type DefaultTerminalCmdOpts struct {
	Args []string

	// Provide dagger access to the executed command
	ExperimentalPrivilegedNesting dagql.Optional[dagql.Boolean] `default:"false"`

	// Grant the process all root capabilities
	InsecureRootCapabilities dagql.Optional[dagql.Boolean] `default:"false"`
}

// Container is a content-addressed container.
type Container struct {
	// The container's root filesystem.
	FS *dagql.ObjectResult[*Directory]

	// Image configuration (env, workdir, etc)
	Config specs.ImageConfig

	// List of GPU devices that will be exposed to the container
	EnabledGPUs []string

	// Mount points configured for the container.
	Mounts ContainerMounts

	// Meta is the /dagger filesystem. It will be null if nothing has run yet.
	Meta *Directory

	// The platform of the container's rootfs.
	Platform Platform

	// OCI annotations
	Annotations []containerutil.ContainerAnnotation

	// Secrets to expose to the container.
	Secrets []ContainerSecret

	// Sockets to expose to the container.
	Sockets []ContainerSocket

	// Image reference
	ImageRef string

	// Ports to expose from the container.
	Ports []Port

	// Services to start before running the container.
	Services ServiceBindings

	// The args to invoke when using the terminal api on this container.
	DefaultTerminalCmd DefaultTerminalCmdOpts

	// (Internal-only for now) Environment variables from the engine container, prefixed
	// with a special value, that will be inherited by this container if set.
	SystemEnvNames []string

	// DefaultArgs have been explicitly set by the user
	DefaultArgs bool
}

func (*Container) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Container",
		NonNull:   true,
	}
}

func (*Container) TypeDescription() string {
	return "An OCI-compatible container, also known as a Docker container."
}

var _ HasPBDefinitions = (*Container)(nil)

func (container *Container) PBDefinitions(ctx context.Context) ([]*pb.Definition, error) {
	if container == nil {
		return nil, nil
	}
	var defs []*pb.Definition
	if fs := container.FS; fs != nil && fs.Self().LLB != nil {
		defs = append(defs, fs.Self().LLB)
	} else {
		defs = append(defs, nil)
	}
	for _, mnt := range container.Mounts {
		handleMount(mnt,
			func(dir *dagql.ObjectResult[*Directory]) {
				if dir.Self().LLB != nil {
					defs = append(defs, dir.Self().LLB)
				}
			},
			func(file *dagql.ObjectResult[*File]) {
				if file.Self().LLB != nil {
					defs = append(defs, file.Self().LLB)
				}
			},
			func(cache *CacheMountSource) {
				if cache.Base != nil {
					defs = append(defs, cache.Base)
				}
			},
			func(tmpfs *TmpfsMountSource) {},
		)
	}
	for _, bnd := range container.Services {
		ctr := bnd.Service.Self().Container
		if ctr == nil {
			continue
		}
		ctrDefs, err := ctr.PBDefinitions(ctx)
		if err != nil {
			return nil, err
		}
		defs = append(defs, ctrDefs...)
	}
	return defs, nil
}

func NewContainer(platform Platform) *Container {
	return &Container{Platform: platform}
}

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (container *Container) Clone() *Container {
	if container == nil {
		return nil
	}
	cp := *container
	cp.Config.ExposedPorts = maps.Clone(cp.Config.ExposedPorts)
	cp.Config.Env = slices.Clone(cp.Config.Env)
	cp.Config.Entrypoint = slices.Clone(cp.Config.Entrypoint)
	cp.Config.Cmd = slices.Clone(cp.Config.Cmd)
	cp.Config.Volumes = maps.Clone(cp.Config.Volumes)
	cp.Config.Labels = maps.Clone(cp.Config.Labels)
	cp.Mounts = slices.Clone(cp.Mounts)
	cp.Secrets = slices.Clone(cp.Secrets)
	cp.Sockets = slices.Clone(cp.Sockets)
	cp.Ports = slices.Clone(cp.Ports)
	cp.Services = slices.Clone(cp.Services)
	cp.SystemEnvNames = slices.Clone(cp.SystemEnvNames)
	return &cp
}

func (container *Container) WithoutInputs() *Container {
	container = container.Clone()

	container.FS = nil
	container.Meta = nil

	for i, mount := range container.Mounts {
		mount.DirectorySource = nil
		mount.FileSource = nil
		mount.CacheSource = nil
		mount.TmpfsSource = nil
		container.Mounts[i] = mount
	}

	return container
}

var _ dagql.OnReleaser = (*Container)(nil)

func (container *Container) OnRelease(ctx context.Context) error {
	if container == nil {
		return nil
	}
	// TODO: this might be problematic if directories overlap, could release same result multiple times
	if rootfs := container.FS; rootfs != nil {
		rootfsRes := rootfs.Self().Result
		if rootfsRes != nil {
			err := rootfsRes.Release(ctx)
			if err != nil {
				return err
			}
		}
	}
	if meta := container.Meta; meta != nil {
		metaRes := meta.Result
		if metaRes != nil {
			err := metaRes.Release(ctx)
			if err != nil {
				return err
			}
		}
	}
	for _, mount := range container.Mounts {
		if src := mount.DirectorySource; src != nil {
			if src := mount.DirectorySource.Self(); src != nil {
				res := src.Result
				if res != nil {
					err := res.Release(ctx)
					if err != nil {
						return err
					}
				}
			}
		}
		if src := mount.FileSource; src != nil {
			if src := mount.FileSource.Self(); src != nil {
				res := src.Result
				if res != nil {
					err := res.Release(ctx)
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// Ownership contains a UID/GID pair resolved from a user/group name or ID pair
// provided via the API. It primarily exists to distinguish an unspecified
// ownership from UID/GID 0 (root) ownership.
type Ownership struct {
	UID int
	GID int
}

func (owner Ownership) Opt() llb.ChownOption {
	return llb.WithUIDGID(owner.UID, owner.GID)
}

// ContainerSecret configures a secret to expose, either as an environment
// variable or mounted to a file path.
type ContainerSecret struct {
	Secret    dagql.ObjectResult[*Secret]
	EnvName   string
	MountPath string
	Owner     *Ownership
	Mode      fs.FileMode
}

// ContainerSocket configures a socket to expose, currently as a Unix socket,
// but potentially as a TCP or UDP address in the future.
type ContainerSocket struct {
	Source        *Socket
	ContainerPath string
	Owner         *Ownership
}

// FSState returns the container's root filesystem mount state. If there is
// none (as with an empty container ID), it returns scratch.
func (container *Container) FSState() (llb.State, error) {
	if container.FS == nil {
		return llb.Scratch(), nil
	}

	return container.FS.Self().StateWithSourcePath()
}

// MetaState returns the container's metadata mount state. If the container has
// yet to run, it returns nil.
func (container *Container) MetaState() (*llb.State, error) {
	if container.Meta == nil {
		return nil, nil
	}

	metaSt, err := defToState(container.Meta.LLB)
	if err != nil {
		return nil, err
	}

	return &metaSt, nil
}

// ContainerMount is a mount point configured in a container.
type ContainerMount struct {
	// The path of the mount within the container.
	Target string

	// Configure the mount as read-only.
	Readonly bool

	// The following fields are mutually exclusive, only one of them should be set.

	// The mounted directory
	DirectorySource *dagql.ObjectResult[*Directory]
	// The mounted file
	FileSource *dagql.ObjectResult[*File]
	// The mounted cache
	CacheSource *CacheMountSource
	// The mounted tmpfs
	TmpfsSource *TmpfsMountSource
}

type CacheMountSource struct {
	// The base layers underneath the cache mount, if any
	Base *pb.Definition

	// The path from the Base to use, if any
	BasePath string

	// The ID of the cache mount
	ID string

	// The sharing mode of the cache mount
	SharingMode CacheSharingMode
}

type TmpfsMountSource struct {
	// Configure the size of the mounted tmpfs in bytes
	Size int
}

func handleMountValues[T any](
	mnt ContainerMount,
	onDir func(*dagql.ObjectResult[*Directory]) (T, error),
	onFile func(*dagql.ObjectResult[*File]) (T, error),
	onCache func(*CacheMountSource) (T, error),
	onTmpfs func(*TmpfsMountSource) (T, error),
) (T, error) {
	switch {
	case mnt.DirectorySource != nil:
		return onDir(mnt.DirectorySource)
	case mnt.FileSource != nil:
		return onFile(mnt.FileSource)
	case mnt.CacheSource != nil:
		return onCache(mnt.CacheSource)
	case mnt.TmpfsSource != nil:
		return onTmpfs(mnt.TmpfsSource)
	default:
		var zero T
		return zero, fmt.Errorf("no mount source configured for %s", mnt.Target)
	}
}

func handleMountValue[T any](
	mnt ContainerMount,
	onDir func(*dagql.ObjectResult[*Directory]) T,
	onFile func(*dagql.ObjectResult[*File]) T,
	onCache func(*CacheMountSource) T,
	onTmpfs func(*TmpfsMountSource) T,
) T {
	t, _ := handleMountValues(mnt,
		func(dir *dagql.ObjectResult[*Directory]) (T, error) {
			return onDir(dir), nil
		},
		func(file *dagql.ObjectResult[*File]) (T, error) {
			return onFile(file), nil
		},
		func(cache *CacheMountSource) (T, error) {
			return onCache(cache), nil
		},
		func(tmpfs *TmpfsMountSource) (T, error) {
			return onTmpfs(tmpfs), nil
		},
	)
	return t
}

func handleMount(
	mnt ContainerMount,
	onDir func(*dagql.ObjectResult[*Directory]),
	onFile func(*dagql.ObjectResult[*File]),
	onCache func(*CacheMountSource),
	onTmpfs func(*TmpfsMountSource),
) {
	handleMountValues(mnt,
		func(dir *dagql.ObjectResult[*Directory]) (any, error) {
			onDir(dir)
			return nil, nil
		},
		func(file *dagql.ObjectResult[*File]) (any, error) {
			onFile(file)
			return nil, nil
		},
		func(cache *CacheMountSource) (any, error) {
			onCache(cache)
			return nil, nil
		},
		func(tmpfs *TmpfsMountSource) (any, error) {
			onTmpfs(tmpfs)
			return nil, nil
		},
	)
}

// SourceState returns the state of the source of the mount.
func (mnt ContainerMount) SourceState() (llb.State, error) {
	switch {
	case mnt.DirectorySource != nil:
		return mnt.DirectorySource.Self().StateWithSourcePath()
	case mnt.FileSource != nil:
		return mnt.FileSource.Self().State()
	default:
		return llb.Scratch(), nil
	}
}

// GetLLB returns the associated LLB with a mount
func (mnt *ContainerMount) GetLLB() *pb.Definition {
	var llb *pb.Definition
	handleMount(*mnt,
		func(dir *dagql.ObjectResult[*Directory]) {
			if dir != nil && dir.Self() != nil {
				llb = dir.Self().LLB
			}
		},
		func(file *dagql.ObjectResult[*File]) {
			if file != nil && file.Self() != nil {
				llb = file.Self().LLB
			}
		},
		func(cacheMount *CacheMountSource) {
			llb = cacheMount.Base
		},
		func(tmpMount *TmpfsMountSource) {
			// no LLB
		},
	)
	return llb
}

type ContainerMounts []ContainerMount

func (mnts ContainerMounts) With(newMnt ContainerMount) ContainerMounts {
	mntsCp := make(ContainerMounts, 0, len(mnts))

	// NB: this / might need to change on Windows, but I'm not even sure how
	// mounts work on Windows, so...
	parent := newMnt.Target + "/"

	for _, mnt := range mnts {
		if mnt.Target == newMnt.Target || strings.HasPrefix(mnt.Target, parent) {
			continue
		}

		mntsCp = append(mntsCp, mnt)
	}

	mntsCp = append(mntsCp, newMnt)

	return mntsCp
}

func (mnts ContainerMounts) Replace(newMnt ContainerMount) (ContainerMounts, error) {
	mntsCp := make(ContainerMounts, 0, len(mnts))
	found := false
	for _, mnt := range mnts {
		if mnt.Target == newMnt.Target {
			mntsCp = append(mntsCp, newMnt)
			found = true
		} else {
			mntsCp = append(mntsCp, mnt)
		}
	}
	if !found {
		return nil, fmt.Errorf("failed to replace %s: %w", newMnt.Target, ErrMountNotExist)
	}
	return mntsCp, nil
}

func (container *Container) FromRefString(ctx context.Context, addr string) (*Container, error) {
	refName, err := reference.ParseNormalizedNamed(addr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image address %s: %w", addr, err)
	}

	// add a default :latest if no tag or digest, otherwise this is a no-op
	refName = reference.TagNameOnly(refName)

	var containerArgs []dagql.NamedInput
	if container.Platform.OS != "" {
		containerArgs = append(containerArgs, dagql.NamedInput{Name: "platform", Value: dagql.Opt(container.Platform)})
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dagger server: %w", err)
	}

	ctx, span := Tracer(ctx).Start(ctx, fmt.Sprintf("from %s", addr),
		telemetry.Internal(),
	)
	defer telemetry.EndWithCause(span, nil)

	var ctr dagql.ObjectResult[*Container]
	err = srv.Select(ctx, srv.Root(), &ctr,
		dagql.Selector{
			Field: "container",
			Args:  containerArgs,
		},
		dagql.Selector{
			Field: "from",
			Args: []dagql.NamedInput{
				{Name: "address", Value: dagql.String(refName.String())},
			},
		},
	)
	if err != nil {
		return nil, err
	}

	return ctr.Self(), nil
}

// FromCanonicalRef implements the dagop portion of the "from" command: it fetches an image, and updates the root fs
// to point to a snapshot of the referenced image
func (container *Container) FromCanonicalRef(
	ctx context.Context,
	refName reference.Canonical,
	// cfgBytes is optional, will be retrieved if not provided
	cfgBytes []byte,
) (*Container, error) {
	container = container.Clone()

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	platform := container.Platform

	refStr := refName.String()

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	hsm, err := containersource.NewSource(containersource.SourceOpt{
		Snapshotter:   bk.Worker.Snapshotter,
		ContentStore:  bk.Worker.ContentStore(),
		ImageStore:    bk.Worker.ImageStore,
		CacheAccessor: query.BuildkitCache(),
		RegistryHosts: bk.Worker.RegistryHosts,
		ResolverType:  containersource.ResolverTypeRegistry,
		LeaseManager:  bk.Worker.LeaseManager(),
	})
	if err != nil {
		return nil, err
	}

	attrs := map[string]string{}
	id, err := hsm.Identifier(refStr, attrs, &pb.Platform{
		Architecture: platform.Architecture,
		OS:           platform.OS,
		Variant:      platform.Variant,
		OSVersion:    platform.OSVersion,
		OSFeatures:   platform.OSFeatures,
	})
	if err != nil {
		return nil, err
	}

	src, err := hsm.Resolve(ctx, id, query.BuildkitSession())
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group found")
	}

	ref, err := src.Snapshot(ctx, bkSessionGroup)
	if err != nil {
		return nil, err
	}

	rootfsDir := &Directory{
		Result: ref,
	}
	if container.FS != nil {
		rootfsDir.Dir = container.FS.Self().Dir
		if rootfsDir.Dir == "" {
			return nil, fmt.Errorf("SetFSFromCanonicalRef got an empty dir")
		} else if rootfsDir.Dir != "/" {
			return nil, fmt.Errorf("SetFSFromCanonicalRef got %s as dir; however it will be lost", rootfsDir.Dir)
		}
		rootfsDir.Platform = container.FS.Self().Platform
		rootfsDir.Services = container.FS.Self().Services
	} else {
		rootfsDir.Dir = "/"
	}
	updatedRootFS, err := UpdatedRootFS(ctx, rootfsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to update rootfs: %w", err)
	}
	container.FS = updatedRootFS
	return container, nil
}

// FromCanonicalRefUpdateConfig is must be called outside of a dagop context, and is responsible for fetching the image config
// and applying it to the container's metadata
func (container *Container) FromCanonicalRefUpdateConfig(
	ctx context.Context,
	refName reference.Canonical,
	// cfgBytes is optional, will be retrieved if not provided
	cfgBytes []byte,
) (*Container, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	platform := container.Platform
	refStr := refName.String()

	// since this is an image ref w/ a digest, always check the local cache for the image
	// first before making any network requests
	resolveMode := llb.ResolveModePreferLocal
	if cfgBytes == nil {
		_, _, cfgBytes, err = bk.ResolveImageConfig(ctx, refStr, sourceresolver.Opt{
			Platform: ptr(platform.Spec()),
			ImageOpt: &sourceresolver.ResolveImageOpt{
				ResolveMode: resolveMode.String(),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to resolve image %q (platform: %q): %w", refStr, platform.Format(), err)
		}
	}

	var imgSpec specs.Image
	if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
		return nil, err
	}

	container.Config = mergeImageConfig(container.Config, imgSpec.Config)
	container.ImageRef = refStr
	container.Platform = Platform(platforms.Normalize(imgSpec.Platform))

	return container, nil
}

const defaultDockerfileName = "Dockerfile"

func (container *Container) Build(
	ctx context.Context,
	dockerfileDir *Directory,
	// contextDir is dockerfileDir with files excluded as per dockerignore file
	contextDir *Directory,
	dockerfile string,
	buildArgs []BuildArg,
	target string,
	secrets []dagql.ObjectResult[*Secret],
	secretStore *SecretStore,
	noInit bool,
) (*Container, error) {
	container = container.Clone()

	secretNameToLLBID := make(map[string]string)
	for _, secret := range secrets {
		secretName, ok := secretStore.GetSecretName(secret.ID().Digest())
		if !ok {
			return nil, fmt.Errorf("secret not found: %s", secret.ID().Digest())
		}
		container.Secrets = append(container.Secrets, ContainerSecret{
			Secret:    secret,
			MountPath: fmt.Sprintf("/run/secrets/%s", secretName),
		})
		secretNameToLLBID[secretName] = secret.ID().Digest().String()
	}

	// set image ref to empty string
	container.ImageRef = ""

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	platform := container.Platform

	opts := map[string]string{
		"platform":      platform.Format(),
		"contextsubdir": contextDir.Dir,
	}

	if dockerfile != "" {
		opts["filename"] = filepath.Join(dockerfileDir.Dir, dockerfile)
	} else {
		opts["filename"] = filepath.Join(dockerfileDir.Dir, defaultDockerfileName)
	}

	if target != "" {
		opts["target"] = target
	}

	for _, buildArg := range buildArgs {
		opts["build-arg:"+buildArg.Name] = buildArg.Value
	}

	inputs := map[string]*pb.Definition{
		dockerui.DefaultLocalNameContext:    contextDir.LLB,
		dockerui.DefaultLocalNameDockerfile: dockerfileDir.LLB,
	}

	// FIXME: this is a terrible way to pass this around
	solveCtx := buildkit.WithSecretTranslator(ctx, func(name string, optional bool) (string, error) {
		llbID, ok := secretNameToLLBID[name]
		if !ok {
			if optional {
				// set to a purposely invalid name, so we don't get something else
				return "notfound:" + identity.NewID(), nil
			}
			return "", fmt.Errorf("secret not found: %s", name)
		}
		return llbID, nil
	})

	res, err := bk.Solve(solveCtx, bkgw.SolveRequest{
		Frontend:       "dockerfile.v0",
		FrontendOpt:    opts,
		FrontendInputs: inputs,
	})
	if err != nil {
		return nil, err
	}

	bkref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	var st llb.State
	if bkref == nil {
		st = llb.Scratch()
	} else {
		st, err = bkref.ToState()
		if err != nil {
			return nil, err
		}
	}

	def, err := st.Marshal(ctx, llb.Platform(platform.Spec()))
	if err != nil {
		return nil, err
	}

	dag, err := buildkit.DefToDAG(def.ToPB())
	if err != nil {
		return nil, err
	}
	if err := dag.Walk(func(dag *buildkit.OpDAG) error {
		// forcibly inject our trace context into each op, since st.Marshal
		// isn't strong enough to do so
		desc := dag.Metadata.Description
		if desc == nil {
			desc = map[string]string{}
		}
		if desc["traceparent"] == "" {
			telemetry.Propagator.Inject(ctx,
				propagation.MapCarrier(desc))
		}

		execOp, isExecOp := dag.AsExec()
		if noInit && isExecOp {
			execMD, ok, err := buildkit.ExecutionMetadataFromDescription(desc)
			if err != nil {
				return fmt.Errorf("failed to get execution metadata: %w", err)
			}
			if !ok {
				execMD = &buildkit.ExecutionMetadata{}
			}
			execMD.NoInit = true
			if err := buildkit.AddExecutionMetadataToDescription(desc, execMD); err != nil {
				return fmt.Errorf("failed to add execution metadata: %w", err)
			}
			execOp.Meta.Env = append(execOp.Meta.Env,
				buildkit.DaggerNoInitEnv+"=true",
			)
		}

		dag.Metadata.Description = desc
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk DAG: %w", err)
	}
	newDef, err := dag.Marshal()
	if err != nil {
		return nil, err
	}
	if newDef != nil {
		newDef.Source = nil
	}

	rootfsDir := NewDirectory(newDef, "/", container.Platform, container.Services)
	container.FS, err = UpdatedRootFS(ctx, rootfsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs directory: %w", err)
	}

	cfgBytes, found := res.Metadata[exptypes.ExporterImageConfigKey]
	if found {
		var imgSpec specs.Image
		if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
			return nil, err
		}

		container.Config = mergeImageConfig(container.Config, imgSpec.Config)
	}

	return container, nil
}

func (container *Container) RootFS(ctx context.Context) (*Directory, error) {
	if container.FS != nil {
		return container.FS.Self(), nil
	}
	return NewScratchDirectory(ctx, container.Platform)
}

func (container *Container) WithRootFS(ctx context.Context, dir dagql.ObjectResult[*Directory]) (*Container, error) {
	container = container.Clone()
	container.FS = &dir

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithDirectory(
	ctx context.Context,
	subdir string,
	src dagql.ObjectResult[*Directory],
	filter CopyFilter,
	owner string,
) (*Container, error) {
	container = container.Clone()

	mnt, mntSubpath, err := locatePath(container, subdir)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", subdir, err)
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dagger server: %w", err)
	}

	// if the path being overwritten is an exact mount point for a file, then we need to unmount it
	// and then overwrite the source that exists below it (including unmounting any mounts below it)
	if mnt != nil && mnt.FileSource != nil && (mntSubpath == "/" || mntSubpath == "" || mntSubpath == ".") {
		container, err = container.WithoutMount(ctx, mnt.Target)
		if err != nil {
			return nil, fmt.Errorf("failed to unmount %s: %w", mnt.Target, err)
		}
		return container.WithDirectory(ctx, subdir, src, filter, owner)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
		{Name: "source", Value: dagql.NewID[*Directory](src.ID())},
	}
	if len(filter.Exclude) > 0 {
		args = append(args, dagql.NamedInput{Name: "exclude", Value: asArrayInput(filter.Exclude, dagql.NewString)})
	}
	if len(filter.Include) > 0 {
		args = append(args, dagql.NamedInput{Name: "include", Value: asArrayInput(filter.Include, dagql.NewString)})
	}
	if filter.Gitignore {
		args = append(args, dagql.NamedInput{Name: "gitignore", Value: dagql.Boolean(true)})
	}
	if owner != "" {
		// directories only handle int uid/gid, so make sure we resolve names if needed
		ownership, err := container.ownership(ctx, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve ownership for %s: %w", owner, err)
		}
		owner := strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)
		args = append(args, dagql.NamedInput{Name: "owner", Value: dagql.String(owner)})
	}

	//nolint:dupl
	switch {
	case mnt == nil: // rootfs
		selectors := []dagql.Selector{
			{
				Field: "withDirectory",
				Args:  args,
			},
		}
		queryParent := dagql.AnyObjectResult(container.FS)
		if container.FS == nil {
			// need to start from a scratch directory
			selectors = append([]dagql.Selector{{
				Field: "directory",
			}}, selectors...)
			queryParent = srv.Root()
		}
		var newRootfs dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, queryParent, &newRootfs, selectors...)
		if err != nil {
			return nil, err
		}
		return container.WithRootFS(ctx, newRootfs)

	case mnt.DirectorySource != nil: // directory mount
		var newDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, mnt.DirectorySource, &newDir, dagql.Selector{
			Field: "withDirectory",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.replaceMount(mnt.Target, newDir)

	case mnt.FileSource != nil: // file mount
		// should be handled by the check for exact mount point above
		return nil, fmt.Errorf("invalid mount source for %s", subdir)

	default:
		return nil, fmt.Errorf("invalid mount source for %s", subdir)
	}
}

func (container *Container) WithFile(
	ctx context.Context,
	srv *dagql.Server,
	destPath string,
	src dagql.ObjectResult[*File],
	permissions *int,
	owner string,
) (*Container, error) {
	container = container.Clone()

	mnt, mntSubpath, err := locatePath(container, destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", destPath, err)
	}

	// if the path being overwritten is an exact mount point, then we need to unmount
	// it and then overwrite the source that exists below it (including unmounting any mounts below it)
	if mnt != nil && (mntSubpath == "/" || mntSubpath == "" || mntSubpath == ".") {
		container, err = container.WithoutMount(ctx, mnt.Target)
		if err != nil {
			return nil, fmt.Errorf("failed to unmount %s: %w", mnt.Target, err)
		}
		return container.WithFile(ctx, srv, destPath, src, permissions, owner)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
		{Name: "source", Value: dagql.NewID[*File](src.ID())},
	}
	if permissions != nil {
		args = append(args, dagql.NamedInput{Name: "permissions", Value: dagql.Opt(dagql.Int(*permissions))})
	}
	if owner != "" {
		// files only handle int uid/gid, so make sure we resolve names if needed
		ownership, err := container.ownership(ctx, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve ownership for %s: %w", owner, err)
		}
		owner := strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)
		args = append(args, dagql.NamedInput{Name: "owner", Value: dagql.String(owner)})
	}

	//nolint:dupl
	switch {
	case mnt == nil: // rootfs
		selectors := []dagql.Selector{{
			Field: "withFile",
			Args:  args,
		}}
		queryParent := dagql.AnyObjectResult(container.FS)
		if container.FS == nil {
			// need to start from a scratch directory
			selectors = append([]dagql.Selector{{
				Field: "directory",
			}}, selectors...)
			queryParent = srv.Root()
		}
		var newRootfs dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, queryParent, &newRootfs, selectors...)
		if err != nil {
			return nil, err
		}
		return container.WithRootFS(ctx, newRootfs)

	case mnt.DirectorySource != nil: // directory mount
		var newDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, mnt.DirectorySource, &newDir, dagql.Selector{
			Field: "withFile",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.replaceMount(mnt.Target, newDir)

	case mnt.FileSource != nil: // file mount
		// should be handled by the check for exact mount point above
		return nil, fmt.Errorf("invalid mount source for %s", destPath)

	default:
		return nil, fmt.Errorf("invalid mount source for %s", destPath)
	}
}

func (container *Container) WithoutPaths(ctx context.Context, srv *dagql.Server, destPaths ...string) (*Container, error) {
	container = container.Clone()

	for _, destPath := range destPaths {
		var err error
		container, err = container.withoutPath(ctx, srv, destPath)
		if err != nil {
			return nil, fmt.Errorf("failed to remove path %q: %w", destPath, err)
		}
	}
	return container, nil
}

// assumes that container is already cloned by caller
func (container *Container) withoutPath(
	ctx context.Context,
	srv *dagql.Server,
	destPath string,
) (*Container, error) {
	mnt, mntSubpath, err := locatePath(container, destPath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", destPath, err)
	}

	// if the path being removed is an exact mount point, then we need to unmount it and then
	// (recursively) remove the source that exists below it (including unmounting any mounts below it)
	if mnt != nil && (mntSubpath == "/" || mntSubpath == "" || mntSubpath == ".") {
		container, err = container.WithoutMount(ctx, mnt.Target)
		if err != nil {
			return nil, fmt.Errorf("failed to unmount %s: %w", mnt.Target, err)
		}
		return container.withoutPath(ctx, srv, destPath)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
	}

	// Directory.withoutDirectory and Directory.withoutFile are actually the same thing, so choose one arbitrarily
	switch {
	case mnt == nil: // rootfs
		if container.FS == nil {
			// rootfs is an empty dir, nothing to do
			return container, nil
		}
		var newRootfs dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, container.FS, &newRootfs, dagql.Selector{
			Field: "withoutDirectory",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.WithRootFS(ctx, newRootfs)

	case mnt.DirectorySource != nil: // directory mount
		var newDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, mnt.DirectorySource, &newDir, dagql.Selector{
			Field: "withoutDirectory",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.replaceMount(mnt.Target, newDir)

	case mnt.FileSource != nil: // file mount
		// This should be handled by the check above for whether the path being removed is an exact mount point
		return nil, fmt.Errorf("invalid mount source for %s", destPath)

	default:
		return nil, fmt.Errorf("invalid mount source for %s", destPath)
	}
}

func (container *Container) WithFiles(
	ctx context.Context,
	srv *dagql.Server,
	destDir string,
	src []dagql.ObjectResult[*File],
	permissions *int,
	owner string,
) (*Container, error) {
	container = container.Clone()

	for _, file := range src {
		destPath := filepath.Join(destDir, filepath.Base(file.Self().File))
		var err error
		container, err = container.WithFile(ctx, srv, destPath, file, permissions, owner)
		if err != nil {
			return nil, fmt.Errorf("failed to add file %s: %w", destPath, err)
		}
	}

	return container, nil
}

func (container *Container) WithNewFile(
	ctx context.Context,
	dest string,
	content []byte,
	permissions fs.FileMode,
	owner string,
) (*Container, error) {
	container = container.Clone()

	_, fileName := filepath.Split(filepath.Clean(dest))

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	var newFile dagql.ObjectResult[*File]
	args := []dagql.NamedInput{
		{Name: "name", Value: dagql.String(fileName)},
		{Name: "contents", Value: dagql.String(string(content))},
	}
	if permissions != 0 {
		args = append(args, dagql.NamedInput{Name: "permissions", Value: dagql.Opt(dagql.Int(int(permissions)))})
	}
	err = srv.Select(ctx, srv.Root(), &newFile, dagql.Selector{
		Field: "file",
		Args:  args,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create new file %s: %w", dest, err)
	}

	return container.WithFile(ctx, srv, dest, newFile, nil, owner)
}

func (container *Container) WithSymlink(ctx context.Context, srv *dagql.Server, target, linkPath string) (*Container, error) {
	container = container.Clone()

	mnt, mntSubpath, err := locatePath(container, linkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", linkPath, err)
	}

	// if the path being overwritten is an exact mount point, then we need to unmount it and then overwrite the source that exists below it (including unmounting any mounts below it)
	if mnt != nil && (mntSubpath == "/" || mntSubpath == "" || mntSubpath == ".") {
		container, err = container.WithoutMount(ctx, mnt.Target)
		if err != nil {
			return nil, fmt.Errorf("failed to unmount %s: %w", mnt.Target, err)
		}
		return container.WithSymlink(ctx, srv, target, linkPath)
	}

	args := []dagql.NamedInput{
		{Name: "target", Value: dagql.String(target)},
		{Name: "linkName", Value: dagql.String(mntSubpath)},
	}

	//nolint:dupl
	switch {
	case mnt == nil: // rootfs
		selectors := []dagql.Selector{{
			Field: "withSymlink",
			Args:  args,
		}}
		queryParent := dagql.AnyObjectResult(container.FS)
		if container.FS == nil {
			// need to start from a scratch directory
			selectors = append([]dagql.Selector{{
				Field: "directory",
			}}, selectors...)
			queryParent = srv.Root()
		}
		var newRootfs dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, queryParent, &newRootfs, selectors...)
		if err != nil {
			return nil, err
		}
		return container.WithRootFS(ctx, newRootfs)

	case mnt.DirectorySource != nil: // directory mount
		var newDir dagql.ObjectResult[*Directory]
		err = srv.Select(ctx, mnt.DirectorySource, &newDir, dagql.Selector{
			Field: "withSymlink",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}
		return container.replaceMount(mnt.Target, newDir)

	case mnt.FileSource != nil: // file mount
		// should be handled by the check for exact mount point above
		return nil, fmt.Errorf("invalid mount source for %s", linkPath)

	default:
		return nil, fmt.Errorf("invalid mount source for %s", linkPath)
	}
}

func (container *Container) WithMountedDirectory(
	ctx context.Context,
	target string,
	dir dagql.ObjectResult[*Directory],
	owner string,
	readonly bool,
) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	var err error
	if owner != "" {
		dir, err = container.chownDir(ctx, dir, owner)
		if err != nil {
			return nil, err
		}
	}

	container.Mounts = container.Mounts.With(ContainerMount{
		DirectorySource: &dir,
		Target:          target,
		Readonly:        readonly,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithMountedFile(
	ctx context.Context,
	target string,
	file dagql.ObjectResult[*File],
	owner string,
	readonly bool,
) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	var err error
	if owner != "" {
		file, err = container.chownFile(ctx, file, owner)
		if err != nil {
			return nil, err
		}
	}

	container.Mounts = container.Mounts.With(ContainerMount{
		FileSource: &file,
		Target:     target,
		Readonly:   readonly,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

var SeenCacheKeys = new(sync.Map)

func (container *Container) WithMountedCache(
	ctx context.Context,
	target string,
	cache *CacheVolume,
	source *Directory,
	sharingMode CacheSharingMode,
	owner string,
) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	if sharingMode == "" {
		sharingMode = CacheSharingModeShared
	}

	mount := ContainerMount{
		Target: target,
		CacheSource: &CacheMountSource{
			ID:          cache.Sum(),
			SharingMode: sharingMode,
		},
	}

	if source != nil {
		mount.CacheSource.Base = source.LLB
		mount.CacheSource.BasePath = source.Dir
	}

	if owner != "" {
		var err error
		mount.CacheSource.Base, mount.CacheSource.BasePath, err = container.chownLLB(
			ctx,
			mount.CacheSource.Base,
			mount.CacheSource.BasePath,
			owner,
			llb.Platform(container.Platform.Spec()),
		)
		if err != nil {
			return nil, err
		}
	}

	container.Mounts = container.Mounts.With(mount)

	// set image ref to empty string
	container.ImageRef = ""

	SeenCacheKeys.Store(cache.Keys[0], struct{}{})

	return container, nil
}

func (container *Container) WithMountedTemp(ctx context.Context, target string, size int) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	container.Mounts = container.Mounts.With(ContainerMount{
		Target: target,
		TmpfsSource: &TmpfsMountSource{
			Size: size,
		},
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithMountedSecret(
	ctx context.Context,
	target string,
	source dagql.ObjectResult[*Secret],
	owner string,
	mode fs.FileMode,
) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	ownership, err := container.ownership(ctx, owner)
	if err != nil {
		return nil, err
	}

	container.Secrets = append(container.Secrets, ContainerSecret{
		Secret:    source,
		MountPath: target,
		Owner:     ownership,
		Mode:      mode,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithoutMount(ctx context.Context, target string) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	var found bool
	var foundIdx int
	for i := len(container.Mounts) - 1; i >= 0; i-- {
		if container.Mounts[i].Target == target {
			found = true
			foundIdx = i
			break
		}
	}

	if found {
		container.Mounts = slices.Delete(container.Mounts, foundIdx, foundIdx+1)
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) MountTargets(ctx context.Context) ([]string, error) {
	mounts := []string{}
	for _, mnt := range container.Mounts {
		mounts = append(mounts, mnt.Target)
	}

	return mounts, nil
}

func (container *Container) WithUnixSocket(ctx context.Context, target string, source *Socket, owner string) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	ownership, err := container.ownership(ctx, owner)
	if err != nil {
		return nil, err
	}

	newSocket := ContainerSocket{
		Source:        source,
		ContainerPath: target,
		Owner:         ownership,
	}

	var replaced bool
	for i, sock := range container.Sockets {
		if sock.ContainerPath == target {
			container.Sockets[i] = newSocket
			replaced = true
			break
		}
	}

	if !replaced {
		container.Sockets = append(container.Sockets, newSocket)
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithoutUnixSocket(ctx context.Context, target string) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	for i, sock := range container.Sockets {
		if sock.ContainerPath == target {
			container.Sockets = slices.Delete(container.Sockets, i, i+1)
			break
		}
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithSecretVariable(
	ctx context.Context,
	name string,
	secret dagql.ObjectResult[*Secret],
) (*Container, error) {
	container = container.Clone()

	container.Secrets = append(container.Secrets, ContainerSecret{
		Secret:  secret,
		EnvName: name,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithoutSecretVariable(ctx context.Context, name string) (*Container, error) {
	container = container.Clone()

	for i, secret := range container.Secrets {
		if secret.EnvName == name {
			container.Secrets = slices.Delete(container.Secrets, i, i+1)
			break
		}
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) Directory(ctx context.Context, dirPath string) (*Directory, error) {
	mnt, subpath, err := locatePath(container, dirPath)
	if err != nil {
		return nil, err
	}

	var dir *Directory
	switch {
	case mnt == nil: // rootfs
		if container.FS == nil {
			dir, err = NewScratchDirectory(ctx, container.Platform)
			if err != nil {
				return nil, fmt.Errorf("failed to create scratch directory: %w", err)
			}
			dir, err = dir.Directory(ctx, subpath)
		} else {
			dir, err = container.FS.Self().Directory(ctx, subpath)
		}
	case mnt.DirectorySource != nil: // mounted directory
		dir, err = mnt.DirectorySource.Self().Directory(ctx, subpath)
	case mnt.FileSource != nil: // mounted file
		return nil, fmt.Errorf("path %s is a file, not a directory", dirPath)
	default:
		return nil, fmt.Errorf("invalid path %s in container mounts", dirPath)
	}

	switch {
	case err == nil:
		return dir, nil
	case errors.As(err, &notADirectoryError{}):
		// fix the error message to use dirPath rather than subpath
		return nil, notADirectoryError{fmt.Errorf("path %s is a file, not a directory", dirPath)}
	default:
		return nil, err
	}
}

func (container *Container) File(ctx context.Context, filePath string) (*File, error) {
	mnt, subpath, err := locatePath(container, filePath)
	if err != nil {
		return nil, err
	}

	var f *File
	switch {
	case mnt == nil: // rootfs
		if container.FS == nil {
			return nil, fmt.Errorf("container rootfs is not set")
		}
		f, err = container.FS.Self().File(ctx, subpath)
	case mnt.DirectorySource != nil: // mounted directory
		f, err = mnt.DirectorySource.Self().File(ctx, subpath)
		err = RestoreErrPath(err, filePath) // preserve the full filePath, rather than subpath
	case mnt.FileSource != nil: // mounted file
		return mnt.FileSource.Self(), nil
	default:
		return nil, fmt.Errorf("invalid path %s in container mounts", filePath)
	}

	switch {
	case err == nil:
		return f, nil
	case errors.As(err, &notAFileError{}):
		// fix the error message to use filePath rather than subpath
		return nil, notAFileError{fmt.Errorf("path %s is a directory, not a file", filePath)}
	default:
		return nil, err
	}
}

// locatePath finds the mount that contains the given container path. It returns
// the mount and the subpath of containerPath relative to the mountpoint.
func locatePath(
	container *Container,
	containerPath string,
) (*ContainerMount, string, error) {
	containerPath = absPath(container.Config.WorkingDir, containerPath)

	// NB(vito): iterate in reverse order so we'll find deeper mounts first
	for i := len(container.Mounts) - 1; i >= 0; i-- {
		mnt := container.Mounts[i]

		if containerPath == mnt.Target || strings.HasPrefix(containerPath, mnt.Target+"/") {
			if mnt.TmpfsSource != nil {
				return nil, "", fmt.Errorf("%s: cannot retrieve path from tmpfs", containerPath)
			}

			if mnt.CacheSource != nil {
				return nil, "", fmt.Errorf("%s: cannot retrieve path from cache", containerPath)
			}

			relPath, err := filepath.Rel(mnt.Target, containerPath)
			if err != nil {
				return nil, "", err
			}
			return &mnt, relPath, nil
		}
	}

	// Not found in a mount
	return nil, containerPath, nil
}

func (container *Container) replaceMount(
	target string,
	dir dagql.ObjectResult[*Directory],
) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	var err error
	container.Mounts, err = container.Mounts.Replace(ContainerMount{
		DirectorySource: &dir,
		Target:          target,
	})
	if err != nil {
		return nil, err
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) chownDir(
	ctx context.Context,
	src dagql.ObjectResult[*Directory],
	owner string,
) (res dagql.ObjectResult[*Directory], err error) {
	ownership, err := container.ownership(ctx, owner)
	if err != nil {
		return res, err
	}

	if ownership == nil {
		return src, nil
	}

	// Directory.chown only knows uid/gid ints, provide those
	owner = strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get dagql server: %w", err)
	}

	err = srv.Select(ctx, src, &res, dagql.Selector{
		Field: "chown",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(".")},
			{Name: "owner", Value: dagql.String(owner)},
		},
	})
	if err != nil {
		return res, fmt.Errorf("failed to chown directory: %w", err)
	}

	return res, nil
}

func (container *Container) chownFile(
	ctx context.Context,
	src dagql.ObjectResult[*File],
	owner string,
) (res dagql.ObjectResult[*File], err error) {
	ownership, err := container.ownership(ctx, owner)
	if err != nil {
		return res, err
	}

	if ownership == nil {
		return src, nil
	}

	// File.chown only knows uid/gid ints, provide those
	owner = strconv.Itoa(ownership.UID) + ":" + strconv.Itoa(ownership.GID)

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to get dagql server: %w", err)
	}

	err = srv.Select(ctx, src, &res, dagql.Selector{
		Field: "chown",
		Args: []dagql.NamedInput{
			{Name: "owner", Value: dagql.String(owner)},
		},
	})
	if err != nil {
		return res, fmt.Errorf("failed to chown file: %w", err)
	}

	return res, nil
}

func (container *Container) chownLLB(
	ctx context.Context,
	srcDef *pb.Definition,
	srcPath string,
	owner string,
	opts ...llb.ConstraintsOpt,
) (*pb.Definition, string, error) {
	ownership, err := container.ownership(ctx, owner)
	if err != nil {
		return nil, "", err
	}

	if ownership == nil {
		return srcDef, srcPath, nil
	}

	var srcSt llb.State
	if srcDef == nil {
		// e.g. empty cache mount
		srcSt = llb.Scratch().File(
			llb.Mkdir("/chown", 0o755, ownership.Opt()),
		)

		srcPath = "/chown"
	} else {
		srcSt, err = defToState(srcDef)
		if err != nil {
			return nil, "", err
		}

		def, err := srcSt.Marshal(ctx, opts...)
		if err != nil {
			return nil, "", err
		}

		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, "", err
		}
		bk, err := query.Buildkit(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get buildkit client: %w", err)
		}
		ref, err := bkRef(ctx, bk, def.ToPB())
		if err != nil {
			return nil, "", err
		}

		stat, err := ref.StatFile(ctx, bkgw.StatRequest{
			Path: srcPath,
		})
		if err != nil {
			return nil, "", err
		}

		if stat.IsDir() {
			chowned := "/chown"

			// NB(vito): need to create intermediate directory with correct ownership
			// to handle the directory case, otherwise the mount will be owned by
			// root
			srcSt = llb.Scratch().File(
				llb.Mkdir(chowned, os.FileMode(stat.Mode), ownership.Opt()).
					Copy(srcSt, srcPath, chowned, &llb.CopyInfo{
						CopyDirContentsOnly: true,
					}, ownership.Opt()),
			)

			srcPath = chowned
		} else {
			srcSt = llb.Scratch().File(
				llb.Copy(srcSt, srcPath, ".", ownership.Opt()),
			)

			srcPath = filepath.Base(srcPath)
		}
	}

	def, err := srcSt.Marshal(ctx, opts...)
	if err != nil {
		return nil, "", err
	}

	return def.ToPB(), srcPath, nil
}

func (container *Container) ImageConfig(ctx context.Context) (specs.ImageConfig, error) {
	return container.Config, nil
}

func (container *Container) UpdateImageConfig(ctx context.Context, updateFn func(specs.ImageConfig) specs.ImageConfig) (*Container, error) {
	container = container.Clone()
	container.Config = updateFn(container.Config)
	return container, nil
}

type ContainerGPUOpts struct {
	Devices []string
}

func (container *Container) WithGPU(ctx context.Context, gpuOpts ContainerGPUOpts) (*Container, error) {
	container = container.Clone()
	container.EnabledGPUs = gpuOpts.Devices
	return container, nil
}

func (container *Container) Evaluate(ctx context.Context) (*buildkit.Result, error) {
	if container == nil {
		return nil, nil
	}
	if container.FS == nil {
		return nil, nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	st, err := container.FSState()
	if err != nil {
		return nil, err
	}

	def, err := st.Marshal(ctx, llb.Platform(container.Platform.Spec()))
	if err != nil {
		return nil, err
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	ctx, span := Tracer(ctx).Start(ctx, "evaling", telemetry.Internal())
	defer span.End()

	return bk.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: def.ToPB(),
	})
}

func (container *Container) Exists(ctx context.Context, srv *dagql.Server, targetPath string, targetType ExistsType, doNotFollowSymlinks bool) (bool, error) {
	mnt, mntSubpath, err := locatePath(container, targetPath)
	if err != nil {
		return false, fmt.Errorf("failed to locate path %s: %w", targetPath, err)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
	}
	if targetType != "" {
		args = append(args, dagql.NamedInput{Name: "expectedType", Value: dagql.Opt[ExistsType](targetType)})
	}
	if doNotFollowSymlinks {
		args = append(args, dagql.NamedInput{Name: "doNotFollowSymlinks", Value: dagql.Opt[dagql.Boolean](dagql.Boolean(doNotFollowSymlinks))})
	}

	var exists bool
	switch {
	case mnt == nil: // rootfs
		err = srv.Select(ctx, container.FS, &exists, dagql.Selector{
			Field: "exists",
			Args:  args,
		})
		if err != nil {
			return false, err
		}

	case mnt.DirectorySource != nil: // directory mount
		err = srv.Select(ctx, mnt.DirectorySource, &exists, dagql.Selector{
			Field: "exists",
			Args:  args,
		})
		if err != nil {
			return false, err
		}

	case mnt.FileSource != nil: // file mount
		if targetType == "" {
			return true, nil
		}
		return targetType == ExistsTypeRegular, nil

	default:
		return false, fmt.Errorf("invalid mount source for %s", targetPath)
	}

	return exists, nil
}

func (container *Container) Stat(ctx context.Context, srv *dagql.Server, targetPath string, doNotFollowSymlinks bool) (*Stat, error) {
	mnt, mntSubpath, err := locatePath(container, targetPath)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", targetPath, err)
	}

	args := []dagql.NamedInput{
		{Name: "path", Value: dagql.String(mntSubpath)},
	}
	if doNotFollowSymlinks {
		args = append(args, dagql.NamedInput{Name: "doNotFollowSymlinks", Value: dagql.Opt[dagql.Boolean](dagql.Boolean(doNotFollowSymlinks))})
	}

	var stat *Stat
	switch {
	case mnt == nil: // rootfs
		err = srv.Select(ctx, container.FS, &stat, dagql.Selector{
			Field: "stat",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}

	case mnt.DirectorySource != nil: // directory mount
		err = srv.Select(ctx, mnt.DirectorySource, &stat, dagql.Selector{
			Field: "stat",
			Args:  args,
		})
		if err != nil {
			return nil, err
		}

	case mnt.FileSource != nil: // file mount
		err = srv.Select(ctx, mnt.FileSource, &stat, dagql.Selector{
			Field: "stat",
		})
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("invalid mount source for %s", targetPath)
	}

	return stat, nil
}

func (container *Container) WithAnnotation(ctx context.Context, key, value string) (*Container, error) {
	container = container.Clone()

	container.Annotations = append(container.Annotations, containerutil.ContainerAnnotation{
		Key:   key,
		Value: value,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithoutAnnotation(ctx context.Context, name string) (*Container, error) {
	container = container.Clone()

	for i, annotation := range container.Annotations {
		if annotation.Key == name {
			container.Annotations = slices.Delete(container.Annotations, i, i+1)
			break
		}
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) Publish(
	ctx context.Context,
	ref string,
	platformVariants []*Container,
	forcedCompression ImageLayerCompression,
	mediaTypes ImageMediaTypes,
) (string, error) {
	variants := filterEmptyContainers(append([]*Container{container}, platformVariants...))
	inputByPlatform, err := getVariantRefs(ctx, variants)
	if err != nil {
		return "", err
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get buildkit client: %w", err)
	}

	resp, err := bk.PublishContainerImage(ctx, inputByPlatform, ref, useOCIMediaTypes(mediaTypes), string(forcedCompression))
	if err != nil {
		return "", err
	}

	refName, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", err
	}

	imageDigest, found := resp[exptypes.ExporterImageDigestKey]
	if found {
		dig, err := digest.Parse(imageDigest)
		if err != nil {
			return "", fmt.Errorf("parse digest: %w", err)
		}

		withDig, err := reference.WithDigest(refName, dig)
		if err != nil {
			return "", fmt.Errorf("with digest: %w", err)
		}

		return withDig.String(), nil
	}

	return ref, nil
}

func (container *Container) AsTarball(
	ctx context.Context,
	platformVariants []*Container,
	forcedCompression ImageLayerCompression,
	mediaTypes ImageMediaTypes,
	filePath string,
) (f *File, rerr error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	engineHostPlatform := query.Platform()

	if mediaTypes == "" {
		mediaTypes = OCIMediaTypes
	}

	variants := filterEmptyContainers(append([]*Container{container}, platformVariants...))
	inputByPlatform, err := getVariantRefs(ctx, variants)
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group found")
	}

	bkref, err := query.BuildkitCache().New(ctx, nil, bkSessionGroup,
		bkcache.CachePolicyRetain,
		bkcache.WithRecordType(bkclient.UsageRecordTypeRegular),
		bkcache.WithDescription("dagop.fs container.asTarball "+filePath),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil && bkref != nil {
			bkref.Release(context.WithoutCancel(ctx))
		}
	}()
	err = MountRef(ctx, bkref, bkSessionGroup, func(out string, _ *mount.Mount) error {
		err = bk.ContainerImageToTarball(ctx, engineHostPlatform.Spec(), filepath.Join(out, filePath), inputByPlatform, useOCIMediaTypes(mediaTypes), string(forcedCompression))
		if err != nil {
			return fmt.Errorf("container image to tarball file conversion failed: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("container image to tarball file conversion failed: %w", err)
	}

	f = NewFile(nil, filePath, query.Platform(), nil)
	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	f.Result = snap
	return f, nil
}

type ExportOpts struct {
	Dest              string
	PlatformVariants  []*Container
	ForcedCompression ImageLayerCompression
	MediaTypes        ImageMediaTypes
	Tar               bool
	LeaseID           string
}

func useOCIMediaTypes(mediaTypes ImageMediaTypes) bool {
	if mediaTypes == "" {
		// Modern registry implementations support oci types and docker daemons
		// have been capable of pulling them since 2018:
		// https://github.com/moby/moby/pull/37359
		// So they are a safe default.
		mediaTypes = OCIMediaTypes
	}
	return mediaTypes == OCIMediaTypes
}

func filterEmptyContainers(containers []*Container) []*Container {
	var l []*Container
	for _, c := range containers {
		if c.FS == nil {
			continue
		}
		rootFS := c.FS.Self()
		if rootFS == nil {
			continue
		}
		l = append(l, c)
	}
	return l
}

func getVariantRefs(ctx context.Context, variants []*Container) (map[string]buildkit.ContainerExport, error) {
	inputByPlatform := map[string]buildkit.ContainerExport{}
	var eg errgroup.Group
	var mu sync.Mutex
	for _, variant := range variants {
		if variant.FS == nil {
			continue
		}
		rootFS := variant.FS.Self()
		if rootFS == nil {
			continue
		}

		platformString := variant.Platform.Format()
		if _, ok := inputByPlatform[platformString]; ok {
			return nil, fmt.Errorf("duplicate platform %q", platformString)
		}

		eg.Go(func() error {
			fsRef, err := getRefOrEvaluate(ctx, rootFS)
			if err != nil {
				return err
			}

			mu.Lock()
			defer mu.Unlock()

			inputByPlatform[platformString] = buildkit.ContainerExport{
				Ref:         fsRef,
				Config:      variant.Config,
				Annotations: variant.Annotations,
			}
			return nil
		})
	}
	err := eg.Wait()
	if err != nil {
		return nil, err
	}
	if len(inputByPlatform) == 0 {
		// Could also just ignore and do nothing, airing on side of error until proven otherwise.
		return nil, errors.New("no containers to export")
	}
	return inputByPlatform, nil
}

func (container *Container) Export(ctx context.Context, opts ExportOpts) (*specs.Descriptor, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	variants := filterEmptyContainers(append([]*Container{container}, opts.PlatformVariants...))
	inputByPlatform, err := getVariantRefs(ctx, variants)
	if err != nil {
		return nil, err
	}

	resp, err := bk.ExportContainerImage(ctx, opts.Dest, inputByPlatform, string(opts.ForcedCompression), opts.Tar, opts.LeaseID, useOCIMediaTypes(opts.MediaTypes))
	if err != nil {
		return nil, err
	}

	encodedDesc, ok := resp[exptypes.ExporterImageDescriptorKey]
	if !ok {
		return nil, fmt.Errorf("exporter response missing %s", exptypes.ExporterImageDescriptorKey)
	}
	rawDesc, err := base64.StdEncoding.DecodeString(encodedDesc)
	if err != nil {
		return nil, fmt.Errorf("failed decoding descriptor: %w", err)
	}

	var desc specs.Descriptor
	err = json.Unmarshal(rawDesc, &desc)
	if err != nil {
		return nil, fmt.Errorf("failed decoding descriptor: %w", err)
	}
	return &desc, nil
}

func (container *Container) Import(
	ctx context.Context,
	tarball io.Reader,
	tag string,
) (*Container, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	store := query.OCIStore()
	lm := query.LeaseManager()

	container = container.Clone()

	var release func(context.Context) error
	loadManifest := func(ctx context.Context) (*specs.Descriptor, error) {
		// override outer ctx with release ctx and set release
		ctx, release, err = leaseutil.WithLease(ctx, lm, leaseutil.MakeTemporary)
		if err != nil {
			return nil, err
		}

		stream := archive.NewImageImportStream(tarball, "")

		desc, err := stream.Import(ctx, store)
		if err != nil {
			return nil, fmt.Errorf("image archive import: %w", err)
		}

		return resolveIndex(ctx, store, desc, container.Platform.Spec(), tag)
	}
	defer func() {
		if release != nil {
			release(ctx)
		}
	}()

	manifestDesc, err := loadManifest(ctx)
	if err != nil {
		return nil, fmt.Errorf("recover: %w", err)
	}

	return container.FromInternal(ctx, *manifestDesc)
}

// FromInternal creates a Container from an OCI image descriptor, loading the
// image directly from the main worker OCI store.
func (container *Container) FromInternal(
	ctx context.Context,
	desc specs.Descriptor,
) (*Container, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}

	// NB: the repository portion of this ref doesn't actually matter, but it's
	// pleasant to see something recognizable.
	dummyRepo := "dagger/import"

	st := llb.OCILayout(
		fmt.Sprintf("%s@%s", dummyRepo, desc.Digest),
		llb.OCIStore("", buildkit.OCIStoreName),
		llb.Platform(container.Platform.Spec()),
		buildkit.WithTracePropagation(ctx),
	)

	execDef, err := st.Marshal(ctx, llb.Platform(container.Platform.Spec()))
	if err != nil {
		return nil, fmt.Errorf("marshal root: %w", err)
	}

	container = container.Clone()
	rootfsDir := NewDirectory(execDef.ToPB(), "/", container.Platform, container.Services)
	container.FS, err = UpdatedRootFS(ctx, rootfsDir)
	if err != nil {
		return nil, fmt.Errorf("updated rootfs: %w", err)
	}

	// eagerly evaluate the OCI reference so Buildkit sets up a long-term lease
	_, err = bk.Solve(ctx, bkgw.SolveRequest{
		Definition: container.FS.Self().LLB,
		Evaluate:   true,
	})
	if err != nil {
		return nil, fmt.Errorf("solve: %w", err)
	}

	manifestBlob, err := content.ReadBlob(ctx, query.OCIStore(), desc)
	if err != nil {
		return nil, fmt.Errorf("image archive read manifest blob: %w", err)
	}
	var man specs.Manifest
	err = json.Unmarshal(manifestBlob, &man)
	if err != nil {
		return nil, fmt.Errorf("image archive unmarshal manifest: %w", err)
	}
	configBlob, err := content.ReadBlob(ctx, query.OCIStore(), man.Config)
	if err != nil {
		return nil, fmt.Errorf("image archive read image config blob %s: %w", man.Config.Digest, err)
	}
	var imgSpec specs.Image
	err = json.Unmarshal(configBlob, &imgSpec)
	if err != nil {
		return nil, fmt.Errorf("load image config: %w", err)
	}
	container.Config = imgSpec.Config

	return container, nil
}

func (container *Container) WithExposedPort(port Port) (*Container, error) {
	container = container.Clone()

	// replace existing port to avoid duplicates
	gotOne := false

	for i, p := range container.Ports {
		if p.Port == port.Port && p.Protocol == port.Protocol {
			container.Ports[i] = port
			gotOne = true
			break
		}
	}

	if !gotOne {
		container.Ports = append(container.Ports, port)
	}

	if container.Config.ExposedPorts == nil {
		container.Config.ExposedPorts = map[string]struct{}{}
	}

	ociPort := fmt.Sprintf("%d/%s", port.Port, port.Protocol.Network())
	container.Config.ExposedPorts[ociPort] = struct{}{}

	return container, nil
}

func (container *Container) WithoutExposedPort(port int, protocol NetworkProtocol) (*Container, error) {
	container = container.Clone()

	filtered := []Port{}
	filteredOCI := map[string]struct{}{}
	for _, p := range container.Ports {
		if p.Port != port || p.Protocol != protocol {
			filtered = append(filtered, p)
			ociPort := fmt.Sprintf("%d/%s", p.Port, p.Protocol.Network())
			filteredOCI[ociPort] = struct{}{}
		}
	}

	container.Ports = filtered
	container.Config.ExposedPorts = filteredOCI

	return container, nil
}

func (container *Container) WithServiceBinding(ctx context.Context, svc dagql.ObjectResult[*Service], alias string) (*Container, error) {
	container = container.Clone()

	host, err := svc.Self().Hostname(ctx, svc.ID())
	if err != nil {
		return nil, err
	}

	var aliases AliasSet
	if alias != "" {
		aliases = AliasSet{alias}
	}

	container.Services.Merge(ServiceBindings{
		{
			Service:  svc,
			Hostname: host,
			Aliases:  aliases,
		},
	})

	return container, nil
}

func (container *Container) ImageRefOrErr(ctx context.Context) (string, error) {
	imgRef := container.ImageRef
	if imgRef != "" {
		return imgRef, nil
	}

	return "", errors.Errorf("Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
}

type ContainerAsServiceArgs struct {
	// Command to run instead of the container's default command
	Args []string `default:"[]"`

	// If the container has an entrypoint, prepend it to this exec's args
	UseEntrypoint bool `default:"false"`

	// Provide the executed command access back to the Dagger API
	ExperimentalPrivilegedNesting bool `default:"false"`

	// Grant the process all root capabilities
	InsecureRootCapabilities bool `default:"false"`

	// Expand the environment variables in args
	Expand bool `default:"false"`

	// Skip the init process injected into containers by default so that the
	// user's process is PID 1
	NoInit bool `default:"false"`
}

func (container *Container) AsService(ctx context.Context, args ContainerAsServiceArgs) (*Service, error) {
	if len(args.Args) == 0 &&
		len(container.Config.Cmd) == 0 &&
		len(container.Config.Entrypoint) == 0 {
		return nil, ErrNoSvcCommand
	}

	useEntrypoint := args.UseEntrypoint
	if len(container.Config.Entrypoint) > 0 && !container.DefaultArgs {
		useEntrypoint = true
	}

	var cmdargs = container.Config.Cmd
	if len(args.Args) > 0 {
		cmdargs = args.Args
		if !args.UseEntrypoint {
			useEntrypoint = false
		}
	}

	if len(container.Config.Entrypoint) > 0 && useEntrypoint {
		cmdargs = append(container.Config.Entrypoint, cmdargs...)
	}

	return &Service{
		Creator:                       trace.SpanContextFromContext(ctx),
		Container:                     container,
		Args:                          cmdargs,
		ExperimentalPrivilegedNesting: args.ExperimentalPrivilegedNesting,
		InsecureRootCapabilities:      args.InsecureRootCapabilities,
		NoInit:                        args.NoInit,
	}, nil
}

func (container *Container) AsRecoveredService(ctx context.Context, richErr *buildkit.RichError) (*Service, error) {
	return &Service{
		Creator:   trace.SpanContextFromContext(ctx),
		Container: container,
		ExecMeta:  richErr.Meta,
		ExecMD:    richErr.ExecMD,
	}, nil
}

func (container *Container) openFile(ctx context.Context, path string) (io.ReadCloser, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get server: %w", err)
	}

	mnt, mntSubpath, err := locatePath(container, path)
	if err != nil {
		return nil, fmt.Errorf("failed to locate path %s: %w", path, err)
	}

	var fileSource *dagql.ObjectResult[*File]
	var directorySource *dagql.ObjectResult[*Directory]

	switch {
	case mnt == nil: // rootfs
		directorySource = container.FS

	case mnt.DirectorySource != nil: // directory mount
		directorySource = mnt.DirectorySource

	case mnt.FileSource != nil: // file mount
		fileSource = mnt.FileSource

	default:
		return nil, fmt.Errorf("invalid mount source for %s", path)
	}

	if fileSource == nil {
		fileSource = &dagql.ObjectResult[*File]{}
		err = srv.Select(ctx, directorySource, fileSource, dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(mntSubpath)},
			},
		})
		if err != nil {
			return nil, err
		}
	}

	return fileSource.Self().Open(ctx)
}

func (container *Container) ownership(ctx context.Context, owner string) (*Ownership, error) {
	if owner == "" {
		// do not change ownership
		return nil, nil
	}

	uidOrName, gidOrName, hasGroup := strings.Cut(owner, ":")

	var uid, gid int
	var uname, gname string

	uid, err := parseUID(uidOrName)
	if err != nil {
		uname = uidOrName
	}

	if hasGroup {
		gid, err = parseUID(gidOrName)
		if err != nil {
			gname = gidOrName
		}
	}

	if uname != "" {
		f, err := container.openFile(ctx, "/etc/passwd")
		if err != nil {
			return nil, fmt.Errorf("open /etc/passwd: %w", err)
		}
		defer f.Close()
		uid, err = findUID(f, uname)
		if err != nil {
			return nil, fmt.Errorf("find uid: %w", err)
		}
	}

	if gname != "" {
		f, err := container.openFile(ctx, "/etc/group")
		if err != nil {
			return nil, fmt.Errorf("open /etc/passwd: %w", err)
		}
		defer f.Close()
		gid, err = findGID(f, gname)
		if err != nil {
			return nil, fmt.Errorf("find gid: %w", err)
		}
	}

	if !hasGroup {
		gid = uid
	}

	return &Ownership{uid, gid}, nil
}

func (container *Container) command(opts ContainerExecOpts) ([]string, error) {
	cfg := container.Config
	args := opts.Args

	if len(args) == 0 {
		// we use the default args if no new default args are passed
		args = cfg.Cmd
	}

	if len(cfg.Entrypoint) > 0 && opts.UseEntrypoint {
		args = append(cfg.Entrypoint, args...)
	}

	if len(args) == 0 {
		return nil, ErrNoCommand
	}

	return args, nil
}

type BuildArg struct {
	Name  string `field:"true" doc:"The build argument name."`
	Value string `field:"true" doc:"The build argument value."`
}

func (BuildArg) TypeName() string {
	return "BuildArg"
}

func (BuildArg) TypeDescription() string {
	return "Key value object that represents a build argument."
}

// OCI manifest annotation that specifies an image's tag
const ociTagAnnotation = "org.opencontainers.image.ref.name"

func ResolveIndex(ctx context.Context, store content.Store, desc specs.Descriptor, platform specs.Platform, tag string) (*specs.Descriptor, error) {
	return resolveIndex(ctx, store, desc, platform, tag)
}

func resolveIndex(ctx context.Context, store content.Store, desc specs.Descriptor, platform specs.Platform, tag string) (*specs.Descriptor, error) {
	if desc.MediaType != specs.MediaTypeImageIndex {
		return nil, fmt.Errorf("expected index, got %s", desc.MediaType)
	}

	indexBlob, err := content.ReadBlob(ctx, store, desc)
	if err != nil {
		return nil, fmt.Errorf("read index blob: %w", err)
	}

	var idx specs.Index
	err = json.Unmarshal(indexBlob, &idx)
	if err != nil {
		return nil, fmt.Errorf("unmarshal index: %w", err)
	}

	matcher := platforms.Only(platform)

	for _, m := range idx.Manifests {
		if m.Platform != nil {
			if !matcher.Match(*m.Platform) {
				// incompatible
				continue
			}
		}

		if tag != "" {
			if m.Annotations == nil {
				continue
			}

			manifestTag, found := m.Annotations[ociTagAnnotation]
			if !found || manifestTag != tag {
				continue
			}
		}

		switch m.MediaType {
		case specs.MediaTypeImageManifest, // OCI
			images.MediaTypeDockerSchema2Manifest: // Docker
			return &m, nil

		case specs.MediaTypeImageIndex, // OCI
			images.MediaTypeDockerSchema2ManifestList: // Docker
			return resolveIndex(ctx, store, m, platform, tag)

		default:
			return nil, fmt.Errorf("expected manifest or index, got %s", m.MediaType)
		}
	}

	return nil, fmt.Errorf("no manifest for platform %s and tag %s", platforms.Format(platform), tag)
}

type ImageLayerCompression string

var ImageLayerCompressions = dagql.NewEnum[ImageLayerCompression]()

var (
	CompressionGzip         = ImageLayerCompressions.Register("Gzip")
	_                       = ImageLayerCompressions.AliasView("GZIP", "Gzip", enumView)
	CompressionZstd         = ImageLayerCompressions.Register("Zstd")
	_                       = ImageLayerCompressions.AliasView("ZSTD", "Zstd", enumView)
	CompressionEStarGZ      = ImageLayerCompressions.Register("EStarGZ")
	_                       = ImageLayerCompressions.AliasView("ESTARGZ", "EStarGZ", enumView)
	CompressionUncompressed = ImageLayerCompressions.Register("Uncompressed")
	_                       = ImageLayerCompressions.AliasView("UNCOMPRESSED", "Uncompressed", enumView)
)

func (proto ImageLayerCompression) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ImageLayerCompression",
		NonNull:   true,
	}
}

func (proto ImageLayerCompression) TypeDescription() string {
	return "Compression algorithm to use for image layers."
}

func (proto ImageLayerCompression) Decoder() dagql.InputDecoder {
	return ImageLayerCompressions
}

func (proto ImageLayerCompression) ToLiteral() call.Literal {
	return ImageLayerCompressions.Literal(proto)
}

type ImageMediaTypes string

var ImageMediaTypesEnum = dagql.NewEnum[ImageMediaTypes]()

var (
	OCIMediaTypes    = ImageMediaTypesEnum.Register("OCIMediaTypes")
	_                = ImageMediaTypesEnum.AliasView("OCI", "OCIMediaTypes", enumView)
	DockerMediaTypes = ImageMediaTypesEnum.Register("DockerMediaTypes")
	_                = ImageMediaTypesEnum.AliasView("DOCKER", "DockerMediaTypes", enumView)
)

func (proto ImageMediaTypes) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ImageMediaTypes",
		NonNull:   true,
	}
}

func (proto ImageMediaTypes) TypeDescription() string {
	return "Mediatypes to use in published or exported image metadata."
}

func (proto ImageMediaTypes) Decoder() dagql.InputDecoder {
	return ImageMediaTypesEnum
}

func (proto ImageMediaTypes) ToLiteral() call.Literal {
	return ImageMediaTypesEnum.Literal(proto)
}

type ReturnTypes string

var ReturnTypesEnum = dagql.NewEnum[ReturnTypes]()

var (
	ReturnSuccess = ReturnTypesEnum.Register("SUCCESS",
		`A successful execution (exit code 0)`,
	)
	ReturnFailure = ReturnTypesEnum.Register("FAILURE",
		`A failed execution (exit codes 1-127)`,
	)
	ReturnAny = ReturnTypesEnum.Register("ANY",
		`Any execution (exit codes 0-127)`,
	)
)

func (expect ReturnTypes) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ReturnType",
		NonNull:   true,
	}
}

func (expect ReturnTypes) TypeDescription() string {
	return "Expected return type of an execution"
}

func (expect ReturnTypes) Decoder() dagql.InputDecoder {
	return ReturnTypesEnum
}

func (expect ReturnTypes) ToLiteral() call.Literal {
	return ReturnTypesEnum.Literal(expect)
}

// ReturnCodes gets the valid exit codes allowed for a specific return status
//
// NOTE: exit status codes above 128 are likely from exiting via a signal - we
// shouldn't try and handle these.
func (expect ReturnTypes) ReturnCodes() []int {
	switch expect {
	case ReturnSuccess:
		return []int{0}
	case ReturnFailure:
		codes := make([]int, 0, 128)
		for i := 1; i <= 128; i++ {
			codes = append(codes, i)
		}
		return codes
	case ReturnAny:
		codes := make([]int, 0, 129)
		for i := 0; i <= 128; i++ {
			codes = append(codes, i)
		}
		return codes
	default:
		return nil
	}
}

type TerminalLegacy struct{}

func (*TerminalLegacy) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Terminal",
		NonNull:   true,
	}
}

func (*TerminalLegacy) TypeDescription() string {
	return "An interactive terminal that clients can connect to."
}

func (*TerminalLegacy) Evaluate(ctx context.Context) (*buildkit.Result, error) {
	return nil, nil
}

// UpdatedRootFS returns an updated rootfs for a given directory after an exec/import/etc.
// The returned ObjectResult uses the ID of the current operation.
func UpdatedRootFS(
	ctx context.Context,
	dir *Directory,
) (*dagql.ObjectResult[*Directory], error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	curSrv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, err
	}
	curID := dagql.CurrentID(ctx)
	view := curID.View()
	objType, ok := curSrv.ObjectType("Container")
	if !ok {
		return nil, fmt.Errorf("object type Container not found in server")
	}
	fieldSpec, ok := objType.FieldSpec("rootfs", view)
	if !ok {
		return nil, fmt.Errorf("field spec for rootfs not found in object type Container")
	}
	astType := fieldSpec.Type.Type()
	rootfsID := curID.Append(
		astType, "rootfs",
		call.WithView(view),
		call.WithModule(fieldSpec.Module))
	updatedRootfs, err := dagql.NewObjectResultForID(dir, curSrv, rootfsID)
	if err != nil {
		return nil, err
	}
	return &updatedRootfs, nil
}

// updatedDirMount returns an updated mount for a given directory after an exec/import/etc.
// The returned ObjectResult uses the ID of the current operation.
//
//nolint:dupl
func updatedDirMount(
	ctx context.Context,
	dir *Directory,
	mntTarget string,
) (*dagql.ObjectResult[*Directory], error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	curSrv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, err
	}
	curID := dagql.CurrentID(ctx)
	view := curID.View()
	objType, ok := curSrv.ObjectType("Container")
	if !ok {
		return nil, fmt.Errorf("object type Container not found in server")
	}
	fieldSpec, ok := objType.FieldSpec("directory", view)
	if !ok {
		return nil, fmt.Errorf("field spec for directory not found in object type Container")
	}
	astType := fieldSpec.Type.Type()
	dirIDPathArg := call.NewArgument("path", call.NewLiteralString(mntTarget), false)
	dirID := curID.Append(
		astType, "directory",
		call.WithView(view),
		call.WithModule(fieldSpec.Module),
		call.WithArgs(dirIDPathArg))
	updatedDirMnt, err := dagql.NewObjectResultForID(dir, curSrv, dirID)
	if err != nil {
		return nil, err
	}
	return &updatedDirMnt, nil
}

// updatedFileMount returns an updated mount for a given file after an exec/import/etc.
// The returned ObjectResult uses the ID of the current operation.
//
//nolint:dupl
func updatedFileMount(
	ctx context.Context,
	file *File,
	mntTarget string,
) (*dagql.ObjectResult[*File], error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	curSrv, err := query.Server.Server(ctx)
	if err != nil {
		return nil, err
	}
	curID := dagql.CurrentID(ctx)
	view := curID.View()
	objType, ok := curSrv.ObjectType("Container")
	if !ok {
		return nil, fmt.Errorf("object type Container not found in server")
	}
	fieldSpec, ok := objType.FieldSpec("file", view)
	if !ok {
		return nil, fmt.Errorf("field spec for file not found in object type Container")
	}
	astType := fieldSpec.Type.Type()
	fileIDPathArg := call.NewArgument("path", call.NewLiteralString(mntTarget), false)
	fileID := curID.Append(
		astType, "file",
		call.WithView(view),
		call.WithModule(fieldSpec.Module),
		call.WithArgs(fileIDPathArg))
	updatedFileMnt, err := dagql.NewObjectResultForID(file, curSrv, fileID)
	if err != nil {
		return nil, err
	}
	return &updatedFileMnt, nil
}
