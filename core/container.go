package core

import (
	"context"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/pkg/transfer/archive"
	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/docker/distribution/reference"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/zeebo/xxh3"
)

// Container is a content-addressed container.
type Container struct {
	ID ContainerID `json:"id"`
}

func NewContainer(id ContainerID, pipeline pipeline.Path, platform specs.Platform) (*Container, error) {
	if id == "" {
		payload := &containerIDPayload{
			Pipeline: pipeline.Copy(),
			Platform: platform,
		}

		id, err := payload.Encode()
		if err != nil {
			return nil, err
		}
		return &Container{ID: id}, nil
	}
	return &Container{ID: id}, nil
}

// ContainerID is an opaque value representing a content-addressed container.
type ContainerID string

func (id ContainerID) String() string {
	return string(id)
}

func (id ContainerID) decode() (*containerIDPayload, error) {
	if id == "" {
		// scratch
		return &containerIDPayload{}, nil
	}

	var payload containerIDPayload
	if err := decodeID(&payload, id); err != nil {
		return nil, err
	}

	return &payload, nil
}

// containerIDPayload is the inner content of a ContainerID.
type containerIDPayload struct {
	// The container's root filesystem.
	FS *pb.Definition `json:"fs"`

	// Image configuration (env, workdir, etc)
	Config specs.ImageConfig `json:"cfg"`

	// UID/GID of the current user specified in Config.User.
	//
	// These are fetched on config change and cached here so that they can be
	// applied easily to newly applied mounts.
	UID int `json:"uid,omitempty"`
	GID int `json:"gid,omitempty"`

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
}

type HostAlias struct {
	Alias  string `json:"alias"`
	Target string `json:"target"`
}

// ContainerSecret configures a secret to expose, either as an environment
// variable or mounted to a file path.
type ContainerSecret struct {
	Secret    SecretID `json:"secret"`
	EnvName   string   `json:"env,omitempty"`
	MountPath string   `json:"path,omitempty"`
	Owner     string   `json:"owner,omitempty"`
}

// ContainerSocket configures a socket to expose, currently as a Unix socket,
// but potentially as a TCP or UDP address in the future.
type ContainerSocket struct {
	Socket   SocketID `json:"socket"`
	UnixPath string   `json:"unix_path,omitempty"`
	Owner    string   `json:"owner,omitempty"`
}

// ContainerPort configures a port to expose from the container.
type ContainerPort struct {
	Port        int             `json:"port"`
	Protocol    NetworkProtocol `json:"protocol"`
	Description *string         `json:"description,omitempty"`
}

// Encode returns the opaque string ID representation of the container.
func (payload *containerIDPayload) Encode() (ContainerID, error) {
	id, err := encodeID(payload)
	if err != nil {
		return "", err
	}

	return ContainerID(id), nil
}

// FSState returns the container's root filesystem mount state. If there is
// none (as with an empty container ID), it returns scratch.
func (payload *containerIDPayload) FSState() (llb.State, error) {
	if payload.FS == nil {
		return llb.Scratch(), nil
	}

	return defToState(payload.FS)
}

// metaMountDestPath is the special path that the shim writes metadata to.
const metaMountDestPath = "/.dagger_meta_mount"

// metaSourcePath is a world-writable directory created and mounted to /dagger.
const metaSourcePath = "meta"

// MetaState returns the container's metadata mount state. If the container has
// yet to run, it returns nil.
func (payload *containerIDPayload) MetaState() (*llb.State, error) {
	if payload.Meta == nil {
		return nil, nil
	}

	metaSt, err := defToState(payload.Meta)
	if err != nil {
		return nil, err
	}

	return &metaSt, nil
}

// InheritOwner defaults an owner to the container's current user so that
// chained sequences of mounts and withUser will have the mounts retain the
// previous user instead of assuming the new one.
func (payload *containerIDPayload) InheritOwner(given string) string {
	if given == "" {
		return payload.Config.User
	}
	return given
}

