package task

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"syscall"
	"time"

	"cuelang.org/go/cue"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/pkg"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Exec", func() Task { return &execTask{} })
	Register("Start", func() Task { return &asyncExecTask{} })
	Register("Stop", func() Task { return &stopAsyncExecTask{} })
	Register("SendSignal", func() Task { return &sendSignalTask{} })
}

type execTask struct {
}

func (t *execTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	common, err := parseCommon(pctx, v)
	if err != nil {
		return nil, err
	}
	opts, err := common.runOpts()
	if err != nil {
		return nil, err
	}

	// env
	envs, err := v.Lookup("env").Fields()
	if err != nil {
		return nil, err
	}
	for _, env := range envs {
		if plancontext.IsSecretValue(env.Value) {
			secret, err := pctx.Secrets.FromValue(env.Value)
			if err != nil {
				return nil, err
			}
			opts = append(opts, llb.AddSecret(env.Label(), llb.SecretID(secret.ID()), llb.SecretAsEnv(true)))
		} else {
			s, err := env.Value.String()
			if err != nil {
				return nil, err
			}
			opts = append(opts, llb.AddEnv(env.Label(), s))
		}
	}

	// always run
	always, err := v.Lookup("always").Bool()
	if err != nil {
		return nil, err
	}
	if always {
		opts = append(opts, llb.IgnoreCache)
	}

	// marker for status events
	// FIXME
	args := make([]string, 0, len(common.args))
	for _, a := range common.args {
		args = append(args, fmt.Sprintf("%q", a))
	}
	opts = append(opts, withCustomName(v, "Exec [%s]", strings.Join(args, ", ")))

	st, err := common.root.State()
	if err != nil {
		return nil, err
	}
	st = st.Run(opts...).Root()

	// Solve
	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	// Fill result
	resultFS := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": resultFS.MarshalCUE(),
		"exit":   0,
	})
}

type asyncExecTask struct {
}

func (t *asyncExecTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	common, err := parseCommon(pctx, v)
	if err != nil {
		return nil, err
	}
	req, err := common.containerRequest()
	if err != nil {
		return nil, err
	}

	// env
	envVal, err := v.Lookup("env").Fields()
	if err != nil {
		return nil, err
	}

	for _, env := range envVal {
		s, err := env.Value.String()
		if err != nil {
			return nil, err
		}
		req.Proc.Env = append(req.Proc.Env, fmt.Sprintf("%s=%s", env.Label(), s))
	}

	// platform
	platform := pb.PlatformFromSpec(pctx.Platform.Get())
	req.Container.Platform = &platform

	ctrID, err := s.StartContainer(ctx, req)
	if err != nil {
		return nil, err
	}

	lg := log.Ctx(ctx)
	lg.Debug().Msgf("started async exec %s", ctrID)

	// Fill result
	if err := v.FillPath(cue.MakePath(cue.Hid("_id", pkg.DaggerPackage)), ctrID); err != nil {
		return nil, err
	}
	return v, nil
}

type stopAsyncExecTask struct {
}

func (t *stopAsyncExecTask) Run(ctx context.Context, _ *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	ctrID, err := v.LookupPath(cue.MakePath(cue.Str("input"), cue.Hid("_id", pkg.DaggerPackage))).String()
	if err != nil {
		return nil, err
	}

	timeout, err := v.Lookup("timeout").Int64()
	if err != nil {
		return nil, err
	}

	lg := log.Ctx(ctx)

	exitCode, err := s.StopContainer(ctx, ctrID, time.Duration(timeout))
	if err != nil {
		return nil, fmt.Errorf("failed to stop exec %s: %w", ctrID, err)
	}
	lg.Debug().Msgf("exec %s stopped with exit code %d", ctrID, exitCode)

	return compiler.NewValue().FillFields(map[string]interface{}{
		"exit": exitCode,
	})
}

type sendSignalTask struct {
}

func (t *sendSignalTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	ctrID, err := v.LookupPath(cue.MakePath(cue.Str("input"), cue.Hid("_id", pkg.DaggerPackage))).String()
	if err != nil {
		return nil, err
	}

	sigVal, err := v.Lookup("signal").Int64()
	if err != nil {
		return nil, err
	}
	sig := syscall.Signal(sigVal)

	if err := s.SignalContainer(ctx, ctrID, sig); err != nil {
		return nil, fmt.Errorf("failed to send signal %d to exec %s: %w", sig, ctrID, err)
	}

	return compiler.NewValue(), nil
}

