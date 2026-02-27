package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"dagger.io/dagger/telemetry"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/platforms"
	"github.com/dagger/dagger/core/containersource"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/exporter/containerimage/exptypes"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/util/containerutil"
	"github.com/distribution/reference"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"
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
	Config dockerspec.DockerOCIImageConfig

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

	Parent dagql.ObjectResult[*Container]
	LazyState

	// OpID is the operation call ID that should be used as the base when
	// synthesizing updated container child selection IDs (e.g. rootfs, file,
	// directory) after operations like exec/import.
	OpID *call.ID
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

func NewContainer(platform Platform) *Container {
	return &Container{
		Platform:  platform,
		LazyState: NewLazyState(),
	}
}

func NewContainerChild(parent dagql.ObjectResult[*Container]) *Container {
	if parent.Self() == nil {
		return &Container{
			Parent:    parent,
			LazyState: NewLazyState(),
		}
	}

	cp := *parent.Self()
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
	cp.Parent = parent
	cp.LazyState = NewLazyState()
	cp.OpID = nil
	return &cp
}

var _ dagql.OnReleaser = (*Container)(nil)

func (container *Container) Evaluate(ctx context.Context) error {
	if container == nil {
		return nil
	}
	return container.LazyState.Evaluate(ctx, "Container")
}