// SyncUIDGID refreshes the UID/GID cache value from the image config's user.
func (payload *containerIDPayload) SyncUIDGID(ctx context.Context, gw bkgw.Client) error {
	fsSt, err := payload.FSState()
	if err != nil {
		return err
	}

	payload.UID, payload.GID, err = resolveUIDGID(
		ctx,
		fsSt,
		gw,
		payload.Platform,
		payload.Config.User,
	)
	if err != nil {
		return fmt.Errorf("resolve uid/gid: %w", err)
	}

	return nil
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

	// A user:group to set for the mount and its contents.
	//
	// The user and group can either be an ID (1000:1000) or a name (foo:bar).
	Owner string `json:"owner,omitempty"`
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

type PipelineMetaResolver struct {
	Resolver llb.ImageMetaResolver
	Pipeline pipeline.Path
}

func (r PipelineMetaResolver) ResolveImageConfig(ctx context.Context, ref string, opt llb.ResolveImageConfigOpt) (digest.Digest, []byte, error) {
	// FIXME: `ResolveImageConfig` doesn't support progress groups. As a workaround, we inject
	// the pipeline in the vertex name.
	opt.LogName = pipeline.CustomName{
		Name:     fmt.Sprintf("resolve image config for %s", ref),
		Pipeline: r.Pipeline,
	}.String()

	return r.Resolver.ResolveImageConfig(ctx, ref, opt)
}

func (container *Container) From(ctx context.Context, gw bkgw.Client, addr string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}
	platform := payload.Platform

	// `From` creates 2 vertices: fetching the image config and actually pulling the image.
	// We create a sub-pipeline to encapsulate both.
	p := payload.Pipeline.Add(pipeline.Pipeline{
		Name: fmt.Sprintf("from %s", addr),
	})

	refName, err := reference.ParseNormalizedNamed(addr)
	if err != nil {
		return nil, err
	}

	ref := reference.TagNameOnly(refName).String()

	resolver := PipelineMetaResolver{
		Resolver: gw,
		Pipeline: p,
	}

	digest, cfgBytes, err := resolver.ResolveImageConfig(ctx, ref, llb.ResolveImageConfigOpt{
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

	dir, err := NewDirectory(ctx,
		llb.Image(addr,
			llb.WithCustomNamef("pull %s", ref),
			p.LLBOpt(),
			llb.WithMetaResolver(resolver),
		),
		"/", payload.Pipeline, platform, nil)
	if err != nil {
		return nil, err
	}

	ctr, err := container.WithRootFS(ctx, dir)
	if err != nil {
		return nil, err
	}

	payload, err = ctr.ID.decode()
	if err != nil {
		return nil, err
	}

	// merge config.Env with imgSpec.Config.Env
	imgSpec.Config.Env = append(payload.Config.Env, imgSpec.Config.Env...)
	payload.Config = imgSpec.Config

	if err := payload.SyncUIDGID(ctx, gw); err != nil {
		return nil, err
	}

	payload.ImageRef = digested.String()

	return container.containerFromPayload(payload)
}

const defaultDockerfileName = "Dockerfile"

func (container *Container) Build(ctx context.Context, gw bkgw.Client, context *Directory, dockerfile string, buildArgs []BuildArg, target string, secrets []SecretID) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	ctxPayload, err := context.ID.Decode()
	if err != nil {
		return nil, err
	}

	for _, secretID := range secrets {
		secPayload := secretID.decode()

		payload.Secrets = append(payload.Secrets, ContainerSecret{
			// Mounted at /run/secrets/<secret-name>
			Secret:    secretID,
			MountPath: fmt.Sprintf("/run/secrets/%s", secPayload.Name),
		})
	}

	payload.Services.Merge(ctxPayload.Services)

	// set image ref to empty string
	payload.ImageRef = ""

	return WithServices(ctx, gw, payload.Services, func() (*Container, error) {
		platform := payload.Platform

		opts := map[string]string{
			"platform":      platforms.Format(platform),
			"contextsubdir": ctxPayload.Dir,
		}

		if dockerfile != "" {
			opts["filename"] = path.Join(ctxPayload.Dir, dockerfile)
		} else {
			opts["filename"] = path.Join(ctxPayload.Dir, defaultDockerfileName)
		}

		if target != "" {
			opts["target"] = target
		}

		for _, buildArg := range buildArgs {
			opts["build-arg:"+buildArg.Name] = buildArg.Value
		}

		inputs := map[string]*pb.Definition{
			dockerui.DefaultLocalNameContext:    ctxPayload.LLB,
			dockerui.DefaultLocalNameDockerfile: ctxPayload.LLB,
		}

		res, err := gw.Solve(ctx, bkgw.SolveRequest{
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

		overrideProgress(def, payload.Pipeline.Add(pipeline.Pipeline{
			Name: "docker build",
		}))

		payload.FS = def.ToPB()

		cfgBytes, found := res.Metadata[exptypes.ExporterImageConfigKey]
		if found {
			var imgSpec specs.Image
			if err := json.Unmarshal(cfgBytes, &imgSpec); err != nil {
				return nil, err
			}

			payload.Config = imgSpec.Config
		}

		if err := payload.SyncUIDGID(ctx, gw); err != nil {
			return nil, err
		}

		return container.containerFromPayload(payload)
	})
}

func (container *Container) RootFS(ctx context.Context) (*Directory, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	return (&directoryIDPayload{
		LLB:      payload.FS,
		Platform: payload.Platform,
		Pipeline: payload.Pipeline,
		Services: payload.Services,
	}).ToDirectory()
}

func (container *Container) WithRootFS(ctx context.Context, dir *Directory) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	dirPayload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	dirSt, err := dirPayload.StateWithSourcePath()
	if err != nil {
		return nil, err
	}

	def, err := dirSt.Marshal(ctx, llb.Platform(dirPayload.Platform))
	if err != nil {
		return nil, err
	}

	payload.FS = def.ToPB()

	payload.Services.Merge(dirPayload.Services)

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) WithDirectory(ctx context.Context, gw bkgw.Client, subdir string, src *Directory, filter CopyFilter, owner string) (*Container, error) {
	return container.writeToPath(ctx, subdir, func(dir *Directory) (*Directory, error) {
		uid, gid, err := container.uidgid(ctx, gw, owner)
		if err != nil {
			return nil, err
		}

		return dir.WithDirectory(ctx, ".", src, filter, uid, gid)
	})
}

func (container *Container) WithFile(ctx context.Context, gw bkgw.Client, subdir string, src *File, permissions fs.FileMode, owner string) (*Container, error) {
	return container.writeToPath(ctx, subdir, func(dir *Directory) (*Directory, error) {
		uid, gid, err := container.uidgid(ctx, gw, owner)
		if err != nil {
			return nil, err
		}

		return dir.WithFile(ctx, ".", src, permissions, uid, gid)
	})
}

func (container *Container) WithNewFile(ctx context.Context, gw bkgw.Client, dest string, content []byte, permissions fs.FileMode, owner string) (*Container, error) {
	dir, file := filepath.Split(dest)
	return container.writeToPath(ctx, dir, func(dir *Directory) (*Directory, error) {
		uid, gid, err := container.uidgid(ctx, gw, owner)
		if err != nil {
			return nil, err
		}

		return dir.WithNewFile(ctx, file, content, permissions, uid, gid)
	})
}

func (container *Container) WithMountedDirectory(ctx context.Context, target string, source *Directory, owner string) (*Container, error) {
	payload, err := source.ID.Decode()
	if err != nil {
		return nil, err
	}

	return container.withMounted(target, payload.LLB, payload.Dir, payload.Services, owner)
}

func (container *Container) WithMountedFile(ctx context.Context, target string, source *File, owner string) (*Container, error) {
	payload, err := source.ID.decode()
	if err != nil {
		return nil, err
	}

	return container.withMounted(target, payload.LLB, payload.File, payload.Services, owner)
}

func (container *Container) WithMountedCache(ctx context.Context, target string, cache CacheID, source *Directory, concurrency CacheSharingMode, owner string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	cachePayload, err := cache.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

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
		CacheID:          cachePayload.Sum(),
		CacheSharingMode: cacheSharingMode,
		Owner:            payload.InheritOwner(owner),
	}

	if source != nil {
		srcPayload, err := source.ID.Decode()
		if err != nil {
			return nil, err
		}

		mount.Source = srcPayload.LLB
		mount.SourcePath = srcPayload.Dir
	}

	payload.Mounts = payload.Mounts.With(mount)

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) WithMountedTemp(ctx context.Context, target string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	payload.Mounts = payload.Mounts.With(ContainerMount{
		Target: target,
		Tmpfs:  true,
	})

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) WithMountedSecret(ctx context.Context, target string, source *Secret, owner string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	payload.Secrets = append(payload.Secrets, ContainerSecret{
		Secret:    source.ID,
		MountPath: target,
		Owner:     payload.InheritOwner(owner),
	})

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) WithoutMount(ctx context.Context, target string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	var found bool
	var foundIdx int
	for i := len(payload.Mounts) - 1; i >= 0; i-- {
		if payload.Mounts[i].Target == target {
			found = true
			foundIdx = i
			break
		}
	}

	if found {
		payload.Mounts = append(payload.Mounts[:foundIdx], payload.Mounts[foundIdx+1:]...)
	}

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) Mounts(ctx context.Context) ([]string, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	mounts := []string{}
	for _, mnt := range payload.Mounts {
		mounts = append(mounts, mnt.Target)
	}

	return mounts, nil
}

func (container *Container) WithUnixSocket(ctx context.Context, target string, source *Socket, owner string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	newSocket := ContainerSocket{
		Socket:   source.ID,
		UnixPath: target,
		Owner:    payload.InheritOwner(owner),
	}

	var replaced bool
	for i, sock := range payload.Sockets {
		if sock.UnixPath == target {
			payload.Sockets[i] = newSocket
			replaced = true
			break
		}
	}

	if !replaced {
		payload.Sockets = append(payload.Sockets, newSocket)
	}

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) WithoutUnixSocket(ctx context.Context, target string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	for i, sock := range payload.Sockets {
		if sock.UnixPath == target {
			payload.Sockets = append(payload.Sockets[:i], payload.Sockets[i+1:]...)
			break
		}
	}

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) WithSecretVariable(ctx context.Context, name string, secret *Secret) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	payload.Secrets = append(payload.Secrets, ContainerSecret{
		Secret:  secret.ID,
		EnvName: name,
	})

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) Directory(ctx context.Context, gw bkgw.Client, dirPath string) (*Directory, error) {
	dir, _, err := locatePath(ctx, container, dirPath, NewDirectory)
	if err != nil {
		return nil, err
	}

	// check that the directory actually exists so the user gets an error earlier
	// rather than when the dir is used
	info, err := dir.Stat(ctx, gw, ".")
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("path %s is a file, not a directory", dirPath)
	}

	return dir, nil
}

