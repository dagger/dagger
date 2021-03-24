package dagger

import (
	"encoding/json"
	"fmt"

	"dagger.io/go/dagger/compiler"
)

// An input is a value or artifact supplied by the user.
//
// - A value is any structured data which can be encoded as cue.
//
// - An artifact is a piece of data, like a source code checkout,
//   binary bundle, docker image, database backup etc.
//
//   Artifacts can be passed as inputs, generated dynamically from
//   other inputs, and received as outputs.
//   Under the hood, an artifact is encoded as a LLB pipeline, and
//   attached to the cue configuration as a
//
type Input interface {
	// Compile to a cue value which can be merged into a route config
	Compile() (*compiler.Value, error)
}

// An input artifact loaded from a local directory
func DirInput(path string, include []string) Input {
	return &dirInput{
		Type:    "dir",
		Path:    path,
		Include: include,
	}
}

type dirInput struct {
	Type    string
	Path    string
	Include []string
}

func (dir dirInput) Compile() (*compiler.Value, error) {
	// FIXME: serialize an intermediate struct, instead of generating cue source
	includeLLB, err := json.Marshal(dir.Include)
	if err != nil {
		return nil, err
	}
	llb := fmt.Sprintf(
		`[{do:"local",dir:"%s",include:%s}]`,
		dir.Path,
		includeLLB,
	)
	return compiler.Compile("", llb)
}

// An input artifact loaded from a git repository
type gitInput struct {
	Type   string
	Remote string
	Ref    string
	Dir    string
}

func GitInput(remote, ref, dir string) Input {
	return &gitInput{
		Type:   "git",
		Remote: remote,
		Ref:    ref,
		Dir:    dir,
	}
}

func (git gitInput) Compile() (*compiler.Value, error) {
	panic("NOT IMPLEMENTED")
}

// An input artifact loaded from a docker container
func DockerInput(ref string) Input {
	return &dockerInput{
		Type: "docker",
		Ref:  ref,
	}
}

type dockerInput struct {
	Type string
	Ref  string
}

func (i dockerInput) Compile() (*compiler.Value, error) {
	panic("NOT IMPLEMENTED")
}

// An input value encoded as text
func TextInput(data string) Input {
	return &textInput{
		Type: "text",
		Data: data,
	}
}

type textInput struct {
	Type string
	Data string
}

func (i textInput) Compile() (*compiler.Value, error) {
	panic("NOT IMPLEMENTED")
}

// An input value encoded as JSON
func JSONInput(data string) Input {
	return &jsonInput{
		Type: "json",
		Data: data,
	}
}

type jsonInput struct {
	Type string
	// Marshalled JSON data
	Data string
}

func (i jsonInput) Compile() (*compiler.Value, error) {
	panic("NOT IMPLEMENTED")
}

// An input value encoded as YAML
func YAMLInput(data string) Input {
	return &yamlInput{
		Type: "yaml",
		Data: data,
	}
}

type yamlInput struct {
	Type string
	// Marshalled YAML data
	Data string
}

func (i yamlInput) Compile() (*compiler.Value, error) {
	panic("NOT IMPLEMENTED")
}
