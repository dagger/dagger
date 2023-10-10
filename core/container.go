package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/pkg/transfer/archive"
	"github.com/containerd/containerd/platforms"

	"github.com/docker/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/vito/progrock"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/core/socket"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
)

var ErrContainerNoExec = errors.New("no command has been executed")

// Container is a content-addressed container.
type Container struct {
	// The container's root filesystem.
	FS *pb.Definition `json:"fs"`

	// Image configuration (env, workdir, etc)
	Config specs.ImageConfig `json:"cfg"`

	// Pipeline
	Pipeline pipeline.Path `json:"pipeline"`

	// Mount points configured for the container.
	Mounts ContainerMounts `json:"mounts,omitempty"`

	// Meta is the /dagger filesystem. It will be null if nothing has run yet.
	Meta *pb.Definition `json:"meta,omitempty"`

	// The platform of the container's rootfs.
	Platform specs.Platform `json:"platform,omitempty"`

	// Secrets to expose to the container.
	Secrets []ContainerSecret `json:"secret_env,omitempty"`

	// Sockets to expose to the container.
	Sockets []ContainerSocket `json:"sockets,omitempty"`

	// Image reference
	ImageRef string `json:"image_ref,omitempty"`

	// Ports to expose from the container.
	Ports []Port `json:"ports,omitempty"`

	// Services to start before running the container.
	Services ServiceBindings `json:"services,omitempty"`

	// Focused indicates whether subsequent operations will be
	// focused, i.e. shown more prominently in the UI.
	Focused bool `json:"focused"`
}

func (container *Container) PBDefinitions() ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if container.FS != nil {
		defs = append(defs, container.FS)
	}
	for _, mnt := range container.Mounts {
		if mnt.Source != nil {
			defs = append(defs, mnt.Source)
		}
	}
	if container.Services != nil {
		for _, bnd := range container.Services {
			ctr := bnd.Service.Container
			if ctr == nil {
				continue
			}
			ctrDefs, err := ctr.PBDefinitions()
			if err != nil {
				return nil, err
			}
			defs = append(defs, ctrDefs...)
		}
	}
	return defs, nil
}

func NewContainer(id ContainerID, pipeline pipeline.Path, platform specs.Platform) (*Container, error) {
	container, err := id.Decode()
	if err != nil {
		return nil, err
	}

	container.Pipeline = pipeline.Copy()
	container.Platform = platform

	return container, nil
}

// Clone returns a deep copy of the container suitable for modifying in a
// WithXXX method.
func (container *Container) Clone() *Container {
	cp := *container
	cp.Config.ExposedPorts = cloneMap(cp.Config.ExposedPorts)
	cp.Config.Env = cloneSlice(cp.Config.Env)
	cp.Config.Entrypoint = cloneSlice(cp.Config.Entrypoint)
	cp.Config.Cmd = cloneSlice(cp.Config.Cmd)
	cp.Config.Volumes = cloneMap(cp.Config.Volumes)
	cp.Config.Labels = cloneMap(cp.Config.Labels)
	cp.Mounts = cloneSlice(cp.Mounts)
	cp.Secrets = cloneSlice(cp.Secrets)
	cp.Sockets = cloneSlice(cp.Sockets)
	cp.Ports = cloneSlice(cp.Ports)
	cp.Services = cloneSlice(cp.Services)
	cp.Pipeline = cloneSlice(cp.Pipeline)
	return &cp
}

// ID marshals the container into a content-addressed ID.
func (container *Container) ID() (ContainerID, error) {
	return resourceid.Encode(container)
}

var _ pipeline.Pipelineable = (*Container)(nil)

// PipelinePath returns the container's pipeline path.
func (container *Container) PipelinePath() pipeline.Path {
	return container.Pipeline
}

// Container is digestible so that it can be recorded as an output of the
// --debug vertex that created it.
var _ resourceid.Digestible = (*Container)(nil)

// Digest returns the container's content hash.
func (container *Container) Digest() (digest.Digest, error) {
	return stableDigest(container)
}

// Ownership contains a UID/GID pair resolved from a user/group name or ID pair
// provided via the API. It primarily exists to distinguish an unspecified
// ownership from UID/GID 0 (root) ownership.
type Ownership struct {
	UID int `json:"uid"`
	GID int `json:"gid"`
}

func (owner Ownership) Opt() llb.ChownOption {
	return llb.WithUIDGID(owner.UID, owner.GID)
}

// ContainerSecret configures a secret to expose, either as an environment
// variable or mounted to a file path.
type ContainerSecret struct {
	Secret    SecretID   `json:"secret"`
	EnvName   string     `json:"env,omitempty"`
	MountPath string     `json:"path,omitempty"`
	Owner     *Ownership `json:"owner,omitempty"`
	Mode      *int       `json:"mode,omitempty"`
}

// ContainerSocket configures a socket to expose, currently as a Unix socket,
// but potentially as a TCP or UDP address in the future.
type ContainerSocket struct {
	SocketID socket.ID  `json:"socket"`
	UnixPath string     `json:"unix_path,omitempty"`
	Owner    *Ownership `json:"owner,omitempty"`
}