func (container *Container) File(ctx context.Context, gw bkgw.Client, filePath string) (*File, error) {
	file, _, err := locatePath(ctx, container, filePath, NewFile)
	if err != nil {
		return nil, err
	}

	// check that the file actually exists so the user gets an error earlier
	// rather than when the file is used
	info, err := file.Stat(ctx, gw)
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
	init func(context.Context, llb.State, string, pipeline.Path, specs.Platform, ServiceBindings) (T, error),
) (T, *ContainerMount, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, nil, err
	}

	containerPath = absPath(payload.Config.WorkingDir, containerPath)

	var found T

	// NB(vito): iterate in reverse order so we'll find deeper mounts first
	for i := len(payload.Mounts) - 1; i >= 0; i-- {
		mnt := payload.Mounts[i]

		if containerPath == mnt.Target || strings.HasPrefix(containerPath, mnt.Target+"/") {
			if mnt.Tmpfs {
				return nil, nil, fmt.Errorf("%s: cannot retrieve path from tmpfs", containerPath)
			}

			if mnt.CacheID != "" {
				return nil, nil, fmt.Errorf("%s: cannot retrieve path from cache", containerPath)
			}

			st, err := mnt.SourceState()
			if err != nil {
				return nil, nil, err
			}

			sub := mnt.SourcePath
			if containerPath != mnt.Target {
				// make relative portion relative to the source path
				dirSub := strings.TrimPrefix(containerPath, mnt.Target+"/")
				if dirSub != "" {
					sub = path.Join(sub, dirSub)
				}
			}

			found, err := init(ctx, st, sub, payload.Pipeline, payload.Platform, payload.Services)
			if err != nil {
				return nil, nil, err
			}
			return found, &mnt, nil
		}
	}

	// Not found in a mount
	st, err := payload.FSState()
	if err != nil {
		return nil, nil, err
	}

	found, err = init(ctx, st, containerPath, payload.Pipeline, payload.Platform, payload.Services)
	if err != nil {
		return nil, nil, err
	}
	return found, nil, nil
}

