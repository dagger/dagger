package dagger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"

	"dagger.cloud/go/dagger/cc"
)

type Op struct {
	v *cc.Value
}

func NewOp(v *cc.Value) (*Op, error) {
	final, err := spec.Get("#Op").Merge(v)
	if err != nil {
		return nil, errors.Wrap(err, "invalid op")
	}
	return newOp(final)
}

// Same as newOp, but without spec merge + validation.
func newOp(v *cc.Value) (*Op, error) {
	// Exists() appears to be buggy, is it needed here?
	if !v.Exists() {
		return nil, ErrNotExist
	}
	return &Op{
		v: v,
	}, nil
}

func (op *Op) Execute(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	action, err := op.Action()
	if err != nil {
		return fs, err
	}
	return action(ctx, fs, out)
}

func (op *Op) Walk(ctx context.Context, fn func(*Op) error) error {
	lg := log.Ctx(ctx)

	lg.Debug().Interface("v", op.v).Msg("Op.Walk")
	switch op.Do() {
	case "copy", "load":
		if from, err := newExecutable(op.Get("from")); err == nil {
			if err := from.Walk(ctx, fn); err != nil {
				return err
			}
		}
		// FIXME: we tolerate "from" which is not executable
	case "exec":
		return op.Get("mount").RangeStruct(func(k string, v *cc.Value) error {
			if from, err := newExecutable(op.Get("from")); err == nil {
				if err := from.Walk(ctx, fn); err != nil {
					return err
				}
			}
			return nil
		})
	}
	// depth first
	return fn(op)
}

type Action func(context.Context, FS, *Fillable) (FS, error)

func (op *Op) Do() string {
	do, err := op.Get("do").String()
	if err != nil {
		return ""
	}
	return do
}

func (op *Op) Action() (Action, error) {
	// An empty struct is allowed as a no-op, to be
	//  more tolerant of if() in arrays.
	if op.v.IsEmptyStruct() {
		return op.Nothing, nil
	}
	switch op.Do() {
	case "copy":
		return op.Copy, nil
	case "exec":
		return op.Exec, nil
	case "export":
		return op.Export, nil
	case "fetch-container":
		return op.FetchContainer, nil
	case "fetch-git":
		return op.FetchGit, nil
	case "local":
		return op.Local, nil
	case "load":
		return op.Load, nil
	case "subdir":
		return op.Subdir, nil
	default:
		return nil, fmt.Errorf("invalid operation: %s", op.v.JSON())
	}
}

func (op *Op) Subdir(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	// FIXME: this could be more optimized by carrying subdir path as metadata,
	//  and using it in copy, load or mount.

	dir, err := op.Get("dir").String()
	if err != nil {
		return fs, err
	}
	return fs.Change(func(st llb.State) llb.State {
		return st.File(llb.Copy(
			fs.LLB(),
			dir,
			"/",
			&llb.CopyInfo{
				CopyDirContentsOnly: true,
			},
		))
	}), nil
}

func (op *Op) Copy(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	// Decode copy options
	src, err := op.Get("src").String()
	if err != nil {
		return fs, err
	}
	dest, err := op.Get("dest").String()
	if err != nil {
		return fs, err
	}
	from, err := newExecutable(op.Get("from"))
	if err != nil {
		return fs, errors.Wrap(err, "from")
	}
	// Compute source component or script, discarding fs writes & output value
	fromFS, err := from.Execute(ctx, fs.Solver().Scratch(), nil)
	if err != nil {
		return fs, err
	}
	return fs.Change(func(st llb.State) llb.State {
		return st.File(llb.Copy(
			fromFS.LLB(),
			src,
			dest,
			// FIXME: allow more configurable llb options
			// For now we define the following convenience presets:
			&llb.CopyInfo{
				CopyDirContentsOnly: true,
				CreateDestPath:      true,
				AllowWildcard:       true,
			},
		))
	}), nil // lazy solve
}

func (op *Op) Nothing(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	return fs, nil
}
func (op *Op) Local(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	dir, err := op.Get("dir").String()
	if err != nil {
		return fs, err
	}
	var include []string
	if err := op.Get("include").Decode(&include); err != nil {
		return fs, err
	}
	return fs.Set(llb.Local(dir, llb.FollowPaths(include))), nil // lazy solve
}