func parseCommon(pctx *plancontext.Context, v *compiler.Value) (*execCommon, error) {
	e := &execCommon{
		hosts: make(map[string]string),
	}

	// root
	input, err := pctx.FS.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}
	e.root = input

	// args
	var cmd struct {
		Args []string
	}
	if err := v.Decode(&cmd); err != nil {
		return nil, err
	}
	e.args = cmd.Args

	// workdir
	workdir, err := v.Lookup("workdir").String()
	if err != nil {
		return nil, err
	}
	e.workdir = workdir

	// user
	user, err := v.Lookup("user").String()
	if err != nil {
		return nil, err
	}
	e.user = user

	// hosts
	hosts, err := v.Lookup("hosts").Fields()
	if err != nil {
		return nil, err
	}
	for _, host := range hosts {
		ip, err := host.Value.String()
		if err != nil {
			return nil, err
		}
		e.hosts[host.Label()] = ip
	}

	// mounts
	mounts, err := v.Lookup("mounts").Fields()
	if err != nil {
		return nil, err
	}
	for _, mntField := range mounts {
		if mntField.Value.Lookup("dest").IsConcreteR() != nil {
			return nil, fmt.Errorf("mount %q is not concrete", mntField.Selector.String())
		}
		mnt, err := parseMount(pctx, mntField.Value)
		if err != nil {
			return nil, err
		}
		e.mounts = append(e.mounts, mnt)
	}

	return e, nil
}

// fields that are common between sync and async execs
type execCommon struct {
	root    *plancontext.FS
	args    []string
	workdir string
	user    string
	hosts   map[string]string
	mounts  []mount
}

func (e execCommon) runOpts() ([]llb.RunOption, error) {
	opts := []llb.RunOption{
		llb.Args(e.args),
		llb.Dir(e.workdir),
		llb.User(e.user),
	}
	for k, v := range e.hosts {
		opts = append(opts, llb.AddExtraHost(k, net.ParseIP(v)))
	}
	for _, mnt := range e.mounts {
		opt, err := mnt.runOpt()
		if err != nil {
			return nil, err
		}
		opts = append(opts, opt)
	}
	return opts, nil
}

func (e execCommon) containerRequest() (solver.StartContainerRequest, error) {
	req := solver.StartContainerRequest{
		Container: client.NewContainerRequest{
			Mounts: []client.Mount{{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       e.root.Result(),
			}},
		},
		Proc: client.StartRequest{
			Args: e.args,
			User: e.user,
			Cwd:  e.workdir,
		},
	}

	for _, mnt := range e.mounts {
		m, err := mnt.containerMount()
		if err != nil {
			return req, err
		}
		req.Container.Mounts = append(req.Container.Mounts, m)
	}

	for k, v := range e.hosts {
		req.Container.ExtraHosts = append(req.Container.ExtraHosts, &pb.HostIP{Host: k, IP: v})
	}

	return req, nil
}

func parseMount(pctx *plancontext.Context, v *compiler.Value) (mount, error) {
	dest, err := v.Lookup("dest").String()
	if err != nil {
		return mount{}, err
	}

	typ, err := v.Lookup("type").String()
	if err != nil {
		return mount{}, err
	}
	switch typ {
	case "cache":
		contents := v.Lookup("contents")

		idValue := contents.Lookup("id")
		if !idValue.IsConcrete() {
			return mount{}, fmt.Errorf("cache %q is not set", v.Path().String())
		}
		id, err := idValue.String()
		if err != nil {
			return mount{}, err
		}

		concurrency, err := contents.Lookup("concurrency").String()
		if err != nil {
			return mount{}, err
		}
		var mode llb.CacheMountSharingMode
		switch concurrency {
		case "shared":
			mode = llb.CacheMountShared
		case "private":
			mode = llb.CacheMountPrivate
		case "locked":
			mode = llb.CacheMountLocked
		default:
			return mount{}, fmt.Errorf("unknown concurrency mode %q", concurrency)
		}
		return mount{
			dest: dest,
			cacheMount: &cacheMount{
				id:          id,
				concurrency: mode,
			},
		}, nil

	case "tmp":
		return mount{dest: dest, tmpMount: &tmpMount{}}, nil

	case "socket":
		socket, err := pctx.Sockets.FromValue(v.Lookup("contents"))
		if err != nil {
			return mount{}, err
		}
		return mount{dest: dest, socketMount: &socketMount{id: socket.ID()}}, nil

	case "fs":
		mnt := mount{
			dest:    dest,
			fsMount: &fsMount{},
		}

		contents, err := pctx.FS.FromValue(v.Lookup("contents"))
		if err != nil {
			return mount{}, err
		}
		mnt.fsMount.contents = contents

		if source := v.Lookup("source"); source.Exists() {
			src, err := source.String()
			if err != nil {
				return mount{}, err
			}
			mnt.fsMount.source = src
		}

		if ro := v.Lookup("ro"); ro.Exists() {
			readonly, err := ro.Bool()
			if err != nil {
				return mount{}, err
			}
			mnt.fsMount.readonly = readonly
		}

		return mnt, nil

	case "secret":
		contents, err := pctx.Secrets.FromValue(v.Lookup("contents"))
		if err != nil {
			return mount{}, err
		}

		opts := struct {
			UID  uint32
			GID  uint32
			Mask uint32
		}{}
		if err := v.Decode(&opts); err != nil {
			return mount{}, err
		}

		return mount{
			dest: dest,
			secretMount: &secretMount{
				id:   contents.ID(),
				uid:  opts.UID,
				gid:  opts.GID,
				mask: opts.Mask,
			},
		}, nil

	case "file":
		contents, err := v.Lookup("contents").String()
		if err != nil {
			return mount{}, err
		}

		opts := struct {
			Permissions uint32
		}{}

		if err := v.Decode(&opts); err != nil {
			return mount{}, err
		}

		return mount{
			dest: dest,
			fileMount: &fileMount{
				contents:    contents,
				permissions: opts.Permissions,
			},
		}, nil

	case "":
		return mount{}, errors.New("no mount type specified")
	default:
		return mount{}, fmt.Errorf("unsupported mount type %q", typ)
	}
}