func (container *Container) withMounted(
	target string,
	srcDef *pb.Definition,
	srcPath string,
	svcs ServiceBindings,
	owner string,
) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	target = absPath(payload.Config.WorkingDir, target)

	payload.Mounts = payload.Mounts.With(ContainerMount{
		Source:     srcDef,
		SourcePath: srcPath,
		Target:     target,
		Owner:      payload.InheritOwner(owner),
	})

	payload.Services.Merge(svcs)

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) writeToPath(ctx context.Context, subdir string, fn func(dir *Directory) (*Directory, error)) (*Container, error) {
	dir, mount, err := locatePath(ctx, container, subdir, NewDirectory)
	if err != nil {
		return nil, err
	}

	containerPayload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}
	dirPayload, err := dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	dirPayload.Pipeline = containerPayload.Pipeline

	dir, err = dirPayload.ToDirectory()
	if err != nil {
		return nil, err
	}

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

	dirPayload, err = dir.ID.Decode()
	if err != nil {
		return nil, err
	}

	return container.withMounted(mount.Target, dirPayload.LLB, mount.SourcePath, nil, "")
}

func (container *Container) ImageConfig(ctx context.Context) (specs.ImageConfig, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return specs.ImageConfig{}, err
	}

	return payload.Config, nil
}