func (container *Container) Sync(ctx context.Context) error {
	if err := container.Evaluate(ctx); err != nil {
		return err
	}
	if container == nil {
		return nil
	}
	if container.FS != nil && container.FS.Self() != nil {
		if err := container.FS.Self().Evaluate(ctx); err != nil {
			return err
		}
	}
	if container.Meta != nil {
		if err := container.Meta.Evaluate(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (container *Container) OnRelease(ctx context.Context) error {
	if container == nil {
		return nil
	}
	/* TODO: we don't do this anymore because the dagql cache should know our deps and handle their releases as needed
	  Leaving for the moment as a reference and reminder that we gotta make the dagql cache do that


	// this might be problematic if directories overlap, could release same result multiple times
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
	*/
	return nil
}

// Ownership contains a UID/GID pair resolved from a user/group name or ID pair
// provided via the API. It primarily exists to distinguish an unspecified
// ownership from UID/GID 0 (root) ownership.
type Ownership struct {
	UID int
	GID int
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
	Base *dagql.ObjectResult[*Directory]

	// The ID of the cache mount
	ID string

	// The sharing mode of the cache mount
	SharingMode CacheSharingMode
}

type TmpfsMountSource struct {
	// Configure the size of the mounted tmpfs in bytes
	Size int
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

// FromCanonicalRef returns a lazy initializer for digest-addressed image pulls.
// It updates only rootfs snapshot state.
func (container *Container) FromCanonicalRef(
	ctx context.Context,
	refName reference.Canonical,
) (LazyInitFunc, error) {
	return func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}

		platform := container.Platform
		refStr := refName.String()

		bk, err := query.Buildkit(ctx)
		if err != nil {
			return fmt.Errorf("failed to get buildkit client: %w", err)
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
			return err
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
			return err
		}

		src, err := hsm.Resolve(ctx, id, query.BuildkitSession())
		if err != nil {
			return err
		}

		ref, err := src.Snapshot(ctx, nil)
		if err != nil {
			return err
		}

		if container.FS == nil || container.FS.Self() == nil {
			return fmt.Errorf("missing rootfs directory for fromCanonicalRef")
		}
		rootfsDir := container.FS.Self()
		if rootfsDir.Dir == "" {
			rootfsDir.Dir = "/"
		} else if rootfsDir.Dir != "/" {
			return fmt.Errorf("SetFSFromCanonicalRef got %s as dir; however it will be lost", rootfsDir.Dir)
		}
		rootfsDir.setSnapshot(ref)

		return nil
	}, nil
}

/*
TODO: re-implement Dockerfile.

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
	sshSocket *Socket,
) (*Container, error) {
	container = container.Clone()

	secretNameToLLBID := make(map[string]string)
	for _, secret := range secrets {
		secretDgst := SecretIDDigest(secret.ID())
		secretName, ok := secretStore.GetSecretName(secretDgst)
		if !ok {
			return nil, fmt.Errorf("secret not found: %s", secretDgst)
		}
		container.Secrets = append(container.Secrets, ContainerSecret{
			Secret:    secret,
			MountPath: fmt.Sprintf("/run/secrets/%s", secretName),
		})
		secretNameToLLBID[secretName] = secretDgst.String()
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

	if sshSocket != nil {
		solveCtx = buildkit.WithSSHTranslator(solveCtx, func(id string, optional bool) (string, error) {
			return sshSocket.LLBID(), nil
		})
	}

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
		var imgSpec dockerspec.DockerOCIImage
		if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
			return nil, err
		}

		container.Config = mergeImageConfig(container.Config, imgSpec.Config)
	}

	return container, nil
}
*/

func (container *Container) RootFS(ctx context.Context) (*Directory, error) {
	if container.FS != nil {
		return container.FS.Self(), nil
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dagql server: %w", err)
	}

	var rootfs dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, srv.Root(), &rootfs, dagql.Selector{
		Field: "directory",
	}); err != nil {
		return nil, err
	}

	return rootfs.Self(), nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithRootFS(ctx context.Context, dir dagql.ObjectResult[*Directory]) (*Container, error) {
	container.FS = &dir

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithDirectory(
	ctx context.Context,
	subdir string,
	src dagql.ObjectResult[*Directory],
	filter CopyFilter,
	owner string,
) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithFile(
	ctx context.Context,
	srv *dagql.Server,
	destPath string,
	src dagql.ObjectResult[*File],
	permissions *int,
	owner string,
) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutPaths(ctx context.Context, srv *dagql.Server, destPaths ...string) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithFiles(
	ctx context.Context,
	srv *dagql.Server,
	destDir string,
	src []dagql.ObjectResult[*File],
	permissions *int,
	owner string,
) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithNewFile(
	ctx context.Context,
	dest string,
	content []byte,
	permissions fs.FileMode,
	owner string,
) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithSymlink(ctx context.Context, srv *dagql.Server, target, linkPath string) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedDirectory(
	ctx context.Context,
	target string,
	dir dagql.ObjectResult[*Directory],
	owner string,
	readonly bool,
) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedFile(
	ctx context.Context,
	target string,
	file dagql.ObjectResult[*File],
	owner string,
	readonly bool,
) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedCache(
	ctx context.Context,
	target string,
	cache *CacheVolume,
	source dagql.ObjectResult[*Directory],
	sharingMode CacheSharingMode,
	owner string,
) (*Container, error) {
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

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

	if owner != "" {
		if source.Self() == nil {
			// create a scratch directory for chownDir to operate on
			err = srv.Select(ctx, srv.Root(), &source,
				dagql.Selector{
					Field: "directory",
				},
			)
			if err != nil {
				return nil, err
			}
		}
		var err error
		source, err = container.chownDir(ctx, source, owner)
		if err != nil {
			return nil, err
		}
	}
	mount.CacheSource.Base = &source
	container.Mounts = container.Mounts.With(mount)

	// set image ref to empty string
	container.ImageRef = ""

	SeenCacheKeys.Store(cache.Keys[0], struct{}{})

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedTemp(ctx context.Context, target string, size int) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithMountedSecret(
	ctx context.Context,
	target string,
	source dagql.ObjectResult[*Secret],
	owner string,
	mode fs.FileMode,
) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutMount(ctx context.Context, target string) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithUnixSocket(ctx context.Context, target string, source *Socket, owner string) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutUnixSocket(ctx context.Context, target string) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithSecretVariable(
	ctx context.Context,
	name string,
	secret dagql.ObjectResult[*Secret],
) (*Container, error) {
	container.Secrets = append(container.Secrets, ContainerSecret{
		Secret:  secret,
		EnvName: name,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutSecretVariable(ctx context.Context, name string) (*Container, error) {
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

func (container *Container) Directory(ctx context.Context, dirPath string) (dagql.ObjectResult[*Directory], error) {
	var dir dagql.ObjectResult[*Directory]

	mnt, subpath, err := locatePath(container, dirPath)
	if err != nil {
		return dir, err
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return dir, fmt.Errorf("failed to get dagql server: %w", err)
	}

	directorySelector := dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(subpath)},
		},
	}

	switch {
	case mnt == nil: // rootfs
		rootfs := container.FS
		if container.FS == nil {
			var scratchRootfs dagql.ObjectResult[*Directory]
			err = srv.Select(ctx, srv.Root(), &scratchRootfs, dagql.Selector{
				Field: "directory",
			})
			if err != nil {
				return dir, err
			}
			rootfs = &scratchRootfs
		}
		if subpath == "" || subpath == "." {
			return *rootfs, nil
		}
		err = srv.Select(ctx, *rootfs, &dir, directorySelector)
	case mnt.DirectorySource != nil: // mounted directory
		if subpath == "" || subpath == "." {
			return *mnt.DirectorySource, nil
		}
		err = srv.Select(ctx, *mnt.DirectorySource, &dir, directorySelector)
	case mnt.FileSource != nil: // mounted file
		return dir, fmt.Errorf("path %s is a file, not a directory", dirPath)
	default:
		return dir, fmt.Errorf("invalid path %s in container mounts", dirPath)
	}

	switch {
	case err == nil:
		return dir, nil
	case errors.As(err, &notADirectoryError{}):
		// fix the error message to use dirPath rather than subpath
		return dir, notADirectoryError{fmt.Errorf("path %s is a file, not a directory", dirPath)}
	default:
		return dir, err
	}
}

func (container *Container) File(ctx context.Context, filePath string) (dagql.ObjectResult[*File], error) {
	var f dagql.ObjectResult[*File]

	mnt, subpath, err := locatePath(container, filePath)
	if err != nil {
		return f, err
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return f, fmt.Errorf("failed to get dagql server: %w", err)
	}

	fileSelector := dagql.Selector{
		Field: "file",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(subpath)},
		},
	}

	switch {
	case mnt == nil: // rootfs
		rootfs := container.FS
		if rootfs == nil {
			var scratchRootfs dagql.ObjectResult[*Directory]
			err = srv.Select(ctx, srv.Root(), &scratchRootfs, dagql.Selector{
				Field: "directory",
			})
			if err != nil {
				return f, err
			}
			rootfs = &scratchRootfs
		}
		err = srv.Select(ctx, *rootfs, &f, fileSelector)
	case mnt.DirectorySource != nil: // mounted directory
		err = srv.Select(ctx, *mnt.DirectorySource, &f, fileSelector)
		err = RestoreErrPath(err, filePath) // preserve the full filePath, rather than subpath
	case mnt.FileSource != nil: // mounted file
		return *mnt.FileSource, nil
	default:
		return f, fmt.Errorf("invalid path %s in container mounts", filePath)
	}

	switch {
	case err == nil:
		return f, nil
	case errors.As(err, &notAFileError{}):
		// fix the error message to use filePath rather than subpath
		return f, notAFileError{fmt.Errorf("path %s is a directory, not a file", filePath)}
	default:
		return f, err
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

func (container *Container) ImageConfig(ctx context.Context) (dockerspec.DockerOCIImageConfig, error) {
	return container.Config, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) UpdateImageConfig(ctx context.Context, updateFn func(dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig) (*Container, error) {
	container.Config = updateFn(container.Config)
	return container, nil
}

type ContainerGPUOpts struct {
	Devices []string
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithGPU(ctx context.Context, gpuOpts ContainerGPUOpts) (*Container, error) {
	container.EnabledGPUs = gpuOpts.Devices
	return container, nil
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
		rootfs := container.FS
		if rootfs == nil {
			var scratchRootfs dagql.ObjectResult[*Directory]
			err = srv.Select(ctx, srv.Root(), &scratchRootfs, dagql.Selector{
				Field: "directory",
			})
			if err != nil {
				return false, err
			}
			rootfs = &scratchRootfs
		}
		err = srv.Select(ctx, *rootfs, &exists, dagql.Selector{
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
		rootfs := container.FS
		if rootfs == nil {
			var scratchRootfs dagql.ObjectResult[*Directory]
			err = srv.Select(ctx, srv.Root(), &scratchRootfs, dagql.Selector{
				Field: "directory",
			})
			if err != nil {
				return nil, err
			}
			rootfs = &scratchRootfs
		}
		err = srv.Select(ctx, *rootfs, &stat, dagql.Selector{
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithAnnotation(ctx context.Context, key, value string) (*Container, error) {
	container.Annotations = append(container.Annotations, containerutil.ContainerAnnotation{
		Key:   key,
		Value: value,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutAnnotation(ctx context.Context, name string) (*Container, error) {
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

	bkref, err := query.BuildkitCache().New(ctx, nil, nil,
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
	err = MountRef(ctx, bkref, nil, func(out string, _ *mount.Mount) error {
		err = bk.ContainerImageToTarball(ctx, engineHostPlatform.Spec(), filepath.Join(out, filePath), inputByPlatform, useOCIMediaTypes(mediaTypes), string(forcedCompression))
		if err != nil {
			return fmt.Errorf("container image to tarball file conversion failed: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("container image to tarball file conversion failed: %w", err)
	}

	snap, err := bkref.Commit(ctx)
	if err != nil {
		return nil, err
	}
	bkref = nil
	f = &File{
		File:     filePath,
		Platform: query.Platform(),
		Snapshot: snap,
	}
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
			fsRef, err := rootFS.getSnapshot(ctx)
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

/*
TODO: re-implement import/fromInternal.

func (container *Container) Import(
	ctx context.Context,
	tarball io.Reader,
	tag string,
) (*Container, error) {
	...
}

// FromInternal creates a Container from an OCI image descriptor, loading the
// image directly from the main worker OCI store.
func (container *Container) FromInternal(
	ctx context.Context,
	desc specs.Descriptor,
) (*Container, error) {
	...
}
*/

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithExposedPort(port Port) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithoutExposedPort(port int, protocol NetworkProtocol) (*Container, error) {
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

// mutates container caller must have handled cloning or creating a new child.
func (container *Container) WithServiceBinding(ctx context.Context, svc dagql.ObjectResult[*Service], alias string) (*Container, error) {
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
		`A failed execution (exit codes 1-127 and 192-255)`,
	)
	ReturnAny = ReturnTypesEnum.Register("ANY",
		`Any execution (exit codes 0-127 and 192-255)`,
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
// NOTE: exit status codes 128-191 are likely from exiting via a signal - we
// shouldn't try and handle these. Codes 192-255 are safe to handle to support
// tools that return exit codes >127, such as AWS CLI.
func (expect ReturnTypes) ReturnCodes() []int {
	switch expect {
	case ReturnSuccess:
		return []int{0}
	case ReturnFailure:
		codes := make([]int, 0, 127+64)
		for i := 1; i <= 127; i++ {
			codes = append(codes, i)
		}
		for i := 192; i <= 255; i++ {
			codes = append(codes, i)
		}
		return codes
	case ReturnAny:
		codes := make([]int, 0, 128+64)
		for i := 0; i <= 127; i++ {
			codes = append(codes, i)
		}
		for i := 192; i <= 255; i++ {
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

func (*TerminalLegacy) Evaluate(ctx context.Context) error {
	return nil
}

func (terminal *TerminalLegacy) Sync(ctx context.Context) error {
	return terminal.Evaluate(ctx)
}

// UpdatedRootFS returns an updated rootfs for a given directory after an exec/import/etc.
// The returned ObjectResult uses owner.OpID as the operation ID base.
func UpdatedRootFS(
	ctx context.Context,
	owner *Container,
	dir *Directory,
) (*dagql.ObjectResult[*Directory], error) {
	updatedRootfs, err := updatedContainerSelectionResult(ctx, owner, dir, "rootfs", nil)
	if err != nil {
		return nil, err
	}
	return &updatedRootfs, nil
}

// updatedDirMount returns an updated mount for a given directory after an exec/import/etc.
// The returned ObjectResult uses owner.OpID as the operation ID base.
func updatedDirMount(
	ctx context.Context,
	owner *Container,
	dir *Directory,
	mntTarget string,
) (*dagql.ObjectResult[*Directory], error) {
	dirIDPathArg := call.NewArgument("path", call.NewLiteralString(mntTarget), false)
	updatedDirMnt, err := updatedContainerSelectionResult(ctx, owner, dir, "directory", []*call.Argument{dirIDPathArg})
	if err != nil {
		return nil, err
	}
	return &updatedDirMnt, nil
}

// updatedFileMount returns an updated mount for a given file after an exec/import/etc.
// The returned ObjectResult uses owner.OpID as the operation ID base.
func updatedFileMount(
	ctx context.Context,
	owner *Container,
	file *File,
	mntTarget string,
) (*dagql.ObjectResult[*File], error) {
	fileIDPathArg := call.NewArgument("path", call.NewLiteralString(mntTarget), false)
	updatedFileMnt, err := updatedContainerSelectionResult(ctx, owner, file, "file", []*call.Argument{fileIDPathArg})
	if err != nil {
		return nil, err
	}
	return &updatedFileMnt, nil
}

func updatedContainerSelectionResult[T dagql.Typed](
	ctx context.Context,
	owner *Container,
	val T,
	fieldName string,
	args []*call.Argument,
) (dagql.ObjectResult[T], error) {
	if owner == nil {
		return dagql.ObjectResult[T]{}, fmt.Errorf("missing container owner for %s selection", fieldName)
	}
	if owner.OpID == nil {
		return dagql.ObjectResult[T]{}, fmt.Errorf("missing operation ID on container for %s selection", fieldName)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return dagql.ObjectResult[T]{}, err
	}
	curSrv, err := query.Server.Server(ctx)
	if err != nil {
		return dagql.ObjectResult[T]{}, err
	}
	view := owner.OpID.View()
	objType, ok := curSrv.ObjectType("Container")
	if !ok {
		return dagql.ObjectResult[T]{}, fmt.Errorf("object type Container not found in server")
	}
	fieldSpec, ok := objType.FieldSpec(fieldName, view)
	if !ok {
		return dagql.ObjectResult[T]{}, fmt.Errorf("field spec for %s not found in object type Container", fieldName)
	}
	newID := owner.OpID.Append(
		fieldSpec.Type.Type(),
		fieldName,
		call.WithView(view),
		call.WithArgs(args...),
	)
	inputArgs, err := dagql.ExtractIDArgs(fieldSpec.Args, newID)
	if err != nil {
		return dagql.ObjectResult[T]{}, fmt.Errorf("decode %s input args: %w", fieldName, err)
	}
	identityOpt, err := fieldSpec.IdentityOpt(ctx, inputArgs)
	if err != nil {
		return dagql.ObjectResult[T]{}, fmt.Errorf("resolve %s identity: %w", fieldName, err)
	}
	newID = newID.With(identityOpt)
	return dagql.NewObjectResultForID(val, curSrv, newID)
}