type mount struct {
	dest string
	// following is a sum type (exactly one of the fields should be non-nil)
	cacheMount  *cacheMount
	tmpMount    *tmpMount
	socketMount *socketMount
	fsMount     *fsMount
	secretMount *secretMount
	fileMount   *fileMount
}

func (m mount) runOpt() (llb.RunOption, error) {
	switch {
	case m.cacheMount != nil:
		return llb.AddMount(
			m.dest,
			llb.Scratch(),
			llb.AsPersistentCacheDir(m.cacheMount.id, m.cacheMount.concurrency),
		), nil
	case m.tmpMount != nil:
		// FIXME: handle size
		return llb.AddMount(
			m.dest,
			llb.Scratch(),
			llb.Tmpfs(),
		), nil
	case m.socketMount != nil:
		return llb.AddSSHSocket(
			llb.SSHID(m.socketMount.id),
			llb.SSHSocketTarget(m.dest),
		), nil
	case m.fsMount != nil:
		st, err := m.fsMount.contents.State()
		if err != nil {
			return nil, err
		}
		var opts []llb.MountOption
		if m.fsMount.source != "" {
			opts = append(opts, llb.SourcePath(m.fsMount.source))
		}
		if m.fsMount.readonly {
			opts = append(opts, llb.Readonly)
		}
		return llb.AddMount(
			m.dest,
			st,
			opts...,
		), nil
	case m.secretMount != nil:
		return llb.AddSecret(
			m.dest,
			llb.SecretID(m.secretMount.id),
			llb.SecretFileOpt(int(m.secretMount.uid), int(m.secretMount.gid), int(m.secretMount.mask)),
		), nil
	case m.fileMount != nil:
		return llb.AddMount(
			m.dest,
			llb.Scratch().File(llb.Mkfile(
				"/file",
				fs.FileMode(m.fileMount.permissions),
				[]byte(m.fileMount.contents))),
			llb.SourcePath("/file"),
		), nil
	}
	return nil, fmt.Errorf("no mount type set")
}

func (m mount) containerMount() (client.Mount, error) {
	switch {
	case m.cacheMount != nil:
		mnt := client.Mount{
			Dest:      m.dest,
			MountType: pb.MountType_CACHE,
			CacheOpt: &pb.CacheOpt{
				ID: m.cacheMount.id,
			},
		}
		switch m.cacheMount.concurrency {
		case llb.CacheMountShared:
			mnt.CacheOpt.Sharing = pb.CacheSharingOpt_SHARED
		case llb.CacheMountPrivate:
			mnt.CacheOpt.Sharing = pb.CacheSharingOpt_PRIVATE
		case llb.CacheMountLocked:
			mnt.CacheOpt.Sharing = pb.CacheSharingOpt_LOCKED
		}
		return mnt, nil
	case m.tmpMount != nil:
		// FIXME: handle size
		return client.Mount{
			Dest:      m.dest,
			MountType: pb.MountType_TMPFS,
		}, nil
	case m.socketMount != nil:
		return client.Mount{
			Dest:      m.dest,
			MountType: pb.MountType_SSH,
			SSHOpt: &pb.SSHOpt{
				ID: m.socketMount.id,
			},
		}, nil
	case m.fsMount != nil:
		return client.Mount{
			Dest:      m.dest,
			MountType: pb.MountType_BIND,
			Ref:       m.fsMount.contents.Result(),
			Selector:  m.fsMount.source,
			Readonly:  m.fsMount.readonly,
		}, nil
	case m.secretMount != nil:
		return client.Mount{
			Dest:      m.dest,
			MountType: pb.MountType_SECRET,
			SecretOpt: &pb.SecretOpt{
				ID:   m.secretMount.id,
				Uid:  m.secretMount.uid,
				Gid:  m.secretMount.gid,
				Mode: m.secretMount.mask,
			},
		}, nil
	}
	return client.Mount{}, fmt.Errorf("no mount type set")
}

type cacheMount struct {
	id          string
	concurrency llb.CacheMountSharingMode
}

type tmpMount struct {
}

type socketMount struct {
	id string
}

type fsMount struct {
	contents *plancontext.FS
	source   string
	readonly bool
}

type secretMount struct {
	id   string
	uid  uint32
	gid  uint32
	mask uint32
}

type fileMount struct {
	contents    string
	permissions uint32
}
