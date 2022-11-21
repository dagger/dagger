package task

import (
	"context"
	"errors"
	"fmt"

	"dagger.io/dagger"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Exec", func() Task { return &execTask{} })
}

type execTask struct {
}

func (t *execTask) Run(ctx context.Context, pctx *plancontext.Context, _ *solver.Solver, dgr *dagger.Client, v *compiler.Value) (*compiler.Value, error) {
	common, err := parseCommon(pctx, v)
	if err != nil {
		return nil, err
	}
	// fmt.Println("dude...", common)
	// return compiler.NewValue(), nil
	// opts, err := common.runOpts()
	// if err != nil {
	// 	return nil, err
	// }

	// env

	// always run
	// always, err := v.Lookup("always").Bool()
	// if err != nil {
	// 	return nil, err
	// }

	// marker for status events
	// FIXME

	// args := make([]string, 0, len(common.args))
	// for _, a := range common.args {
	// 	args = append(args, fmt.Sprintf("%q", a))
	// }

	ctr := dgr.Container().WithFS(dgr.Directory(dagger.DirectoryOpts{ID: common.FSID}))

	envs, err := v.Lookup("env").Fields()
	if err != nil {
		return nil, err
	}
	for _, env := range envs {
		if utils.IsSecretValue(env.Value) {
			id, err := utils.GetSecretId(env.Value)

			if err != nil {
				return nil, err
			}
			ctr = ctr.WithSecretVariable(env.Label(), dgr.Secret(id))
		} else {
			s, err := env.Value.String()
			if err != nil {
				return nil, err
			}
			ctr = ctr.WithEnvVariable(env.Label(), s)
		}
	}

	// TODO: Finish implementing mount types.

	for _, m := range common.mounts {
		switch {
		case m.cacheMount != nil:
			// TODO: Need sharing mode...

			ctr = ctr.WithMountedCache(m.dest, dgr.CacheVolume(m.cacheMount.id))
		case m.tmpMount != nil:
			ctr = ctr.WithMountedTemp(m.dest)
		case m.fsMount != nil:
			ctr = ctr.WithMountedDirectory(m.dest, dgr.Directory(dagger.DirectoryOpts{ID: m.fsMount.fsid}))
		case m.secretMount != nil:
			ctr = ctr.WithMountedSecret(m.dest, dgr.Secret(dagger.SecretID(m.secretMount.id)))
		case m.fileMount != nil:
			file := dgr.Directory().WithNewFile("/file", dagger.DirectoryWithNewFileOpts{Contents: m.fileMount.contents}).File("/file")
			ctr = ctr.WithMountedFile(m.dest, file)
		default:
		}
	}

	ctr = ctr.WithUser(common.user).WithWorkdir(common.workdir)

	exec := ctr.Exec(dagger.ContainerExecOpts{
		Args: common.args,
	})
	fsid, err := exec.FS().ID(ctx)

	if err != nil {
		return nil, err
	}
	stdout, err := exec.Stdout().Contents(ctx)
	if err != nil {
		return nil, err
	}
	fmt.Println(stdout)

	exit, err := exec.ExitCode(ctx)
	if err != nil {
		return nil, err
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": utils.NewFS(dagger.DirectoryID(fsid)),
		"exit":   exit,
	})
}