func (container *Container) UpdateImageConfig(ctx context.Context, updateFn func(specs.ImageConfig) specs.ImageConfig) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	payload.Config = updateFn(payload.Config)

	return container.containerFromPayload(payload)
}

func (container *Container) WithUser(ctx context.Context, gw bkgw.Client, user string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	payload.Config.User = user

	if err := payload.SyncUIDGID(ctx, gw); err != nil {
		return nil, err
	}

	return container.containerFromPayload(payload)
}

func (container *Container) Pipeline(ctx context.Context, name, description string, labels []pipeline.Label) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, fmt.Errorf("decode id: %w", err)
	}

	payload.Pipeline = payload.Pipeline.Add(pipeline.Pipeline{
		Name:        name,
		Description: description,
		Labels:      labels,
	})

	return container.containerFromPayload(payload)
}

func (container *Container) WithExec(ctx context.Context, gw bkgw.Client, defaultPlatform specs.Platform, opts ContainerExecOpts) (*Container, error) { //nolint:gocyclo
	payload, err := container.ID.decode()
	if err != nil {
		return nil, fmt.Errorf("decode id: %w", err)
	}

	cfg := payload.Config
	mounts := payload.Mounts
	platform := payload.Platform
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

	runOpts := []llb.RunOption{
		llb.Args(args),
		payload.Pipeline.LLBOpt(),
		llb.WithCustomNamef("exec %s", strings.Join(args, " ")),
	}

	// this allows executed containers to communicate back to this API
	if opts.ExperimentalPrivilegedNesting {
		runOpts = append(runOpts,
			llb.AddEnv("_DAGGER_ENABLE_NESTING", ""),
		)
	}

	// because the shim might run as non-root, we need to make a world-writable
	// directory first and then make it the base of the /dagger mount point.
	//
	// TODO(vito): have the shim exec as the other user instead?
	meta := llb.Mkdir(metaSourcePath, 0o777)
	if opts.Stdin != "" {
		meta = meta.Mkfile(path.Join(metaSourcePath, "stdin"), 0o600, []byte(opts.Stdin))
	}

	// create /dagger mount point for the shim to write to
	runOpts = append(runOpts,
		llb.AddMount(metaMountDestPath,
			llb.Scratch().File(meta, pipeline.CustomName{Name: "creating dagger metadata", Internal: true}.LLBOpt(), payload.Pipeline.LLBOpt()),
			llb.SourcePath(metaSourcePath)))

	if opts.RedirectStdout != "" {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_REDIRECT_STDOUT", opts.RedirectStdout))
	}

	if opts.RedirectStderr != "" {
		runOpts = append(runOpts, llb.AddEnv("_DAGGER_REDIRECT_STDERR", opts.RedirectStderr))
	}

	for _, alias := range payload.HostAliases {
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
		if name == DebugFailedExecEnv {
			// don't pass this through either, should only be set by out code used for obtaining
			// output after a failed exec
			continue
		}

		runOpts = append(runOpts, llb.AddEnv(name, val))
	}

	secretsToScrub := SecretToScrubInfo{}
	for i, secret := range payload.Secrets {
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
			uid, gid, err := container.uidgid(ctx, gw, secret.Owner)
			if err != nil {
				return nil, err
			}
			secretOpts = append(secretOpts, llb.SecretFileOpt(uid, gid, 0o400)) // 0400 is default
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

	for _, socket := range payload.Sockets {
		if socket.UnixPath == "" {
			return nil, fmt.Errorf("unsupported socket: only unix paths are implemented")
		}

		socketOpts := []llb.SSHOption{
			llb.SSHID(socket.Socket.LLBID()),
			llb.SSHSocketTarget(socket.UnixPath),
		}

		uid, gid, err := container.uidgid(ctx, gw, socket.Owner)
		if err != nil {
			return nil, err
		}

		socketOpts = append(socketOpts,
			llb.SSHSocketOpt(socket.UnixPath, uid, gid, 0o600)) // 0600 is default

		runOpts = append(runOpts, llb.AddSSHSocket(socketOpts...))
	}

	for i, mnt := range mounts {
		srcSt, err := mnt.SourceState()
		if err != nil {
			return nil, fmt.Errorf("mount %s: %w", mnt.Target, err)
		}

		mountOpts := []llb.MountOption{}

		uid, gid, err := container.uidgid(ctx, gw, mnt.Owner)
		if err != nil {
			return nil, err
		}

		if !mnt.Tmpfs {
			// NB(vito): need to create intermediate directory with correct ownership
			// to handle the directory case, otherwise the mount will be owned by
			// root
			srcSt = llb.Scratch().File(
				llb.Mkdir("/chown", 0o700, llb.WithUIDGID(uid, gid)).
					Copy(srcSt, mnt.SourcePath, "/chown", llb.WithUIDGID(uid, gid)),
				pipeline.CustomName{
					Name:     fmt.Sprintf("chown to %d:%d", uid, gid),
					Pipeline: payload.Pipeline,
					Internal: true,
				}.LLBOpt(),
			)

			// "re-write" the source path to point to the chowned location
			mnt.SourcePath = filepath.Join("/chown", filepath.Base(mnt.SourcePath))
			mounts[i] = mnt
		}

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

	fsSt, err := payload.FSState()
	if err != nil {
		return nil, fmt.Errorf("fs state: %w", err)
	}

	// first, build without a hostname
	execStNoHostname := fsSt.Run(runOpts...)

	// next, marshal it to compute a deterministic hostname
	constraints := llb.NewConstraints(llb.Platform(platform))
	rootVtx := execStNoHostname.Root().Output().Vertex(ctx, constraints)
	digest, _, _, _, err := rootVtx.Marshal(ctx, constraints) //nolint:dogsled
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	hostname := hostHash(digest)
	payload.Hostname = hostname

	// finally, build with the hostname set
	runOpts = append(runOpts, llb.Hostname(hostname))
	execSt := fsSt.Run(runOpts...)

	execDef, err := execSt.Root().Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, fmt.Errorf("marshal root: %w", err)
	}

	payload.FS = execDef.ToPB()

	metaDef, err := execSt.GetMount(metaMountDestPath).Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, fmt.Errorf("get meta mount: %w", err)
	}

	payload.Meta = metaDef.ToPB()

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

	payload.Mounts = mounts

	// set image ref to empty string
	payload.ImageRef = ""

	return container.containerFromPayload(payload)
}