// FSState returns the container's root filesystem mount state. If there is
// none (as with an empty container ID), it returns scratch.
func (container *Container) FSState() (llb.State, error) {
	if container.FS == nil {
		return llb.Scratch(), nil
	}

	return defToState(container.FS)
}

// MetaState returns the container's metadata mount state. If the container has
// yet to run, it returns nil.
func (container *Container) MetaState() (*llb.State, error) {
	if container.Meta == nil {
		return nil, nil
	}

	metaSt, err := defToState(container.Meta)
	if err != nil {
		return nil, err
	}

	return &metaSt, nil
}

// ContainerMount is a mount point configured in a container.
type ContainerMount struct {
	// The source of the mount.
	Source *pb.Definition `json:"source,omitempty"`

	// A path beneath the source to scope the mount to.
	SourcePath string `json:"source_path,omitempty"`

	// The path of the mount within the container.
	Target string `json:"target"`

	// Persist changes to the mount under this cache ID.
	CacheID string `json:"cache_id,omitempty"`

	// How to share the cache across concurrent runs.
	CacheSharingMode CacheSharingMode `json:"cache_sharing,omitempty"`

	// Configure the mount as a tmpfs.
	Tmpfs bool `json:"tmpfs,omitempty"`

	// Configure the mount as read-only.
	Readonly bool `json:"readonly,omitempty"`
}

// SourceState returns the state of the source of the mount.
func (mnt ContainerMount) SourceState() (llb.State, error) {
	if mnt.Source == nil {
		return llb.Scratch(), nil
	}

	return defToState(mnt.Source)
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

func (container *Container) From(ctx context.Context, bk *buildkit.Client, addr string) (*Container, error) {
	container = container.Clone()

	platform := container.Platform

	// `From` creates 2 vertices: fetching the image config and actually pulling the image.
	// We create a sub-pipeline to encapsulate both.
	ctx, subRecorder := progrock.WithGroup(ctx, fmt.Sprintf("from %s", addr), progrock.Weak())

	refName, err := reference.ParseNormalizedNamed(addr)
	if err != nil {
		return nil, err
	}

	ref := reference.TagNameOnly(refName).String()

	_, digest, cfgBytes, err := bk.ResolveImageConfig(ctx, ref, llb.ResolveImageConfigOpt{
		Platform:    &platform,
		ResolveMode: llb.ResolveModeDefault.String(),
	})
	if err != nil {
		return nil, err
	}

	digested, err := reference.WithDigest(refName, digest)
	if err != nil {
		return nil, err
	}

	var imgSpec specs.Image
	if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
		return nil, err
	}

	fsSt := llb.Image(
		digested.String(),
		llb.WithCustomNamef("pull %s", ref),
	)

	def, err := fsSt.Marshal(ctx, llb.Platform(container.Platform))
	if err != nil {
		return nil, err
	}

	container.FS = def.ToPB()

	// associate vertexes to the 'from' sub-pipeline
	buildkit.RecordVertexes(subRecorder, container.FS)

	container.Config = mergeImageConfig(container.Config, imgSpec.Config)
	container.ImageRef = digested.String()

	return container, nil
}

const defaultDockerfileName = "Dockerfile"

func (container *Container) Build(
	ctx context.Context,
	context *Directory,
	dockerfile string,
	buildArgs []BuildArg,
	target string,
	secrets []SecretID,
	bk *buildkit.Client,
	svcs *Services,
	buildCache *CacheMap[uint64, *Container],
) (*Container, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return buildCache.GetOrInitialize(
		cacheKey(
			container,
			context,
			dockerfile,
			buildArgs,
			target,
			secrets,
			// scope cache per-client to avoid sharing caches across builds that are
			// structurally similar but use different client-specific inputs (i.e.
			// local dir with same path but different content)
			clientMetadata.ClientID,
		),
		func() (*Container, error) {
			return container.buildUncached(ctx, bk, context, dockerfile, buildArgs, target, secrets, svcs)
		},
	)
}

