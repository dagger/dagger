package core

import (
	"github.com/dagger/cloak/dagger"
	"github.com/moby/buildkit/client/llb"
)

//
//
//
//
// TODO: actually autogenerate core too from CUE, just handwritten still for now
//
//
//
//

type ImageInput struct {
	Ref string
}

type ImageOutput struct {
	fs FSOutput
}

func (i *ImageOutput) FS() FSOutput {
	return i.fs
}

/* TODO:
func (i *ImageOutput) Config() ImageConfig {}
*/

type GitRepoInput struct {
	Remote string
	Ref    string
}

type GitRepoOutput struct {
	fs FSOutput
}

func (g *GitRepoOutput) FS() FSOutput {
	return g.fs
}

type ExecInput struct {
	Base   FSOutput
	Dir    string
	Args   []string
	Mounts []Mount
}

type Mount struct {
	FS   FSOutput
	Path string
}

type ExecOutput struct {
	fs     FSOutput
	mounts map[string]FSOutput
}

func (e *ExecOutput) FS() FSOutput {
	return e.fs
}

func (e *ExecOutput) GetMount(path string) FSOutput {
	return e.mounts[path]
}

//
//
//
//
// AUTOGENERATED STOP
//
//
//
//

// temporarily just an alias for llb.State, in long term probably need more thorough wrapping type to
// fully hide llb from users
type FS llb.State

type FSOutput interface {
	fs() llb.State

	// Evaluate synchronously instantiates the filesystem, blocking until it is created
	// TODO: maybe more args to support caching config like "always" and maybe remote imports
	Evaluate(ctx *dagger.Context) error
}

func (fs FS) fs() llb.State {
	return llb.State(fs)
}

func (fs FS) Evaluate(ctx *dagger.Context) error {
	panic("TODO")
}

// TODO: FS.ReadFile and similar

func Image(i *ImageInput) *ImageOutput {
	return &ImageOutput{fs: FS(llb.Image(i.Ref))}
}

func GitRepo(i *GitRepoInput) *GitRepoOutput {
	return &GitRepoOutput{fs: FS(llb.Git(i.Remote, i.Ref))}
}

func Exec(i *ExecInput) *ExecOutput {
	exec := llb.State(i.Base.fs()).Run(
		llb.Dir(i.Dir),
		llb.Args(i.Args),
	)
	out := make(map[string]FSOutput)
	out["/"] = FS(exec.Root())
	for _, m := range i.Mounts {
		out[m.Path] = FS(exec.AddMount(m.Path, llb.State(m.FS.fs())))
	}
	return &ExecOutput{fs: FS(exec.Root()), mounts: out}
}
