package core

import (
	"context"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/pkg/transfer/archive"
	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
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
	"github.com/zeebo/xxh3"
)

var ErrContainerNoExec = errors.New("no command has been executed")

const OCIStoreName = "dagger-oci"

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

	// Hostname is the computed hostname for the container.
	Hostname string `json:"hostname,omitempty"`

	// Ports to expose from the container.
	Ports []ContainerPort `json:"ports,omitempty"`

	// Services to start before running the container.
	Services    ServiceBindings `json:"services,omitempty"`
	HostAliases []HostAlias     `json:"host_aliases,omitempty"`

	// Focused indicates whether subsequent operations will be
	// focused, i.e. shown more prominently in the UI.
	Focused bool `json:"focused"`
}

func NewContainer(id ContainerID, pipeline pipeline.Path, platform specs.Platform) (*Container, error) {
	container, err := id.ToContainer()
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
	cp.Services = cloneMap(cp.Services)
	cp.HostAliases = cloneSlice(cp.HostAliases)
	cp.Pipeline = cloneSlice(cp.Pipeline)
	return &cp
}

// ContainerID is an opaque value representing a content-addressed container.
type ContainerID string

func (id ContainerID) String() string {
	return string(id)
}

// ContainerID is digestible so that smaller hashes can be displayed in
// --debug vertex names.
var _ Digestible = ContainerID("")

func (id ContainerID) Digest() (digest.Digest, error) {
	return digest.FromString(id.String()), nil
}

func (id ContainerID) ToContainer() (*Container, error) {
	var container Container

	if id == "" {
		// scratch
		return &container, nil
	}

	if err := decodeID(&container, id); err != nil {
		return nil, err
	}

	return &container, nil
}

// ID marshals the container into a content-addressed ID.
func (container *Container) ID() (ContainerID, error) {
	return encodeID[ContainerID](container)
}

var _ pipeline.Pipelineable = (*Container)(nil)

// PipelinePath returns the container's pipeline path.
func (container *Container) PipelinePath() pipeline.Path {
	return container.Pipeline
}

// Container is digestible so that it can be recorded as an output of the
// --debug vertex that created it.
var _ Digestible = (*Container)(nil)

// Digest returns the container's content hash.
func (container *Container) Digest() (digest.Digest, error) {
	id, err := container.ID()
	if err != nil {
		return "", err
	}
	return id.Digest()
}

type HostAlias struct {
	Alias  string `json:"alias"`
	Target string `json:"target"`
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
}

// ContainerSocket configures a socket to expose, currently as a Unix socket,
// but potentially as a TCP or UDP address in the future.
type ContainerSocket struct {
	SocketID SocketID   `json:"socket"`
	UnixPath string     `json:"unix_path,omitempty"`
	Owner    *Ownership `json:"owner,omitempty"`
}

// ContainerPort configures a port to expose from the container.
type ContainerPort struct {
	Port        int             `json:"port"`
	Protocol    NetworkProtocol `json:"protocol"`
	Description *string         `json:"description,omitempty"`
}

// FSState returns the container's root filesystem mount state. If there is
// none (as with an empty container ID), it returns scratch.
func (container *Container) FSState() (llb.State, error) {
	if container.FS == nil {
		return llb.Scratch(), nil
	}

	return defToState(container.FS)
}

