package core

import (
	"github.com/dagger/cloak/dagger"
)

type ImageInput struct {
	Ref dagger.String `json:"ref,omitempty"`
}

type ImageOutput struct {
	FS dagger.FS `json:"fs,omitempty"`
}

type ImageResult struct {
	res dagger.Result
}

func (o ImageResult) MarshalJSON() ([]byte, error) {
	return o.res.MarshalJSON()
}

func (o *ImageResult) UnmarshalJSON(b []byte) error {
	return o.res.UnmarshalJSON(b)
}

func (o *ImageResult) FS() dagger.FS {
	return o.res.GetField("fs").FS()
}

func Image(ctx *dagger.Context, input *ImageInput) *ImageResult {
	result, err := dagger.Do(ctx, "localhost:5555/dagger:core", "image", input)
	if err != nil {
		panic(err)
	}
	return &ImageResult{res: *result}
}

type GitInput struct {
	Remote dagger.String `json:"remote,omitempty"`

	Ref dagger.String `json:"ref,omitempty"`
}

type GitOutput struct {
	FS dagger.FS `json:"fs,omitempty"`
}

type GitResult struct {
	res dagger.Result
}

func (o *GitResult) MarshalJSON() ([]byte, error) {
	return o.res.MarshalJSON()
}

func (o *GitResult) UnmarshalJSON(b []byte) error {
	return o.res.UnmarshalJSON(b)
}

func (o *GitResult) FS() dagger.FS {
	return o.res.GetField("fs").FS()
}

func Git(ctx *dagger.Context, input *GitInput) *GitResult {
	result, err := dagger.Do(ctx, "localhost:5555/dagger:core", "git", input)
	if err != nil {
		panic(err)
	}
	return &GitResult{res: *result}
}

type ExecInput struct {
	FS dagger.FS `json:"fs,omitempty"`

	Dir dagger.String `json:"dir,omitempty"`

	Args []dagger.String `json:"args,omitempty"`

	// TODO: map of path->FS would be better, but dagger.String can't be a map key...
	Mounts []Mount `json:"mounts,omitempty"`
}

type Mount struct {
	Path dagger.String `json:"path,omitempty"`
	FS   dagger.FS     `json:"fs,omitempty"`
}

type ExecOutput struct {
	FS dagger.FS `json:"fs,omitempty"`

	// TODO: support mounts again (need to figure out how to (de)serialize)
	// Mounts []Mount `json:"mounts,omitempty"`
}

type ExecResult struct {
	res dagger.Result
}

func (o *ExecResult) MarshalJSON() ([]byte, error) {
	return o.res.MarshalJSON()
}

func (o *ExecResult) UnmarshalJSON(b []byte) error {
	return o.res.UnmarshalJSON(b)
}

func (o *ExecResult) FS() dagger.FS {
	return o.res.GetField("fs").FS()
}

func Exec(ctx *dagger.Context, input *ExecInput) *ExecResult {
	result, err := dagger.Do(ctx, "localhost:5555/dagger:core", "exec", input)
	if err != nil {
		panic(err)
	}
	return &ExecResult{res: *result}
}