func (container *Container) Evaluate(ctx context.Context, gw bkgw.Client, pipelineOverride *pipeline.Path) error {
	payload, err := container.ID.decode()
	if err != nil {
		return err
	}

	if payload.FS == nil {
		return nil
	}

	_, err = WithServices(ctx, gw, payload.Services, func() (*bkgw.Result, error) {
		st, err := payload.FSState()
		if err != nil {
			return nil, err
		}

		def, err := st.Marshal(ctx, llb.Platform(payload.Platform))
		if err != nil {
			return nil, err
		}

		if pipelineOverride != nil {
			overrideProgress(def, *pipelineOverride)
		}

		return gw.Solve(ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: def.ToPB(),
		})
	})
	return err
}

func (container *Container) ExitCode(ctx context.Context, gw bkgw.Client) (int, error) {
	content, err := container.MetaFileContents(ctx, gw, "exitCode")
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(content)
}

func (container *Container) Start(ctx context.Context, gw bkgw.Client) (*Service, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	if payload.Hostname == "" {
		return nil, ErrContainerNoExec
	}

	health := newHealth(gw, payload.Hostname, payload.Ports)

	svcCtx, stop := context.WithCancel(context.Background())

	checked := make(chan error, 1)
	go func() {
		checked <- health.Check(ctx)
	}()

	exited := make(chan error, 1)
	go func() {
		// annotate the container as a service so they can be treated differently
		// in the UI
		pipeline := payload.Pipeline.Add(pipeline.Pipeline{
			Name: fmt.Sprintf("service %s", payload.Hostname),
			Labels: []pipeline.Label{
				{
					Name:  pipeline.ServiceHostnameLabel,
					Value: payload.Hostname,
				},
			},
		})

		exited <- container.Evaluate(svcCtx, gw, &pipeline)
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

func (container *Container) MetaFileContents(ctx context.Context, gw bkgw.Client, filePath string) (string, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return "", err
	}

	metaSt, err := payload.MetaState()
	if err != nil {
		return "", err
	}

	if metaSt == nil {
		return "", ErrContainerNoExec
	}

	file, err := NewFile(
		ctx,
		*metaSt,
		path.Join(metaSourcePath, filePath),
		payload.Pipeline,
		payload.Platform,
		payload.Services,
	)
	if err != nil {
		return "", err
	}

	content, err := file.Contents(ctx, gw)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (container *Container) Publish(
	ctx context.Context,
	ref string,
	platformVariants []ContainerID,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
) (string, error) {
	// NOTE: be careful to not overwrite any values from original solveOpts (i.e. with append).
	solveOpts.Exports = []bkclient.ExportEntry{
		{
			Type: bkclient.ExporterImage,
			Attrs: map[string]string{
				"name": ref,
				"push": "true",
			},
		},
	}

	ch, wg := mirrorCh(solveCh)
	defer wg.Wait()

	res, err := bkClient.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		return container.export(ctx, gw, platformVariants)
	}, ch)
	if err != nil {
		return "", err
	}

	refName, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", err
	}

	imageDigest, found := res.ExporterResponse[exptypes.ExporterImageDigestKey]
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