// metaSourcePath is a world-writable directory created and mounted to /dagger.
const metaSourcePath = "meta"

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
	CacheSharingMode string `json:"cache_sharing,omitempty"`

	// Configure the mount as a tmpfs.
	Tmpfs bool `json:"tmpfs,omitempty"`
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

	digest, cfgBytes, err := bk.ResolveImageConfig(ctx, ref, llb.ResolveImageConfigOpt{
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
	RecordVertexes(subRecorder, container.FS)

	container.Config = mergeImageConfig(container.Config, imgSpec.Config)
	container.ImageRef = digested.String()

	return container, nil
}

const defaultDockerfileName = "Dockerfile"

var buildCache = newCacheMap[uint64, *Container]()

func cacheKey(keys ...any) uint64 {
	hash := xxh3.New()

	enc := json.NewEncoder(hash)
	for _, key := range keys {
		enc.Encode(key)
	}

	return hash.Sum64()
}

func (container *Container) Build(
	ctx context.Context,
	bk *buildkit.Client,
	context *Directory,
	dockerfile string,
	buildArgs []BuildArg,
	target string,
	secrets []SecretID,
) (*Container, error) {
	return buildCache.GetOrInitialize(
		cacheKey(container, context, dockerfile, buildArgs, target, secrets),
		func() (*Container, error) {
			return container.buildUncached(ctx, bk, context, dockerfile, buildArgs, target, secrets)
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
) (*Container, error) {
	container = container.Clone()

	container.Services.Merge(context.Services)

	for _, secretID := range secrets {
		secret, err := secretID.ToSecret()
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

	return WithServices(ctx, bk, container.Services, func() (*Container, error) {
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
		RecordVertexes(subRecorder, def.ToPB())

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
	})
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

func (container *Container) WithMountedDirectory(ctx context.Context, bk *buildkit.Client, target string, dir *Directory, owner string) (*Container, error) {
	container = container.Clone()

	return container.withMounted(ctx, bk, target, dir.LLB, dir.Dir, dir.Services, owner)
}

func (container *Container) WithMountedFile(ctx context.Context, bk *buildkit.Client, target string, file *File, owner string) (*Container, error) {
	container = container.Clone()

	return container.withMounted(ctx, bk, target, file.LLB, file.File, file.Services, owner)
}

func (container *Container) WithMountedCache(ctx context.Context, bk *buildkit.Client, target string, cache *CacheVolume, source *Directory, concurrency CacheSharingMode, owner string) (*Container, error) {
	container = container.Clone()

	target = absPath(container.Config.WorkingDir, target)

	cacheSharingMode := ""
	switch concurrency {
	case CacheSharingModePrivate:
		cacheSharingMode = "private"
	case CacheSharingModeLocked:
		cacheSharingMode = "locked"
	default:
		cacheSharingMode = "shared"
	}

	mount := ContainerMount{
		Target:           target,
		CacheID:          cache.Sum(),
		CacheSharingMode: cacheSharingMode,
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

func (container *Container) WithMountedSecret(ctx context.Context, bk *buildkit.Client, target string, source *Secret, owner string) (*Container, error) {
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

func (container *Container) WithUnixSocket(ctx context.Context, bk *buildkit.Client, target string, source *Socket, owner string) (*Container, error) {
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

func (container *Container) Directory(ctx context.Context, bk *buildkit.Client, dirPath string) (*Directory, error) {
	dir, _, err := locatePath(ctx, container, dirPath, NewDirectory)
	if err != nil {
		return nil, err
	}

	// check that the directory actually exists so the user gets an error earlier
	// rather than when the dir is used
	info, err := dir.Stat(ctx, bk, ".")
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("path %s is a file, not a directory", dirPath)
	}

	return dir, nil
}

func (container *Container) File(ctx context.Context, bk *buildkit.Client, filePath string) (*File, error) {
	file, _, err := locatePath(ctx, container, filePath, NewFile)
	if err != nil {
		return nil, err
	}

	// check that the file actually exists so the user gets an error earlier
	// rather than when the file is used
	info, err := file.Stat(ctx, bk)
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

	return container.withMounted(ctx, bk, mount.Target, dir.LLB, mount.SourcePath, nil, "")
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

	var namef string
	if container.Focused {
		namef = focusPrefix + "exec %s"
	} else {
		namef = "exec %s"
	}

	runOpts := []llb.RunOption{
		llb.Args(args),
		llb.WithCustomNamef(namef, strings.Join(args, " ")),
	}

	// this allows executed containers to communicate back to this API
	if opts.ExperimentalPrivilegedNesting {
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, err
		}
		routerID := clientMetadata.RouterID
		runOpts = append(runOpts,
			llb.AddEnv("_DAGGER_ENABLE_NESTING", ""),
			llb.AddEnv("_DAGGER_ROUTER_ID", routerID),
			llb.AddEnv("_DAGGER_PROG_SOCK_PATH", progSock),
		)
	}

	// because the shim might run as non-root, we need to make a world-writable
	// directory first and then make it the base of the /dagger mount point.
	//
	// TODO(vito): have the shim exec as the other user instead?
	meta := llb.Mkdir(buildkit.MetaSourcePath, 0o777)
	if opts.Stdin != "" {
		meta = meta.Mkfile(path.Join(buildkit.MetaSourcePath, "stdin"), 0o600, []byte(opts.Stdin))
	}

	// create /dagger mount point for the shim to write to
	runOpts = append(runOpts,
		llb.AddMount(buildkit.MetaMountDestPath,
			llb.Scratch().File(meta, llb.WithCustomName(internalPrefix+"creating dagger metadata")),
			llb.SourcePath(buildkit.MetaSourcePath)))

	if opts.RedirectStdout != "" {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_REDIRECT_STDOUT", opts.RedirectStdout))
	}

	if opts.RedirectStderr != "" {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_REDIRECT_STDERR", opts.RedirectStderr))
	}

	for _, alias := range container.HostAliases {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_HOSTNAME_ALIAS_"+alias.Alias, alias.Target))
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
				secretOpts = append(secretOpts, llb.SecretFileOpt(
					secret.Owner.UID,
					secret.Owner.GID,
					0o400, // preserve default
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

		socket, err := ctrSocket.SocketID.ToSocket()
		if err != nil {
			return nil, fmt.Errorf("socket %s: %w", ctrSocket.UnixPath, err)
		}
		socketID, err := bk.SocketLLBID(socket.HostPath, socket.ClientHostname)
		if err != nil {
			return nil, fmt.Errorf("socket %s: %w", ctrSocket.UnixPath, err)
		}
		socketOpts := []llb.SSHOption{
			llb.SSHID(socketID),
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
			case "shared":
				sharingMode = llb.CacheMountShared
			case "private":
				sharingMode = llb.CacheMountPrivate
			case "locked":
				sharingMode = llb.CacheMountLocked
			default:
				return nil, errors.Errorf("invalid cache mount sharing mode %q", mnt.CacheSharingMode)
			}

			mountOpts = append(mountOpts, llb.AsPersistentCacheDir(mnt.CacheID, sharingMode))
		}

		if mnt.Tmpfs {
			mountOpts = append(mountOpts, llb.Tmpfs())
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

	// first, build without a hostname
	execStNoHostname := fsSt.Run(runOpts...)

	// next, marshal it to compute a deterministic hostname
	execDefNoHostname, err := execStNoHostname.Root().Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, fmt.Errorf("marshal root: %w", err)
	}
	// compute a *stable* digest so that hostnames don't change across sessions
	digest, err := stableDigest(execDefNoHostname.ToPB())
	if err != nil {
		return nil, fmt.Errorf("stable digest: %w", err)
	}
	hostname := hostHash(digest)
	container.Hostname = hostname

	// finally, build with the hostname set
	runOpts = append(runOpts, llb.Hostname(hostname))
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

func (container *Container) Evaluate(ctx context.Context, bk *buildkit.Client) error {
	if container.FS == nil {
		return nil
	}

	_, err := WithServices(ctx, bk, container.Services, func() (*buildkit.Result, error) {
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
	})
	return err
}

func (container *Container) Start(ctx context.Context, bk *buildkit.Client) (*Service, error) {
	if container.Hostname == "" {
		return nil, ErrContainerNoExec
	}

	health := newHealth(bk, container.Hostname, container.Ports)

	// annotate the container as a service so they can be treated differently
	// in the UI
	rec := progrock.RecorderFromContext(ctx).
		WithGroup(
			fmt.Sprintf("service %s", container.Hostname),
			progrock.Weak(),
		)

	svcCtx, stop := context.WithCancel(context.Background())
	svcCtx = progrock.RecorderToContext(svcCtx, rec)

	checked := make(chan error, 1)
	go func() {
		checked <- health.Check(svcCtx)
	}()

	exited := make(chan error, 1)
	go func() {
		exited <- container.Evaluate(svcCtx, bk)
	}()

	select {
	case err := <-checked:
		if err != nil {
			stop()
			return nil, fmt.Errorf("health check errored: %w", err)
		}

		_ = stop // leave it running

		return &Service{
			Container: container,
			Detach:    stop,
		}, nil
	case err := <-exited:
		stop() // interrupt healthcheck

		if err != nil {
			return nil, fmt.Errorf("exited: %w", err)
		}

		return nil, fmt.Errorf("service exited before healthcheck")
	}
}

func (container *Container) MetaFileContents(ctx context.Context, bk *buildkit.Client, progSock string, filePath string) (string, error) {
	if container.Meta == nil {
		ctr, err := container.WithExec(ctx, bk, progSock, container.Platform, ContainerExecOpts{})
		if err != nil {
			return "", err
		}
		return ctr.MetaFileContents(ctx, bk, progSock, filePath)
	}

	file := NewFile(
		ctx,
		container.Meta,
		path.Join(metaSourcePath, filePath),
		container.Pipeline,
		container.Platform,
		container.Services,
	)

	content, err := file.Contents(ctx, bk)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (container *Container) Publish(
	ctx context.Context,
	ref string,
	platformVariants []ContainerID,
	forcedCompression ImageLayerCompression,
	mediaTypes ImageMediaTypes,
) (string, error) {
	panic("reimplement container publish")
}

func (container *Container) Export(
	ctx context.Context,
	host *Host,
	dest string,
	platformVariants []ContainerID,
	forcedCompression ImageLayerCompression,
	mediaTypes ImageMediaTypes,
) error {
	panic("reimplement container export")
}

var importCache = newCacheMap[uint64, *specs.Descriptor]()

func (container *Container) Import(
	ctx context.Context,
	bk *buildkit.Client,
	host *Host,
	source FileID,
	tag string,
	store content.Store,
	lm *leaseutil.Manager,
) (*Container, error) {
	file, err := source.ToFile()
	if err != nil {
		return nil, err
	}

	var release func(context.Context) error
	loadManifest := func() (*specs.Descriptor, error) {
		src, err := file.Open(ctx, host, bk)
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

	key := cacheKey(file, tag)

	manifestDesc, err := importCache.GetOrInitialize(key, loadManifest)
	if err != nil {
		return nil, err
	}

	_, err = store.Info(ctx, manifestDesc.Digest)
	if err != nil {
		// TODO(vito): loadManifest again, to be durable to buildctl prune. but I
		// can't reproduce this at the moment since it doesn't seem to be pruned.
		return nil, fmt.Errorf("manifest pruned?: %w", err)
	}

	// NB: the repository portion of this ref doesn't actually matter, but it's
	// pleasant to see something recognizable.
	dummyRepo := "dagger/import"

	st := llb.OCILayout(
		fmt.Sprintf("%s@%s", dummyRepo, manifestDesc.Digest),
		llb.OCIStore("", OCIStoreName),
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

func (container *Container) HostnameOrErr() (string, error) {
	if container.Hostname == "" {
		return "", ErrContainerNoExec
	}

	return container.Hostname, nil
}

func (container *Container) Endpoint(port int, scheme string) (string, error) {
	if port == 0 {
		if len(container.Ports) == 0 {
			return "", fmt.Errorf("no ports exposed")
		}

		port = container.Ports[0].Port
	}

	host, err := container.HostnameOrErr()
	if err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf("%s:%d", host, port)
	if scheme != "" {
		endpoint = scheme + "://" + endpoint
	}

	return endpoint, nil
}

func (container *Container) WithExposedPort(port ContainerPort) (*Container, error) {
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

	filtered := []ContainerPort{}
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

func (container *Container) WithServiceBinding(svc *Container, alias string) (*Container, error) {
	container = container.Clone()

	svcID, err := svc.ID()
	if err != nil {
		return nil, err
	}

	container.Services.Merge(ServiceBindings{
		svcID: AliasSet{alias},
	})

	if alias != "" {
		hn, err := svc.HostnameOrErr()
		if err != nil {
			return nil, fmt.Errorf("get hostname: %w", err)
		}

		container.HostAliases = append(container.HostAliases, HostAlias{
			Alias:  alias,
			Target: hn,
		})
	}

	return container, nil
}

func (container *Container) export(
	ctx context.Context,
	bk *buildkit.Client,
	platformVariants []ContainerID,
) (*buildkit.Result, error) {
	panic("reimplement container export")
	/*
		containers := []*Container{}
		services := ServiceBindings{}
		if container.FS != nil {
			containers = append(containers, container)
			services.Merge(container.Services)
		}
		for _, id := range platformVariants {
			variant, err := id.ToContainer()
			if err != nil {
				return nil, err
			}
			if variant.FS != nil {
				containers = append(containers, variant)
				services.Merge(variant.Services)
			}
		}

		if len(containers) == 0 {
			// Could also just ignore and do nothing, airing on side of error until proven otherwise.
			return nil, errors.New("no containers to export")
		}

		return WithServices(ctx, bk, services, func() (*Result, error) {
			if len(containers) == 1 {
				exportContainer := containers[0]

				st, err := exportContainer.FSState()
				if err != nil {
					return nil, err
				}

				stDef, err := st.Marshal(ctx, llb.Platform(exportContainer.Platform))
				if err != nil {
					return nil, err
				}

				res, err := bk.Solve(ctx, bkgw.SolveRequest{
					Evaluate:   true,
					Definition: stDef.ToPB(),
				})
				if err != nil {
					return nil, err
				}

				cfgBytes, err := json.Marshal(specs.Image{
					Platform: specs.Platform{
						Architecture: exportContainer.Platform.Architecture,
						OS:           exportContainer.Platform.OS,
						OSVersion:    exportContainer.Platform.OSVersion,
						OSFeatures:   exportContainer.Platform.OSFeatures,
					},
					Config: exportContainer.Config,
				})
				if err != nil {
					return nil, err
				}
				res.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)

				return res, nil
			}

			res := &Result{}
			expPlatforms := &exptypes.Platforms{
				Platforms: make([]exptypes.Platform, len(containers)),
			}

			for i, exportContainer := range containers {
				st, err := exportContainer.FSState()
				if err != nil {
					return nil, err
				}

				stDef, err := st.Marshal(ctx, llb.Platform(exportContainer.Platform))
				if err != nil {
					return nil, err
				}

				r, err := bk.Solve(ctx, bkgw.SolveRequest{
					Evaluate:   true,
					Definition: stDef.ToPB(),
				})
				if err != nil {
					return nil, err
				}
				ref, err := r.SingleRef()
				if err != nil {
					return nil, err
				}

				platformKey := platforms.Format(exportContainer.Platform)
				res.AddRef(platformKey, ref)
				expPlatforms.Platforms[i] = exptypes.Platform{
					ID:       platformKey,
					Platform: exportContainer.Platform,
				}

				cfgBytes, err := json.Marshal(specs.Image{
					Platform: specs.Platform{
						Architecture: exportContainer.Platform.Architecture,
						OS:           exportContainer.Platform.OS,
						OSVersion:    exportContainer.Platform.OSVersion,
						OSFeatures:   exportContainer.Platform.OSFeatures,
					},
					Config: exportContainer.Config,
				})
				if err != nil {
					return nil, err
				}
				res.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, platformKey), cfgBytes)
			}

			platformBytes, err := json.Marshal(expPlatforms)
			if err != nil {
				return nil, err
			}
			res.AddMeta(exptypes.ExporterPlatformsKey, platformBytes)

			return res, nil
		})
	*/
}

func (container *Container) ImageRefOrErr(ctx context.Context, bk *buildkit.Client) (string, error) {
	imgRef := container.ImageRef
	if imgRef != "" {
		return imgRef, nil
	}

	return "", errors.Errorf("Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
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
}

type BuildArg struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func hostHash(val digest.Digest) string {
	b, err := hex.DecodeString(val.Encoded())
	if err != nil {
		panic(err)
	}
	return b32(xxh3.Hash(b))
}

func b32(n uint64) string {
	var sum [8]byte
	binary.BigEndian.PutUint64(sum[:], n)
	return base32.HexEncoding.
		WithPadding(base32.NoPadding).
		EncodeToString(sum[:])
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