func (container *Container) buildUncached(
	ctx context.Context,
	bk *buildkit.Client,
	context *Directory,
	dockerfile string,
	buildArgs []BuildArg,
	target string,
	secrets []SecretID,
	svcs *Services,
) (*Container, error) {
	container = container.Clone()

	container.Services.Merge(context.Services)

	for _, secretID := range secrets {
		secret, err := secretID.Decode()
		if err != nil {
			return nil, err
		}

		container.Secrets = append(container.Secrets, ContainerSecret{
			Secret:    secretID,
			MountPath: fmt.Sprintf("/run/secrets/%s", secret.Name),
		})
	}

	// set image ref to empty string
	container.ImageRef = ""

	// add a weak group for the docker build vertices
	ctx, subRecorder := progrock.WithGroup(ctx, "docker build", progrock.Weak())

	detach, _, err := svcs.StartBindings(ctx, bk, container.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	platform := container.Platform

	opts := map[string]string{
		"platform":      platforms.Format(platform),
		"contextsubdir": context.Dir,
	}

	if dockerfile != "" {
		opts["filename"] = path.Join(context.Dir, dockerfile)
	} else {
		opts["filename"] = path.Join(context.Dir, defaultDockerfileName)
	}

	if target != "" {
		opts["target"] = target
	}

	for _, buildArg := range buildArgs {
		opts["build-arg:"+buildArg.Name] = buildArg.Value
	}

	inputs := map[string]*pb.Definition{
		dockerui.DefaultLocalNameContext:    context.LLB,
		dockerui.DefaultLocalNameDockerfile: context.LLB,
	}

	res, err := bk.Solve(ctx, bkgw.SolveRequest{
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

	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}

	// associate vertexes to the 'docker build' sub-pipeline
	buildkit.RecordVertexes(subRecorder, def.ToPB())

	container.FS = def.ToPB()
	container.FS.Source = nil

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
	return &Directory{
		LLB:      container.FS,
		Dir:      "/",
		Platform: container.Platform,
		Pipeline: container.Pipeline,
		Services: container.Services,
	}, nil
}

func (container *Container) WithRootFS(ctx context.Context, dir *Directory) (*Container, error) {
	container = container.Clone()

	dirSt, err := dir.StateWithSourcePath()
	if err != nil {
		return nil, err
	}

	def, err := dirSt.Marshal(ctx, llb.Platform(dir.Platform))
	if err != nil {
		return nil, err
	}

	container.FS = def.ToPB()

	container.Services.Merge(dir.Services)

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithDirectory(ctx context.Context, bk *buildkit.Client, subdir string, src *Directory, filter CopyFilter, owner string) (*Container, error) {
	container = container.Clone()

	return container.writeToPath(ctx, bk, subdir, func(dir *Directory) (*Directory, error) {
		ownership, err := container.ownership(ctx, bk, owner)
		if err != nil {
			return nil, err
		}

		return dir.WithDirectory(ctx, ".", src, filter, ownership)
	})
}

func (container *Container) WithFile(ctx context.Context, bk *buildkit.Client, destPath string, src *File, permissions fs.FileMode, owner string) (*Container, error) {
	container = container.Clone()

	return container.writeToPath(ctx, bk, path.Dir(destPath), func(dir *Directory) (*Directory, error) {
		ownership, err := container.ownership(ctx, bk, owner)
		if err != nil {
			return nil, err
		}

		return dir.WithFile(ctx, path.Base(destPath), src, permissions, ownership)
	})
}

func (container *Container) WithNewFile(ctx context.Context, bk *buildkit.Client, dest string, content []byte, permissions fs.FileMode, owner string) (*Container, error) {
	container = container.Clone()

	dir, file := filepath.Split(dest)
	return container.writeToPath(ctx, bk, dir, func(dir *Directory) (*Directory, error) {
		ownership, err := container.ownership(ctx, bk, owner)
		if err != nil {
			return nil, err
		}

		return dir.WithNewFile(ctx, file, content, permissions, ownership)
	})
}

func (container *Container) WithMountedDirectory(ctx context.Context, bk *buildkit.Client, target string, dir *Directory, owner string, readonly bool) (*Container, error) {
	container = container.Clone()

	return container.withMounted(ctx, bk, target, dir.LLB, dir.Dir, dir.Services, owner, readonly)
}

func (container *Container) WithMountedFile(ctx context.Context, bk *buildkit.Client, target string, file *File, owner string, readonly bool) (*Container, error) {
	container = container.Clone()

	return container.withMounted(ctx, bk, target, file.LLB, file.File, file.Services, owner, readonly)
}

var SeenCacheKeys = new(sync.Map)

func (container *Container) WithMountedCache(ctx context.Context, bk *buildkit.Client, target string, cache *CacheVolume, source *Directory, sharingMode CacheSharingMode, owner string) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	if sharingMode == "" {
		sharingMode = CacheSharingModeShared
	}

	mount := ContainerMount{
		Target:           target,
		CacheID:          cache.Sum(),
		CacheSharingMode: sharingMode,
	}

	if source != nil {
		mount.Source = source.LLB
		mount.SourcePath = source.Dir
	}

	if owner != "" {
		var err error
		mount.Source, mount.SourcePath, err = container.chown(
			ctx,
			bk,
			mount.Source,
			mount.SourcePath,
			owner,
			llb.Platform(container.Platform),
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

func (container *Container) WithMountedTemp(ctx context.Context, target string) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	container.Mounts = container.Mounts.With(ContainerMount{
		Target: target,
		Tmpfs:  true,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithMountedSecret(ctx context.Context, bk *buildkit.Client, target string, source *Secret, owner string, mode *int) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	ownership, err := container.ownership(ctx, bk, owner)
	if err != nil {
		return nil, err
	}

	secretID, err := source.ID()
	if err != nil {
		return nil, err
	}

	container.Secrets = append(container.Secrets, ContainerSecret{
		Secret:    secretID,
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
		container.Mounts = append(container.Mounts[:foundIdx], container.Mounts[foundIdx+1:]...)
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

func (container *Container) WithUnixSocket(ctx context.Context, bk *buildkit.Client, target string, source *socket.Socket, owner string) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	ownership, err := container.ownership(ctx, bk, owner)
	if err != nil {
		return nil, err
	}

	socketID, err := source.ID()
	if err != nil {
		return nil, err
	}

	newSocket := ContainerSocket{
		SocketID: socketID,
		UnixPath: target,
		Owner:    ownership,
	}

	var replaced bool
	for i, sock := range container.Sockets {
		if sock.UnixPath == target {
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
		if sock.UnixPath == target {
			container.Sockets = append(container.Sockets[:i], container.Sockets[i+1:]...)
			break
		}
	}

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) WithSecretVariable(ctx context.Context, name string, secret *Secret) (*Container, error) {
	container = container.Clone()

	secretID, err := secret.ID()
	if err != nil {
		return nil, err
	}

	container.Secrets = append(container.Secrets, ContainerSecret{
		Secret:  secretID,
		EnvName: name,
	})

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) Directory(ctx context.Context, bk *buildkit.Client, svcs *Services, dirPath string) (*Directory, error) {
	dir, _, err := locatePath(ctx, container, dirPath, NewDirectory)
	if err != nil {
		return nil, err
	}

	// check that the directory actually exists so the user gets an error earlier
	// rather than when the dir is used
	info, err := dir.Stat(ctx, bk, svcs, ".")
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("path %s is a file, not a directory", dirPath)
	}

	return dir, nil
}

func (container *Container) File(ctx context.Context, bk *buildkit.Client, svcs *Services, filePath string) (*File, error) {
	file, _, err := locatePath(ctx, container, filePath, NewFile)
	if err != nil {
		return nil, err
	}

	// check that the file actually exists so the user gets an error earlier
	// rather than when the file is used
	info, err := file.Stat(ctx, bk, svcs)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path %s is a directory, not a file", filePath)
	}

	return file, nil
}

func locatePath[T *File | *Directory](
	ctx context.Context,
	container *Container,
	containerPath string,
	init func(context.Context, *pb.Definition, string, pipeline.Path, specs.Platform, ServiceBindings) T,
) (T, *ContainerMount, error) {
	containerPath = absPath(container.Config.WorkingDir, containerPath)

	// NB(vito): iterate in reverse order so we'll find deeper mounts first
	for i := len(container.Mounts) - 1; i >= 0; i-- {
		mnt := container.Mounts[i]

		if containerPath == mnt.Target || strings.HasPrefix(containerPath, mnt.Target+"/") {
			if mnt.Tmpfs {
				return nil, nil, fmt.Errorf("%s: cannot retrieve path from tmpfs", containerPath)
			}

			if mnt.CacheID != "" {
				return nil, nil, fmt.Errorf("%s: cannot retrieve path from cache", containerPath)
			}

			sub := mnt.SourcePath
			if containerPath != mnt.Target {
				// make relative portion relative to the source path
				dirSub := strings.TrimPrefix(containerPath, mnt.Target+"/")
				if dirSub != "" {
					sub = path.Join(sub, dirSub)
				}
			}

			return init(
				ctx,
				mnt.Source,
				sub,
				container.Pipeline,
				container.Platform,
				container.Services,
			), &mnt, nil
		}
	}

	// Not found in a mount
	return init(
		ctx,
		container.FS,
		containerPath,
		container.Pipeline,
		container.Platform,
		container.Services,
	), nil, nil
}

func (container *Container) withMounted(
	ctx context.Context,
	bk *buildkit.Client,
	target string,
	srcDef *pb.Definition,
	srcPath string,
	svcs ServiceBindings,
	owner string,
	readonly bool,
) (*Container, error) {
	target = absPath(container.Config.WorkingDir, target)

	var err error
	if owner != "" {
		srcDef, srcPath, err = container.chown(ctx, bk, srcDef, srcPath, owner, llb.Platform(container.Platform))
		if err != nil {
			return nil, err
		}
	}

	container.Mounts = container.Mounts.With(ContainerMount{
		Source:     srcDef,
		SourcePath: srcPath,
		Target:     target,
	})

	container.Services.Merge(svcs)

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) chown(
	ctx context.Context,
	bk *buildkit.Client,
	srcDef *pb.Definition,
	srcPath string,
	owner string,
	opts ...llb.ConstraintsOpt,
) (*pb.Definition, string, error) {
	ownership, err := container.ownership(ctx, bk, owner)
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

func (container *Container) writeToPath(ctx context.Context, bk *buildkit.Client, subdir string, fn func(dir *Directory) (*Directory, error)) (*Container, error) {
	dir, mount, err := locatePath(ctx, container, subdir, NewDirectory)
	if err != nil {
		return nil, err
	}

	dir.Pipeline = container.Pipeline

	dir, err = fn(dir)
	if err != nil {
		return nil, err
	}

	// If not in a mount, replace rootfs
	if mount == nil {
		root, err := dir.Root()
		if err != nil {
			return nil, err
		}

		return container.WithRootFS(ctx, root)
	}

	return container.withMounted(ctx, bk, mount.Target, dir.LLB, mount.SourcePath, nil, "", false)
}

func (container *Container) ImageConfig(ctx context.Context) (specs.ImageConfig, error) {
	return container.Config, nil
}

func (container *Container) UpdateImageConfig(ctx context.Context, updateFn func(specs.ImageConfig) specs.ImageConfig) (*Container, error) {
	container = container.Clone()
	container.Config = updateFn(container.Config)
	return container, nil
}

func (container *Container) WithPipeline(ctx context.Context, name, description string, labels []pipeline.Label) (*Container, error) {
	container = container.Clone()

	container.Pipeline = container.Pipeline.Add(pipeline.Pipeline{
		Name:        name,
		Description: description,
		Labels:      labels,
	})

	return container, nil
}

func (container *Container) WithExec(ctx context.Context, bk *buildkit.Client, progSock string, defaultPlatform specs.Platform, opts ContainerExecOpts) (*Container, error) { //nolint:gocyclo
	container = container.Clone()

	cfg := container.Config
	mounts := container.Mounts
	platform := container.Platform
	if platform.OS == "" {
		platform = defaultPlatform
	}

	args, err := container.command(opts)
	if err != nil {
		return nil, err
	}

	var namef string
	if container.Focused {
		namef = buildkit.FocusPrefix + "exec %s"
	} else {
		namef = "exec %s"
	}

	runOpts := []llb.RunOption{
		llb.Args(args),
		llb.WithCustomNamef(namef, strings.Join(args, " ")),
	}

	// this allows executed containers to communicate back to this API
	if opts.ExperimentalPrivilegedNesting {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_ENABLE_NESTING", ""))
	}

	if opts.CacheExitCode != 0 {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_CACHE_EXIT_CODE", strconv.FormatUint(uint64(opts.CacheExitCode), 10)))
	}

	metaSt, metaSourcePath := metaMount(opts.Stdin)

	// create /dagger mount point for the shim to write to
	runOpts = append(runOpts,
		llb.AddMount(buildkit.MetaMountDestPath, metaSt, llb.SourcePath(metaSourcePath)))

	if opts.RedirectStdout != "" {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_REDIRECT_STDOUT", opts.RedirectStdout))
	}

	if opts.RedirectStderr != "" {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_REDIRECT_STDERR", opts.RedirectStderr))
	}

	for _, bnd := range container.Services {
		for _, alias := range bnd.Aliases {
			runOpts = append(runOpts,
				llb.AddEnv("_DAGGER_HOSTNAME_ALIAS_"+alias, bnd.Hostname))
		}
	}

	if cfg.User != "" {
		runOpts = append(runOpts, llb.User(cfg.User))
	}

	if cfg.WorkingDir != "" {
		runOpts = append(runOpts, llb.Dir(cfg.WorkingDir))
	}

	for _, env := range cfg.Env {
		name, val, ok := strings.Cut(env, "=")
		if !ok {
			// it's OK to not be OK
			// we'll just set an empty env
			_ = ok
		}

		if name == "_DAGGER_ENABLE_NESTING" && !opts.ExperimentalPrivilegedNesting {
			// don't pass this through to the container when manually set, this is internal only
			continue
		}

		runOpts = append(runOpts, llb.AddEnv(name, val))
	}

	secretsToScrub := SecretToScrubInfo{}
	for i, secret := range container.Secrets {
		secretOpts := []llb.SecretOption{llb.SecretID(secret.Secret.String())}

		var secretDest string
		switch {
		case secret.EnvName != "":
			secretDest = secret.EnvName
			secretOpts = append(secretOpts, llb.SecretAsEnv(true))
			secretsToScrub.Envs = append(secretsToScrub.Envs, secret.EnvName)
		case secret.MountPath != "":
			secretDest = secret.MountPath
			secretsToScrub.Files = append(secretsToScrub.Files, secret.MountPath)
			if secret.Owner != nil {
				mode := 0o400
				if secret.Mode != nil {
					mode = *secret.Mode
				}

				secretOpts = append(secretOpts, llb.SecretFileOpt(
					secret.Owner.UID,
					secret.Owner.GID,
					mode,
				))
			}
		default:
			return nil, fmt.Errorf("malformed secret config at index %d", i)
		}

		runOpts = append(runOpts, llb.AddSecret(secretDest, secretOpts...))
	}

	if len(secretsToScrub.Envs) != 0 || len(secretsToScrub.Files) != 0 {
		// we sort to avoid non-deterministic order that would break caching
		sort.Strings(secretsToScrub.Envs)
		sort.Strings(secretsToScrub.Files)

		secretsToScrubJSON, err := json.Marshal(secretsToScrub)
		if err != nil {
			return nil, fmt.Errorf("scrub secrets json: %w", err)
		}
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_SCRUB_SECRETS", string(secretsToScrubJSON)))
	}

	for _, ctrSocket := range container.Sockets {
		if ctrSocket.UnixPath == "" {
			return nil, fmt.Errorf("unsupported socket: only unix paths are implemented")
		}

		socketOpts := []llb.SSHOption{
			llb.SSHID(string(ctrSocket.SocketID)),
			llb.SSHSocketTarget(ctrSocket.UnixPath),
		}

		if ctrSocket.Owner != nil {
			socketOpts = append(socketOpts,
				llb.SSHSocketOpt(
					ctrSocket.UnixPath,
					ctrSocket.Owner.UID,
					ctrSocket.Owner.GID,
					0o600, // preserve default
				))
		}

		runOpts = append(runOpts, llb.AddSSHSocket(socketOpts...))
	}

	for _, mnt := range mounts {
		srcSt, err := mnt.SourceState()
		if err != nil {
			return nil, fmt.Errorf("mount %s: %w", mnt.Target, err)
		}

		mountOpts := []llb.MountOption{}

		if mnt.SourcePath != "" {
			mountOpts = append(mountOpts, llb.SourcePath(mnt.SourcePath))
		}

		if mnt.CacheID != "" {
			var sharingMode llb.CacheMountSharingMode
			switch mnt.CacheSharingMode {
			case CacheSharingModeShared:
				sharingMode = llb.CacheMountShared
			case CacheSharingModePrivate:
				sharingMode = llb.CacheMountPrivate
			case CacheSharingModeLocked:
				sharingMode = llb.CacheMountLocked
			default:
				return nil, errors.Errorf("invalid cache mount sharing mode %q", mnt.CacheSharingMode)
			}

			mountOpts = append(mountOpts, llb.AsPersistentCacheDir(mnt.CacheID, sharingMode))
		}

		if mnt.Tmpfs {
			mountOpts = append(mountOpts, llb.Tmpfs())
		}

		if mnt.Readonly {
			mountOpts = append(mountOpts, llb.Readonly)
		}

		runOpts = append(runOpts, llb.AddMount(mnt.Target, srcSt, mountOpts...))
	}

	if opts.InsecureRootCapabilities {
		runOpts = append(runOpts, llb.Security(llb.SecurityModeInsecure))
	}

	fsSt, err := container.FSState()
	if err != nil {
		return nil, fmt.Errorf("fs state: %w", err)
	}

	execSt := fsSt.Run(runOpts...)

	execDef, err := execSt.Root().Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, fmt.Errorf("marshal root: %w", err)
	}

	container.FS = execDef.ToPB()

	metaDef, err := execSt.GetMount(buildkit.MetaMountDestPath).Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, fmt.Errorf("get meta mount: %w", err)
	}

	container.Meta = metaDef.ToPB()

	for i, mnt := range mounts {
		if mnt.Tmpfs || mnt.CacheID != "" {
			continue
		}

		mountSt := execSt.GetMount(mnt.Target)

		// propagate any changes to regular mounts to subsequent containers
		execMountDef, err := mountSt.Marshal(ctx, llb.Platform(platform))
		if err != nil {
			return nil, fmt.Errorf("propagate %s: %w", mnt.Target, err)
		}

		mounts[i].Source = execMountDef.ToPB()
	}

	container.Mounts = mounts

	// set image ref to empty string
	container.ImageRef = ""

	return container, nil
}

func (container *Container) Evaluate(ctx context.Context, bk *buildkit.Client, svcs *Services) (*buildkit.Result, error) {
	if container.FS == nil {
		return nil, nil
	}

	detach, _, err := svcs.StartBindings(ctx, bk, container.Services)
	if err != nil {
		return nil, err
	}
	defer detach()

	st, err := container.FSState()
	if err != nil {
		return nil, err
	}

	def, err := st.Marshal(ctx, llb.Platform(container.Platform))
	if err != nil {
		return nil, err
	}

	return bk.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: def.ToPB(),
	})
}

func (container *Container) MetaFileContents(ctx context.Context, bk *buildkit.Client, svcs *Services, progSock string, filePath string) (string, error) {
	if container.Meta == nil {
		ctr, err := container.WithExec(ctx, bk, progSock, container.Platform, ContainerExecOpts{})
		if err != nil {
			return "", err
		}
		return ctr.MetaFileContents(ctx, bk, svcs, progSock, filePath)
	}

	file := NewFile(
		ctx,
		container.Meta,
		path.Join(buildkit.MetaSourcePath, filePath),
		container.Pipeline,
		container.Platform,
		container.Services,
	)

	content, err := file.Contents(ctx, bk, svcs)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (container *Container) Publish(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	ref string,
	platformVariants []ContainerID,
	forcedCompression ImageLayerCompression,
	mediaTypes ImageMediaTypes,
) (string, error) {
	if mediaTypes == "" {
		// Modern registry implementations support oci types and docker daemons
		// have been capable of pulling them since 2018:
		// https://github.com/moby/moby/pull/37359
		// So they are a safe default.
		mediaTypes = OCIMediaTypes
	}

	inputByPlatform := map[string]buildkit.ContainerExport{}
	id, err := container.ID()
	if err != nil {
		return "", err
	}
	services := ServiceBindings{}
	for _, variantID := range append([]ContainerID{id}, platformVariants...) {
		variant, err := variantID.Decode()
		if err != nil {
			return "", err
		}
		if variant.FS == nil {
			continue
		}
		st, err := variant.FSState()
		if err != nil {
			return "", err
		}
		def, err := st.Marshal(ctx, llb.Platform(variant.Platform))
		if err != nil {
			return "", err
		}

		platformString := platforms.Format(variant.Platform)
		if _, ok := inputByPlatform[platformString]; ok {
			return "", fmt.Errorf("duplicate platform %q", platformString)
		}
		inputByPlatform[platforms.Format(variant.Platform)] = buildkit.ContainerExport{
			Definition: def.ToPB(),
			Config:     variant.Config,
		}
		services.Merge(variant.Services)
	}
	if len(inputByPlatform) == 0 {
		// Could also just ignore and do nothing, airing on side of error until proven otherwise.
		return "", errors.New("no containers to export")
	}

	opts := map[string]string{
		string(exptypes.OptKeyName):     ref,
		string(exptypes.OptKeyPush):     strconv.FormatBool(true),
		string(exptypes.OptKeyOCITypes): strconv.FormatBool(mediaTypes == OCIMediaTypes),
	}
	if forcedCompression != "" {
		opts[string(exptypes.OptKeyLayerCompression)] = strings.ToLower(string(forcedCompression))
		opts[string(exptypes.OptKeyForceCompression)] = strconv.FormatBool(true)
	}

	detach, _, err := svcs.StartBindings(ctx, bk, services)
	if err != nil {
		return "", err
	}
	defer detach()

	resp, err := bk.PublishContainerImage(ctx, inputByPlatform, opts)
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

func (container *Container) Export(
	ctx context.Context,
	bk *buildkit.Client,
	svcs *Services,
	dest string,
	platformVariants []ContainerID,
	forcedCompression ImageLayerCompression,
	mediaTypes ImageMediaTypes,
) error {
	if mediaTypes == "" {
		// Modern registry implementations support oci types and docker daemons
		// have been capable of pulling them since 2018:
		// https://github.com/moby/moby/pull/37359
		// So they are a safe default.
		mediaTypes = OCIMediaTypes
	}

	inputByPlatform := map[string]buildkit.ContainerExport{}
	id, err := container.ID()
	if err != nil {
		return err
	}
	services := ServiceBindings{}
	for _, variantID := range append([]ContainerID{id}, platformVariants...) {
		variant, err := variantID.Decode()
		if err != nil {
			return err
		}
		if variant.FS == nil {
			continue
		}
		st, err := variant.FSState()
		if err != nil {
			return err
		}

		def, err := st.Marshal(ctx, llb.Platform(variant.Platform))
		if err != nil {
			return err
		}

		platformString := platforms.Format(variant.Platform)
		if _, ok := inputByPlatform[platformString]; ok {
			return fmt.Errorf("duplicate platform %q", platformString)
		}
		inputByPlatform[platforms.Format(variant.Platform)] = buildkit.ContainerExport{
			Definition: def.ToPB(),
			Config:     variant.Config,
		}
		services.Merge(variant.Services)
	}
	if len(inputByPlatform) == 0 {
		// Could also just ignore and do nothing, airing on side of error until proven otherwise.
		return errors.New("no containers to export")
	}

	opts := map[string]string{
		"tar":                           strconv.FormatBool(true),
		string(exptypes.OptKeyOCITypes): strconv.FormatBool(mediaTypes == OCIMediaTypes),
	}
	if forcedCompression != "" {
		opts[string(exptypes.OptKeyLayerCompression)] = strings.ToLower(string(forcedCompression))
		opts[string(exptypes.OptKeyForceCompression)] = strconv.FormatBool(true)
	}

	detach, _, err := svcs.StartBindings(ctx, bk, services)
	if err != nil {
		return err
	}
	defer detach()

	_, err = bk.ExportContainerImage(ctx, inputByPlatform, dest, opts)
	return err
}

func (container *Container) Import(
	ctx context.Context,
	source FileID,
	tag string,
	bk *buildkit.Client,
	host *Host,
	svcs *Services,
	importCache *CacheMap[uint64, *specs.Descriptor],
	store content.Store,
	lm *leaseutil.Manager,
) (*Container, error) {
	file, err := source.Decode()
	if err != nil {
		return nil, err
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}

	var release func(context.Context) error
	loadManifest := func() (*specs.Descriptor, error) {
		src, err := file.Open(ctx, host, bk, svcs)
		if err != nil {
			return nil, err
		}

		defer src.Close()

		container = container.Clone()

		// override outer ctx with release ctx and set release
		ctx, release, err = leaseutil.WithLease(ctx, lm, leaseutil.MakeTemporary)
		if err != nil {
			return nil, err
		}

		stream := archive.NewImageImportStream(src, "")

		desc, err := stream.Import(ctx, store)
		if err != nil {
			return nil, fmt.Errorf("image archive import: %w", err)
		}

		return resolveIndex(ctx, store, desc, container.Platform, tag)
	}

	// TODO: seems inefficient to recompute for each platform, but do need to get platform-specific manifest stil..
	key := cacheKey(
		file,
		tag,
		container.Platform,
		// scope cache per-client to avoid sharing caches across builds that are
		// structurally similar but use different client-specific inputs (i.e.
		// local dir with same path but different content)
		clientMetadata.ClientID,
	)

	manifestDesc, err := importCache.GetOrInitialize(key, loadManifest)
	if err != nil {
		return nil, err
	}

	if _, err := store.Info(ctx, manifestDesc.Digest); err != nil {
		// NB(vito): loadManifest again, to be durable to buildctl prune.
		manifestDesc, err = loadManifest()
		if err != nil {
			return nil, fmt.Errorf("recover: %w", err)
		}
	}

	// NB: the repository portion of this ref doesn't actually matter, but it's
	// pleasant to see something recognizable.
	dummyRepo := "dagger/import"

	st := llb.OCILayout(
		fmt.Sprintf("%s@%s", dummyRepo, manifestDesc.Digest),
		llb.OCIStore("", buildkit.OCIStoreName),
		llb.Platform(container.Platform),
	)

	execDef, err := st.Marshal(ctx, llb.Platform(container.Platform))
	if err != nil {
		return nil, fmt.Errorf("marshal root: %w", err)
	}

	container.FS = execDef.ToPB()

	if release != nil {
		// eagerly evaluate the OCI reference so Buildkit sets up a long-term lease
		_, err = bk.Solve(ctx, bkgw.SolveRequest{
			Definition: container.FS,
			Evaluate:   true,
		})
		if err != nil {
			return nil, fmt.Errorf("solve: %w", err)
		}

		if err := release(ctx); err != nil {
			return nil, fmt.Errorf("release: %w", err)
		}
	}

	manifestBlob, err := content.ReadBlob(ctx, store, *manifestDesc)
	if err != nil {
		return nil, fmt.Errorf("image archive read manifest blob: %w", err)
	}

	var man specs.Manifest
	err = json.Unmarshal(manifestBlob, &man)
	if err != nil {
		return nil, fmt.Errorf("image archive unmarshal manifest: %w", err)
	}

	configBlob, err := content.ReadBlob(ctx, store, man.Config)
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

func (container *Container) WithServiceBinding(ctx context.Context, svcs *Services, svc *Service, alias string) (*Container, error) {
	container = container.Clone()

	host, err := svc.Hostname(ctx, svcs)
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

func (container *Container) ImageRefOrErr(ctx context.Context, bk *buildkit.Client) (string, error) {
	imgRef := container.ImageRef
	if imgRef != "" {
		return imgRef, nil
	}

	return "", errors.Errorf("Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
}

func (container *Container) Service(ctx context.Context, bk *buildkit.Client, progSock string) (*Service, error) {
	if container.Meta == nil {
		var err error
		container, err = container.WithExec(ctx, bk, progSock, container.Platform, ContainerExecOpts{})
		if err != nil {
			return nil, err
		}
	}
	return NewContainerService(container), nil
}

func (container *Container) ownership(ctx context.Context, bk *buildkit.Client, owner string) (*Ownership, error) {
	if owner == "" {
		// do not change ownership
		return nil, nil
	}

	fsSt, err := container.FSState()
	if err != nil {
		return nil, err
	}

	return resolveUIDGID(ctx, fsSt, bk, container.Platform, owner)
}

func (container *Container) command(opts ContainerExecOpts) ([]string, error) {
	cfg := container.Config
	args := opts.Args

	if len(args) == 0 {
		// we use the default args if no new default args are passed
		args = cfg.Cmd
	}

	if len(cfg.Entrypoint) > 0 && !opts.SkipEntrypoint {
		args = append(cfg.Entrypoint, args...)
	}

	if len(args) == 0 {
		return nil, errors.New("no command has been set")
	}

	return args, nil
}

func metaMount(stdin string) (llb.State, string) {
	// because the shim might run as non-root, we need to make a world-writable
	// directory first and then make it the base of the /dagger mount point.
	//
	// TODO(vito): have the shim exec as the other user instead?
	meta := llb.Mkdir(buildkit.MetaSourcePath, 0o777)
	if stdin != "" {
		meta = meta.Mkfile(path.Join(buildkit.MetaSourcePath, "stdin"), 0o600, []byte(stdin))
	}

	return llb.Scratch().File(
			meta,
			llb.WithCustomName(buildkit.InternalPrefix+"creating dagger metadata"),
		),
		buildkit.MetaSourcePath
}

type ContainerExecOpts struct {
	// Command to run instead of the container's default command
	Args []string

	// If the container has an entrypoint, ignore it for this exec rather than
	// calling it with args.
	SkipEntrypoint bool

	// Content to write to the command's standard input before closing
	Stdin string

	// Redirect the command's standard output to a file in the container
	RedirectStdout string

	// Redirect the command's standard error to a file in the container
	RedirectStderr string

	// Provide dagger access to the executed command
	// Do not use this option unless you trust the command being executed.
	// The command being executed WILL BE GRANTED FULL ACCESS TO YOUR HOST FILESYSTEM
	ExperimentalPrivilegedNesting bool

	// Grant the process all root capabilities
	InsecureRootCapabilities bool

	// (Internal-only for now) An exit code that will be caught by the shim, written to
	// the exec meta mount, but then result in the shim still exiting with 0 so that
	// the exec is cached.
	CacheExitCode uint32
}

type BuildArg struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// OCI manifest annotation that specifies an image's tag
const ociTagAnnotation = "org.opencontainers.image.ref.name"

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

const (
	CompressionGzip         ImageLayerCompression = "Gzip"
	CompressionZstd         ImageLayerCompression = "Zstd"
	CompressionEStarGZ      ImageLayerCompression = "EStarGZ"
	CompressionUncompressed ImageLayerCompression = "Uncompressed"
)

type ImageMediaTypes string

const (
	OCIMediaTypes    ImageMediaTypes = "OCIMediaTypes"
	DockerMediaTypes ImageMediaTypes = "DockerMediaTypes"
)