func (container *Container) Platform() (specs.Platform, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return specs.Platform{}, err
	}
	return payload.Platform, nil
}

func (container *Container) Export(
	ctx context.Context,
	host *Host,
	dest string,
	platformVariants []ContainerID,
	bkClient *bkclient.Client,
	solveOpts bkclient.SolveOpt,
	solveCh chan<- *bkclient.SolveStatus,
) error {
	dest, err := host.NormalizeDest(dest)
	if err != nil {
		return err
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}

	defer out.Close()

	return host.Export(ctx, bkclient.ExportEntry{
		Type: bkclient.ExporterOCI,
		Output: func(map[string]string) (io.WriteCloser, error) {
			return out, nil
		},
	}, bkClient, solveOpts, solveCh, func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		return container.export(ctx, gw, platformVariants)
	})
}

const OCIStoreName = "dagger-oci"

func (container *Container) Import(
	ctx context.Context,
	gw bkgw.Client,
	host *Host,
	source io.Reader,
	tag string,
	store content.Store,
) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	stream := archive.NewImageImportStream(source, "")

	desc, err := stream.Import(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("image archive import: %w", err)
	}

	manifestDesc, err := resolveIndex(ctx, store, desc, payload.Platform, tag)
	if err != nil {
		return nil, fmt.Errorf("image archive resolve index: %w", err)
	}

	// NB: the repository portion of this ref doesn't actually matter, but it's
	// pleasant to see something recognizable.
	dummyRepo := "dagger/import"

	st := llb.OCILayout(
		fmt.Sprintf("%s@%s", dummyRepo, manifestDesc.Digest),
		llb.OCIStore("", OCIStoreName),
		llb.Platform(payload.Platform),
	)

	execDef, err := st.Marshal(ctx, llb.Platform(payload.Platform))
	if err != nil {
		return nil, fmt.Errorf("marshal root: %w", err)
	}

	payload.FS = execDef.ToPB()

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

	payload.Config = imgSpec.Config

	err = payload.SyncUIDGID(ctx, gw)
	if err != nil {
		return nil, fmt.Errorf("update uid/gid: %w", err)
	}

	id, err := payload.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	return &Container{ID: id}, nil
}

func (container *Container) Hostname() (string, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return "", err
	}

	if payload.Hostname == "" {
		return "", ErrContainerNoExec
	}

	return payload.Hostname, nil
}

func (container *Container) Endpoint(port int, scheme string) (string, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return "", err
	}

	if port == 0 {
		if len(payload.Ports) == 0 {
			return "", fmt.Errorf("no ports exposed")
		}

		port = payload.Ports[0].Port
	}

	endpoint := fmt.Sprintf("%s:%d", payload.Hostname, port)
	if scheme != "" {
		endpoint = scheme + "://" + endpoint
	}

	return endpoint, nil
}

func (container *Container) WithExposedPort(port ContainerPort) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	payload.Ports = append(payload.Ports, port)

	if payload.Config.ExposedPorts == nil {
		payload.Config.ExposedPorts = map[string]struct{}{}
	}

	ociPort := fmt.Sprintf("%d/%s", port.Port, port.Protocol.Network())
	payload.Config.ExposedPorts[ociPort] = struct{}{}

	id, err := payload.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	return &Container{ID: id}, nil
}

func (container *Container) WithoutExposedPort(port int, protocol NetworkProtocol) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	filtered := []ContainerPort{}
	filteredOCI := map[string]struct{}{}
	for _, p := range payload.Ports {
		if p.Port != port || p.Protocol != protocol {
			filtered = append(filtered, p)
			ociPort := fmt.Sprintf("%d/%s", p.Port, p.Protocol.Network())
			filteredOCI[ociPort] = struct{}{}
		}
	}

	payload.Ports = filtered
	payload.Config.ExposedPorts = filteredOCI

	id, err := payload.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	return &Container{ID: id}, nil
}

func (container *Container) ExposedPorts() ([]ContainerPort, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	return payload.Ports, nil
}

func (container *Container) WithServiceBinding(svc *Container, alias string) (*Container, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return nil, err
	}

	payload.Services.Merge(ServiceBindings{
		svc.ID: AliasSet{alias},
	})

	if alias != "" {
		hn, err := svc.Hostname()
		if err != nil {
			return nil, fmt.Errorf("get hostname: %w", err)
		}

		payload.HostAliases = append(payload.HostAliases, HostAlias{
			Alias:  alias,
			Target: hn,
		})
	}

	id, err := payload.Encode()
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	return &Container{ID: id}, nil
}