func parseCommon(pctx *plancontext.Context, v *compiler.Value) (*execCommon, error) {
	e := &execCommon{
		hosts: make(map[string]string),
	}

	// root
	// input, err := pctx.FS.FromValue(v.Lookup("input"))
	// if err != nil {
	// 	return nil, err
	// }
	// e.root = input

	// fsid, err := pctx.FS.GetId(v.Lookup("input"))
	fsid, err := utils.GetFSId(v.Lookup("input"))

	if err != nil {
		return nil, err
	}
	e.FSID = fsid

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
	FSID    dagger.DirectoryID
	root    *plancontext.FS
	args    []string
	workdir string
	user    string
	hosts   map[string]string
	mounts  []mount
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
		var mode string
		switch concurrency {
		case "shared":
			mode = concurrency
		case "private":
			mode = concurrency
		case "locked":
			mode = concurrency
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

	// case "socket":
	// 	socket, err := pctx.Sockets.FromValue(v.Lookup("contents"))
	// 	if err != nil {
	// 		return mount{}, err
	// 	}
	// 	return mount{dest: dest, socketMount: &socketMount{id: socket.ID()}}, nil

	case "fs":
		mnt := mount{
			dest:    dest,
			fsMount: &fsMount{},
		}

		// contents, err := pctx.FS.FromValue(v.Lookup("contents"))
		// if err != nil {
		// 	return mount{}, err
		// }
		// mnt.fsMount.contents = contents
		fsid, err := utils.GetFSId(v.Lookup("contents"))
		if err != nil {
			return mount{}, err
		}
		mnt.fsMount.fsid = fsid

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
		secretID, err := utils.GetSecretId(v.Lookup("contents"))
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
				id:   string(secretID),
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

// func (m mount) runOpt() (llb.RunOption, error) {
// 	switch {
// 	case m.cacheMount != nil:
// 		return llb.AddMount(
// 			m.dest,
// 			llb.Scratch(),
// 			llb.AsPersistentCacheDir(m.cacheMount.id, m.cacheMount.concurrency),
// 		), nil
// 	case m.tmpMount != nil:
// 		// FIXME: handle size
// 		return llb.AddMount(
// 			m.dest,
// 			llb.Scratch(),
// 			llb.Tmpfs(),
// 		), nil
// 	case m.socketMount != nil:
// 		return llb.AddSSHSocket(
// 			llb.SSHID(m.socketMount.id),
// 			llb.SSHSocketTarget(m.dest),
// 		), nil
// 	case m.fsMount != nil:
// 		st, err := m.fsMount.contents.State()
// 		if err != nil {
// 			return nil, err
// 		}
// 		var opts []llb.MountOption
// 		if m.fsMount.source != "" {
// 			opts = append(opts, llb.SourcePath(m.fsMount.source))
// 		}
// 		if m.fsMount.readonly {
// 			opts = append(opts, llb.Readonly)
// 		}
// 		return llb.AddMount(
// 			m.dest,
// 			st,
// 			opts...,
// 		), nil
// 	case m.secretMount != nil:
// 		return llb.AddSecret(
// 			m.dest,
// 			llb.SecretID(m.secretMount.id),
// 			llb.SecretFileOpt(int(m.secretMount.uid), int(m.secretMount.gid), int(m.secretMount.mask)),
// 		), nil
// 	case m.fileMount != nil:
// 		return llb.AddMount(
// 			m.dest,
// 			llb.Scratch().File(llb.Mkfile(
// 				"/file",
// 				fs.FileMode(m.fileMount.permissions),
// 				[]byte(m.fileMount.contents))),
// 			llb.SourcePath("/file"),
// 		), nil
// 	}
// 	return nil, fmt.Errorf("no mount type set")
// }

// func (m mount) containerMount() (client.Mount, error) {
// 	switch {
// 	case m.cacheMount != nil:
// 		mnt := client.Mount{
// 			Dest:      m.dest,
// 			MountType: pb.MountType_CACHE,
// 			CacheOpt: &pb.CacheOpt{
// 				ID: m.cacheMount.id,
// 			},
// 		}
// 		switch m.cacheMount.concurrency {
// 		case llb.CacheMountShared:
// 			mnt.CacheOpt.Sharing = pb.CacheSharingOpt_SHARED
// 		case llb.CacheMountPrivate:
// 			mnt.CacheOpt.Sharing = pb.CacheSharingOpt_PRIVATE
// 		case llb.CacheMountLocked:
// 			mnt.CacheOpt.Sharing = pb.CacheSharingOpt_LOCKED
// 		}
// 		return mnt, nil
// 	case m.tmpMount != nil:
// 		// FIXME: handle size
// 		return client.Mount{
// 			Dest:      m.dest,
// 			MountType: pb.MountType_TMPFS,
// 		}, nil
// 	case m.socketMount != nil:
// 		return client.Mount{
// 			Dest:      m.dest,
// 			MountType: pb.MountType_SSH,
// 			SSHOpt: &pb.SSHOpt{
// 				ID: m.socketMount.id,
// 			},
// 		}, nil
// 	case m.fsMount != nil:
// 		return client.Mount{
// 			Dest:      m.dest,
// 			MountType: pb.MountType_BIND,
// 			Ref:       m.fsMount.contents.Result(),
// 			Selector:  m.fsMount.source,
// 			Readonly:  m.fsMount.readonly,
// 		}, nil
// 	case m.secretMount != nil:
// 		return client.Mount{
// 			Dest:      m.dest,
// 			MountType: pb.MountType_SECRET,
// 			SecretOpt: &pb.SecretOpt{
// 				ID:   m.secretMount.id,
// 				Uid:  m.secretMount.uid,
// 				Gid:  m.secretMount.gid,
// 				Mode: m.secretMount.mask,
// 			},
// 		}, nil
// 	}
// 	return client.Mount{}, fmt.Errorf("no mount type set")
// }

type cacheMount struct {
	id          string
	concurrency string
}

type tmpMount struct {
}

type socketMount struct {
	id string
}

type fsMount struct {
	contents *plancontext.FS
	fsid     dagger.DirectoryID
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