func (op *Op) Exec(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	opts := []llb.RunOption{}
	var cmd struct {
		Args   []string
		Env    map[string]string
		Dir    string
		Always bool
	}

	if err := op.v.Decode(&cmd); err != nil {
		return fs, err
	}
	// marker for status events
	// FIXME
	opts = append(opts, llb.WithCustomName(op.v.Path().String()))
	// args
	opts = append(opts, llb.Args(cmd.Args))
	// dir
	opts = append(opts, llb.Dir(cmd.Dir))
	// env
	for k, v := range cmd.Env {
		opts = append(opts, llb.AddEnv(k, v))
	}
	// always?
	if cmd.Always {
		cacheBuster, err := randomID(8)
		if err != nil {
			return fs, err
		}
		opts = append(opts, llb.AddEnv("DAGGER_CACHEBUSTER", cacheBuster))
	}
	// mounts
	if mounts := op.v.Lookup("mount"); mounts.Exists() {
		if err := mounts.RangeStruct(func(k string, v *cc.Value) error {
			mnt, err := newMount(v, k)
			if err != nil {
				return err
			}
			opt, err := mnt.LLB(ctx, fs.Solver())
			if err != nil {
				return err
			}
			opts = append(opts, opt)
			return nil
		}); err != nil {
			return fs, err
		}
	}
	// --> Execute
	return fs.Change(func(st llb.State) llb.State {
		return st.Run(opts...).Root()
	}), nil // lazy solve
}

func (op *Op) Export(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	source, err := op.Get("source").String()
	if err != nil {
		return fs, err
	}
	format, err := op.Get("format").String()
	if err != nil {
		return fs, err
	}
	contents, err := fs.ReadFile(ctx, source)
	if err != nil {
		return fs, errors.Wrapf(err, "export %s", source)
	}
	switch format {
	case "string":
		log.
			Ctx(ctx).
			Debug().
			Bytes("contents", contents).
			Msg("exporting string")

		if err := out.Fill(string(contents)); err != nil {
			return fs, err
		}
	case "json":
		var o interface{}
		o, err := unmarshalAnything(contents, json.Unmarshal)
		if err != nil {
			return fs, err
		}

		log.
			Ctx(ctx).
			Debug().
			Interface("contents", o).
			Msg("exporting json")

		if err := out.Fill(o); err != nil {
			return fs, err
		}
	case "yaml":
		var o interface{}
		o, err := unmarshalAnything(contents, yaml.Unmarshal)
		if err != nil {
			return fs, err
		}

		log.
			Ctx(ctx).
			Debug().
			Interface("contents", o).
			Msg("exporting yaml")

		if err := out.Fill(o); err != nil {
			return fs, err
		}
	default:
		return fs, fmt.Errorf("unsupported export format: %q", format)
	}
	return fs, nil
}

type unmarshaller func([]byte, interface{}) error

func unmarshalAnything(data []byte, fn unmarshaller) (interface{}, error) {
	// unmarshalling a map into interface{} yields an error:
	// "unsupported Go type for map key (interface {})"
	// we want to attempt to unmarshal to a map[string]interface{} first
	var oMap map[string]interface{}
	if err := fn(data, &oMap); err == nil {
		return oMap, nil
	}

	// If the previous attempt didn't work, we might be facing a scalar (e.g.
	// bool).
	// Try to unmarshal to interface{} directly.
	var o interface{}
	err := fn(data, &o)
	return o, err
}

func (op *Op) Load(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	from, err := newExecutable(op.Get("from"))
	if err != nil {
		return fs, errors.Wrap(err, "load")
	}
	fromFS, err := from.Execute(ctx, fs.Solver().Scratch(), nil)
	if err != nil {
		return fs, errors.Wrap(err, "load: compute source")
	}
	return fs.Set(fromFS.LLB()), nil
}

func (op *Op) FetchContainer(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	ref, err := op.Get("ref").String()
	if err != nil {
		return fs, err
	}
	return fs.Set(llb.Image(ref)), nil
}
func (op *Op) FetchGit(ctx context.Context, fs FS, out *Fillable) (FS, error) {
	remote, err := op.Get("remote").String()
	if err != nil {
		return fs, err
	}
	ref, err := op.Get("ref").String()
	if err != nil {
		return fs, err
	}
	return fs.Set(llb.Git(remote, ref)), nil // lazy solve
}

func (op *Op) Get(target string) *cc.Value {
	return op.v.Get(target)
}