func (container *Container) export(
	ctx context.Context,
	gw bkgw.Client,
	platformVariants []ContainerID,
) (*bkgw.Result, error) {
	payloads := []*containerIDPayload{}
	services := ServiceBindings{}
	if container.ID != "" {
		payload, err := container.ID.decode()
		if err != nil {
			return nil, err
		}
		if payload.FS != nil {
			payloads = append(payloads, payload)
			services.Merge(payload.Services)
		}
	}
	for _, id := range platformVariants {
		payload, err := id.decode()
		if err != nil {
			return nil, err
		}
		if payload.FS != nil {
			payloads = append(payloads, payload)
			services.Merge(payload.Services)
		}
	}

	if len(payloads) == 0 {
		// Could also just ignore and do nothing, airing on side of error until proven otherwise.
		return nil, errors.New("no containers to export")
	}

	return WithServices(ctx, gw, services, func() (*bkgw.Result, error) {
		if len(payloads) == 1 {
			payload := payloads[0]

			st, err := payload.FSState()
			if err != nil {
				return nil, err
			}

			stDef, err := st.Marshal(ctx, llb.Platform(payload.Platform))
			if err != nil {
				return nil, err
			}

			res, err := gw.Solve(ctx, bkgw.SolveRequest{
				Evaluate:   true,
				Definition: stDef.ToPB(),
			})
			if err != nil {
				return nil, err
			}

			cfgBytes, err := json.Marshal(specs.Image{
				Architecture: payload.Platform.Architecture,
				OS:           payload.Platform.OS,
				OSVersion:    payload.Platform.OSVersion,
				OSFeatures:   payload.Platform.OSFeatures,
				Config:       payload.Config,
			})
			if err != nil {
				return nil, err
			}
			res.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)

			return res, nil
		}

		res := bkgw.NewResult()
		expPlatforms := &exptypes.Platforms{
			Platforms: make([]exptypes.Platform, len(payloads)),
		}

		for i, payload := range payloads {
			st, err := payload.FSState()
			if err != nil {
				return nil, err
			}

			stDef, err := st.Marshal(ctx, llb.Platform(payload.Platform))
			if err != nil {
				return nil, err
			}

			r, err := gw.Solve(ctx, bkgw.SolveRequest{
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

			platformKey := platforms.Format(payload.Platform)
			res.AddRef(platformKey, ref)
			expPlatforms.Platforms[i] = exptypes.Platform{
				ID:       platformKey,
				Platform: payload.Platform,
			}

			cfgBytes, err := json.Marshal(specs.Image{
				Architecture: payload.Platform.Architecture,
				OS:           payload.Platform.OS,
				OSVersion:    payload.Platform.OSVersion,
				OSFeatures:   payload.Platform.OSFeatures,
				Config:       payload.Config,
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
}

func (container *Container) ImageRef(ctx context.Context, gw bkgw.Client) (string, error) {
	payload, err := container.ID.decode()
	if err != nil {
		return "", err
	}

	imgRef := payload.ImageRef
	if imgRef != "" {
		return imgRef, nil
	}

	return "", errors.Errorf("Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
}

func (container *Container) containerFromPayload(payload *containerIDPayload) (*Container, error) {
	id, err := payload.Encode()
	if err != nil {
		return nil, err
	}

	return &Container{ID: id}, nil
}

func (container *Container) uidgid(ctx context.Context, gw bkgw.Client, owner string) (int, int, error) {
	containerPayload, err := container.ID.decode()
	if err != nil {
		return -1, -1, err
	}

	if owner == "" {
		// inherit container's current user
		return containerPayload.UID, containerPayload.GID, nil
	}

	fsSt, err := containerPayload.FSState()
	if err != nil {
		return -1, -1, err
	}

	return resolveUIDGID(ctx, fsSt, gw, containerPayload.Platform, owner)
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

// Override the progress pipeline of every LLB vertex in the DAG.
//
// FIXME: this can't be done in a normal way because Buildkit doesn't currently
// allow overriding the metadata of DefinitionOp. See this PR and comment:
// https://github.com/moby/buildkit/pull/2819
func overrideProgress(def *llb.Definition, pipeline pipeline.Path) {
	for dgst, metadata := range def.Metadata {
		metadata.ProgressGroup = pipeline.ProgressGroup()
		def.Metadata[dgst] = metadata
	}
}
