package core

import (
	"github.com/dagger/cloak/dagger"
)

type ImageInput struct {
	Ref string `json:"ref,omitempty"`
}

type ImageOutput struct {
	FS dagger.FS `json:"fs,omitempty"`
}

func Image(ctx *dagger.Context, input *ImageInput) *ImageOutput {
	fsInput, err := dagger.Marshal(ctx, input)
	if err != nil {
		panic(err)
	}

	fsOutput, err := dagger.Do(ctx, "localhost:5555/dagger:core", "image", fsInput)
	if err != nil {
		panic(err)
	}
	output := &ImageOutput{}
	if err := dagger.Unmarshal(ctx, fsOutput, output); err != nil {
		panic(err)
	}
	return output
}

type GitInput struct {
	Remote string `json:"remote,omitempty"`

	Ref string `json:"ref,omitempty"`
}

type GitOutput struct {
	FS dagger.FS `json:"fs,omitempty"`
}

func Git(ctx *dagger.Context, input *GitInput) *GitOutput {
	fsInput, err := dagger.Marshal(ctx, input)
	if err != nil {
		panic(err)
	}

	fsOutput, err := dagger.Do(ctx, "localhost:5555/dagger:core", "git", fsInput)
	if err != nil {
		panic(err)
	}
	output := &GitOutput{}
	if err := dagger.Unmarshal(ctx, fsOutput, output); err != nil {
		panic(err)
	}
	return output
}

type ExecInput struct {
	FS dagger.FS `json:"fs,omitempty"`

	Dir string `json:"dir,omitempty"`

	Args []string `json:"args,omitempty"`

	// TODO: cannot figure out how to parse this in the CUE lib:
	// mounts: [path=string]: "$daggerfs"
	Mounts map[string]dagger.FS `json:"mounts,omitempty"`
}

type ExecOutput struct {
	FS dagger.FS `json:"fs,omitempty"`

	// TODO: cannot figure out how to parse this in the CUE lib:
	// mounts: [path=string]: "$daggerfs"
	Mounts map[string]dagger.FS `json:"mounts,omitempty"`
}

func Exec(ctx *dagger.Context, input *ExecInput) *ExecOutput {
	fsInput, err := dagger.Marshal(ctx, input)
	if err != nil {
		panic(err)
	}

	fsOutput, err := dagger.Do(ctx, "localhost:5555/dagger:core", "exec", fsInput)
	if err != nil {
		panic(err)
	}
	output := &ExecOutput{}
	if err := dagger.Unmarshal(ctx, fsOutput, output); err != nil {
		panic(err)
	}
	return output
}
