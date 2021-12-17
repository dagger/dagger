package task

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Exec", func() Task { return &execTask{} })
}

type execTask struct {
}

func (t execTask) Run(ctx context.Context, pctx *plancontext.Context, s solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	// Get input state
	input, err := pctx.FS.FromValue(v.Lookup("input"))
	if err != nil {
		return nil, err
	}
	st, err := input.Result().ToState()
	if err != nil {
		return nil, err
	}

	// Run
	opts, err := t.getRunOpts(v, pctx)
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
	fs := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
		"exit":   0,
	})
}

func (t execTask) getRunOpts(v *compiler.Value, pctx *plancontext.Context) ([]llb.RunOption, error) {
	opts := []llb.RunOption{}
	var cmd struct {
		Args   []string
		Always bool
	}

	if err := v.Decode(&cmd); err != nil {
		return nil, err
	}
	// args
	opts = append(opts, llb.Args(cmd.Args))

	// workdir
	workdir, err := v.Lookup("workdir").String()
	if err != nil {
		return nil, err
	}
	opts = append(opts, llb.Dir(workdir))

	// env
	envs, err := v.Lookup("env").Fields()
	if err != nil {
		return nil, err
	}
	for _, env := range envs {
		v, err := env.Value.String()
		if err != nil {
			return nil, err
		}
		opts = append(opts, llb.AddEnv(env.Label(), v))
	}

	// always?
	if cmd.Always {
		// FIXME: also disables persistent cache directories
		// There's an ongoing proposal that would fix this: https://github.com/moby/buildkit/issues/1213
		opts = append(opts, llb.IgnoreCache)
	}

	hosts, err := v.Lookup("hosts").Fields()
	if err != nil {
		return nil, err
	}
	for _, host := range hosts {
		s, err := host.Value.String()
		if err != nil {
			return nil, err
		}

		if err != nil {
			return nil, err
		}
		opts = append(opts, llb.AddExtraHost(host.Label(), net.ParseIP(s)))
	}

	user, err := v.Lookup("user").String()
	if err != nil {
		return nil, err
	}
	opts = append(opts, llb.User(user))

	// mounts
	mntOpts, err := t.mountAll(pctx, v.Lookup("mounts"))
	if err != nil {
		return nil, err
	}
	opts = append(opts, mntOpts...)

	// marker for status events
	// FIXME
	args := make([]string, 0, len(cmd.Args))
	for _, a := range cmd.Args {
		args = append(args, fmt.Sprintf("%q", a))
	}
	opts = append(opts, withCustomName(v, "Exec [%s]", strings.Join(args, ", ")))

	return opts, nil
}

func (t execTask) mountAll(pctx *plancontext.Context, mounts *compiler.Value) ([]llb.RunOption, error) {
	opts := []llb.RunOption{}
	fields, err := mounts.Fields()
	if err != nil {
		return nil, err
	}
	for _, mnt := range fields {
		dest, err := mnt.Value.Lookup("dest").String()
		if err != nil {
			return nil, err
		}
		o, err := t.mount(pctx, dest, mnt.Value)
		if err != nil {
			return nil, err
		}
		opts = append(opts, o)
	}
	return opts, err
}

func (t execTask) mount(pctx *plancontext.Context, dest string, mnt *compiler.Value) (llb.RunOption, error) {
	typ, err := mnt.Lookup("type").String()
	if err != nil {
		return nil, err
	}
	switch typ {
	case "cache":
		return t.mountCache(pctx, dest, mnt)
	case "tmp":
		return t.mountTmp(pctx, dest, mnt)
	case "service":
		return t.mountService(pctx, dest, mnt)
	case "fs":
		return t.mountFS(pctx, dest, mnt)
	case "secret":
		return t.mountSecret(pctx, dest, mnt)
	case "":
		return nil, errors.New("no mount type specified")
	default:
		return nil, fmt.Errorf("unsupported mount type %q", typ)
	}
}

func (t *execTask) mountTmp(_ *plancontext.Context, dest string, _ *compiler.Value) (llb.RunOption, error) {
	// FIXME: handle size
	return llb.AddMount(
		dest,
		llb.Scratch(),
		llb.Tmpfs(),
	), nil
}

func (t *execTask) mountCache(_ *plancontext.Context, dest string, mnt *compiler.Value) (llb.RunOption, error) {
	contents := mnt.Lookup("contents")
	id, err := contents.Lookup("id").String()
	if err != nil {
		return nil, err
	}

	concurrency, err := mnt.Lookup("concurrency").String()
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("unknown concurrency mode %q", concurrency)
	}

	return llb.AddMount(
		dest,
		llb.Scratch(),
		llb.AsPersistentCacheDir(
			id,
			mode,
		),
	), nil
}

func (t *execTask) mountFS(pctx *plancontext.Context, dest string, mnt *compiler.Value) (llb.RunOption, error) {
	contents, err := pctx.FS.FromValue(mnt.Lookup("contents"))
	if err != nil {
		return nil, err
	}

	// possibly construct mount options for LLB from
	var mo []llb.MountOption

	// handle "path" option
	if source := mnt.Lookup("source"); source.Exists() {
		src, err := source.String()
		if err != nil {
			return nil, err
		}
		mo = append(mo, llb.SourcePath(src))
	}

	// FIXME: handle readonly
	// if readonly := mnt.Lookup("ro"); readonly.Exists() {
	// 	ro, err := readonly.Cue().Bool()
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// }

	st, err := contents.Result().ToState()
	if err != nil {
		return nil, err
	}

	return llb.AddMount(dest, st, mo...), nil
}

func (t *execTask) mountSecret(pctx *plancontext.Context, dest string, mnt *compiler.Value) (llb.RunOption, error) {
	contents, err := pctx.Secrets.FromValue(mnt.Lookup("contents"))
	if err != nil {
		return nil, err
	}

	// FIXME: handle uid, gid, optional
	return llb.AddSecret(dest,
		llb.SecretID(contents.ID()),
		llb.SecretFileOpt(0, 0, 0400), // uid, gid, mask)
	), nil
}

func (t *execTask) mountService(pctx *plancontext.Context, dest string, mnt *compiler.Value) (llb.RunOption, error) {
	contents, err := pctx.Services.FromValue(mnt.Lookup("contents"))
	if err != nil {
		return nil, err
	}

	return llb.AddSSHSocket(
		llb.SSHID(contents.ID()),
		llb.SSHSocketTarget(dest),
	), nil
}
