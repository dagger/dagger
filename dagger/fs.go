package dagger

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkpb "github.com/moby/buildkit/solver/pb"
	fstypes "github.com/tonistiigi/fsutil/types"

	"dagger.io/go/dagger/compiler"
)

type Stat struct {
	*fstypes.Stat
}

type FS struct {
	// Before last solve
	input llb.State
	// After last solve
	output bkgw.Reference
	// How to produce the output
	s Solver
}

func (fs FS) WriteValueJSON(filename string, v *compiler.Value) FS {
	return fs.Change(func(st llb.State) llb.State {
		return st.File(
			llb.Mkfile(filename, 0600, v.JSON()),
			llb.WithCustomName("[internal] serializing state to JSON"),
		)
	})
}

func (fs FS) WriteValueCUE(filename string, v *compiler.Value) (FS, error) {
	src, err := v.Source()
	if err != nil {
		return fs, err
	}
	return fs.Change(func(st llb.State) llb.State {
		return st.File(
			llb.Mkfile(filename, 0600, src),
			llb.WithCustomName("[internal] serializing state to CUE"),
		)
	}), nil
}

func (fs FS) Solver() Solver {
	return fs.s
}

// Compute output from input, if not done already.
//   This method uses a pointer receiver to simplify
//   calling it, since it is called in almost every
//   other method.
func (fs *FS) solve(ctx context.Context) error {
	if fs.output != nil {
		return nil
	}
	output, err := fs.s.Solve(ctx, fs.input)
	if err != nil {
		return bkCleanError(err)
	}
	fs.output = output
	return nil
}

func (fs FS) ReadFile(ctx context.Context, filename string) ([]byte, error) {
	// Lazy solve
	if err := (&fs).solve(ctx); err != nil {
		return nil, err
	}
	// NOTE: llb.Scratch is represented by a `nil` reference. If solve result is
	// Scratch, then `fs.output` is `nil`.
	if fs.output == nil {
		return nil, os.ErrNotExist
	}

	contents, err := fs.output.ReadFile(ctx, bkgw.ReadRequest{Filename: filename})
	if err != nil {
		return nil, bkCleanError(err)
	}
	return contents, nil
}

func (fs FS) ReadDir(ctx context.Context, dir string) ([]Stat, error) {
	// Lazy solve
	if err := (&fs).solve(ctx); err != nil {
		return nil, err
	}

	// NOTE: llb.Scratch is represented by a `nil` reference. If solve result is
	// Scratch, then `fs.output` is `nil`.
	if fs.output == nil {
		return []Stat{}, nil
	}
	st, err := fs.output.ReadDir(ctx, bkgw.ReadDirRequest{
		Path: dir,
	})
	if err != nil {
		return nil, bkCleanError(err)
	}
	out := make([]Stat, len(st))
	for i := range st {
		out[i] = Stat{
			Stat: st[i],
		}
	}
	return out, nil
}

func (fs FS) walk(ctx context.Context, p string, fn WalkFunc) error {
	files, err := fs.ReadDir(ctx, p)
	if err != nil {
		return err
	}
	for _, f := range files {
		fPath := path.Join(p, f.GetPath())
		if err := fn(fPath, f); err != nil {
			return err
		}
		if f.IsDir() {
			if err := fs.walk(ctx, fPath, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

type WalkFunc func(string, Stat) error

func (fs FS) Walk(ctx context.Context, fn WalkFunc) error {
	return fs.walk(ctx, "/", fn)
}

type ChangeFunc func(llb.State) llb.State

func (fs FS) Change(changes ...ChangeFunc) FS {
	for _, change := range changes {
		fs = fs.Set(change(fs.input))
	}
	return fs
}

func (fs FS) Set(st llb.State) FS {
	fs.input = st
	fs.output = nil
	return fs
}

func (fs FS) Solve(ctx context.Context) (FS, error) {
	if err := (&fs).solve(ctx); err != nil {
		return fs, err
	}
	return fs, nil
}

func (fs FS) LLB() llb.State {
	return fs.input
}

func (fs FS) Def(ctx context.Context) (*bkpb.Definition, error) {
	def, err := fs.LLB().Marshal(ctx, llb.LinuxAmd64)
	if err != nil {
		return nil, err
	}
	return def.ToPB(), nil
}

func (fs FS) Ref(ctx context.Context) (bkgw.Reference, error) {
	if err := (&fs).solve(ctx); err != nil {
		return nil, err
	}
	return fs.output, nil
}

func (fs FS) Result(ctx context.Context) (*bkgw.Result, error) {
	res := bkgw.NewResult()
	ref, err := fs.Ref(ctx)
	if err != nil {
		return nil, err
	}
	res.SetRef(ref)
	return res, nil
}

// A helper to remove noise from buildkit error messages.
// FIXME: Obviously a cleaner solution would be nice.
func bkCleanError(err error) error {
	noise := []string{
		"executor failed running ",
		"buildkit-runc did not terminate successfully",
		"rpc error: code = Unknown desc =",
		"failed to solve: ",
	}

	msg := err.Error()

	for _, s := range noise {
		msg = strings.ReplaceAll(msg, s, "")
	}

	return errors.New(msg)
}
