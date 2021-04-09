package dagger

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"cuelang.org/go/cue"

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
type InputType string

const (
	InputTypeDir    InputType = "dir"
	InputTypeGit    InputType = "git"
	InputTypeDocker InputType = "docker"
	InputTypeText   InputType = "text"
	InputTypeJSON   InputType = "json"
	InputTypeYAML   InputType = "yaml"
	InputTypeFile   InputType = "file"
	InputTypeEmpty  InputType = ""
)

type Input struct {
	Type InputType `json:"type,omitempty"`

	Dir    *dirInput    `json:"dir,omitempty"`
	Git    *gitInput    `json:"git,omitempty"`
	Docker *dockerInput `json:"docker,omitempty"`
	Text   *textInput   `json:"text,omitempty"`
	JSON   *jsonInput   `json:"json,omitempty"`
	YAML   *yamlInput   `json:"yaml,omitempty"`
	File   *fileInput   `json:"file,omitempty"`
}

func (i Input) Compile() (*compiler.Value, error) {
	switch i.Type {
	case InputTypeDir:
		return i.Dir.Compile()
	case InputTypeGit:
		return i.Git.Compile()
	case InputTypeDocker:
		return i.Docker.Compile()
	case InputTypeText:
		return i.Text.Compile()
	case InputTypeJSON:
		return i.JSON.Compile()
	case InputTypeYAML:
		return i.YAML.Compile()
	case InputTypeFile:
		return i.File.Compile()
	case "":
		return nil, fmt.Errorf("input has not been set")
	default:
		return nil, fmt.Errorf("unsupported input type: %s", i.Type)
	}
}

// An input artifact loaded from a local directory
func DirInput(path string, include []string) Input {
	// resolve absolute path
	path, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}

	return Input{
		Type: InputTypeDir,
		Dir: &dirInput{
			Path:    path,
			Include: include,
		},
	}
}

type dirInput struct {
	Path    string   `json:"path,omitempty"`
	Include []string `json:"include,omitempty"`
}

func (dir dirInput) Compile() (*compiler.Value, error) {
	// FIXME: serialize an intermediate struct, instead of generating cue source

	// json.Marshal([]string{}) returns []byte("null"), which wreaks havoc
	// in Cue because `null` is not a `[...string]`
	includeLLB := []byte("[]")
	if len(dir.Include) > 0 {
		var err error
		includeLLB, err = json.Marshal(dir.Include)
		if err != nil {
			return nil, err
		}
	}
	llb := fmt.Sprintf(
		`#up: [{do:"local",dir:"%s", include:%s}]`,
		dir.Path,
		includeLLB,
	)
	return compiler.Compile("", llb)
}

// An input artifact loaded from a git repository
type gitInput struct {
	Remote string `json:"remote,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Dir    string `json:"dir,omitempty"`
}

func GitInput(remote, ref, dir string) Input {
	return Input{
		Type: InputTypeGit,
		Git: &gitInput{
			Remote: remote,
			Ref:    ref,
			Dir:    dir,
		},
	}
}

func (git gitInput) Compile() (*compiler.Value, error) {
	ref := "HEAD"
	if git.Ref != "" {
		ref = git.Ref
	}

	return compiler.Compile("", fmt.Sprintf(
		`#up: [{do:"fetch-git", remote:"%s", ref:"%s"}]`,
		git.Remote,
		ref,
	))
}

// An input artifact loaded from a docker container
func DockerInput(ref string) Input {
	return Input{
		Type: InputTypeDocker,
		Docker: &dockerInput{
			Ref: ref,
		},
	}
}

type dockerInput struct {
	Ref string `json:"ref,omitempty"`
}

func (i dockerInput) Compile() (*compiler.Value, error) {
	panic("NOT IMPLEMENTED")
}

// An input value encoded as text
func TextInput(data string) Input {
	return Input{
		Type: InputTypeText,
		Text: &textInput{
			Data: data,
		},
	}
}

type textInput struct {
	Data string `json:"data,omitempty"`
}

func (i textInput) Compile() (*compiler.Value, error) {
	return compiler.Compile("", fmt.Sprintf("%q", i.Data))
}

// An input value encoded as JSON
func JSONInput(data string) Input {
	return Input{
		Type: InputTypeJSON,
		JSON: &jsonInput{
			Data: data,
		},
	}
}

type jsonInput struct {
	// Marshalled JSON data
	Data string `json:"data,omitempty"`
}

func (i jsonInput) Compile() (*compiler.Value, error) {
	return compiler.DecodeJSON("", []byte(i.Data))
}

// An input value encoded as YAML
func YAMLInput(data string) Input {
	return Input{
		Type: InputTypeYAML,
		YAML: &yamlInput{
			Data: data,
		},
	}
}

type yamlInput struct {
	// Marshalled YAML data
	Data string `json:"data,omitempty"`
}

func (i yamlInput) Compile() (*compiler.Value, error) {
	return compiler.DecodeYAML("", []byte(i.Data))
}

func FileInput(data string) Input {
	return Input{
		Type: InputTypeFile,
		File: &fileInput{
			Path: data,
		},
	}
}

type fileInput struct {
	Path string `json:"data,omitempty"`
}

func (i fileInput) Compile() (*compiler.Value, error) {
	data, err := ioutil.ReadFile(i.Path)
	if err != nil {
		return nil, err
	}
	value := compiler.NewValue()
	if err := value.FillPath(cue.MakePath(), data); err != nil {
		return nil, err
	}
	return value, nil
}
