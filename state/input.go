package state

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"

	"go.dagger.io/dagger/compiler"
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

type Input struct {
	Dir    *dirInput    `yaml:"dir,omitempty"`
	Git    *gitInput    `yaml:"git,omitempty"`
	Secret *secretInput `yaml:"secret,omitempty"`
	Text   *textInput   `yaml:"text,omitempty"`
	JSON   *jsonInput   `yaml:"json,omitempty"`
	YAML   *yamlInput   `yaml:"yaml,omitempty"`
	File   *fileInput   `yaml:"file,omitempty"`
	Bool   *boolInput   `yaml:"bool,omitempty"`
	Socket *socketInput `yaml:"socket,omitempty"`
}

func (i Input) Compile(state *State) (*compiler.Value, error) {
	switch {
	case i.Dir != nil:
		return i.Dir.Compile(state)
	case i.Git != nil:
		return i.Git.Compile(state)
	case i.Text != nil:
		return i.Text.Compile(state)
	case i.Secret != nil:
		return i.Secret.Compile(state)
	case i.JSON != nil:
		return i.JSON.Compile(state)
	case i.YAML != nil:
		return i.YAML.Compile(state)
	case i.File != nil:
		return i.File.Compile(state)
	case i.Bool != nil:
		return i.Bool.Compile(state)
	case i.Socket != nil:
		return i.Socket.Compile(state)
	default:
		return nil, fmt.Errorf("input has not been set")
	}
}

// An input artifact loaded from a local directory
func DirInput(path string, include []string, exclude []string) Input {
	return Input{
		Dir: &dirInput{
			Path:    path,
			Include: include,
			Exclude: exclude,
		},
	}
}

type dirInput struct {
	Path    string   `yaml:"path,omitempty"`
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
}

func (dir dirInput) Compile(state *State) (*compiler.Value, error) {
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
	excludeLLB := []byte("[]")
	if len(dir.Exclude) > 0 {
		var err error
		excludeLLB, err = json.Marshal(dir.Exclude)
		if err != nil {
			return nil, err
		}
	}

	p := dir.Path
	if !filepath.IsAbs(p) {
		p = filepath.Clean(path.Join(state.Project, dir.Path))
	}
	if !strings.HasPrefix(p, state.Project) {
		return nil, fmt.Errorf("%q is outside the project", dir.Path)
	}
	// Check that directory exists
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return nil, fmt.Errorf("%q dir doesn't exist", dir.Path)
	}

	dirPath, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}

	state.Context.LocalDirs.Add(p)

	llb := fmt.Sprintf(
		`#up: [{do: "local", dir: %s, include: %s, exclude: %s}]`,
		dirPath,
		includeLLB,
		excludeLLB,
	)
	return compiler.Compile("", llb)
}

// An input artifact loaded from a git repository
type gitInput struct {
	Remote string `yaml:"remote,omitempty"`
	Ref    string `yaml:"ref,omitempty"`
	Dir    string `yaml:"dir,omitempty"`
}

func GitInput(remote, ref, dir string) Input {
	return Input{
		Git: &gitInput{
			Remote: remote,
			Ref:    ref,
			Dir:    dir,
		},
	}
}

func (git gitInput) Compile(_ *State) (*compiler.Value, error) {
	ref := "HEAD"
	if git.Ref != "" {
		ref = git.Ref
	}

	dir := ""
	if git.Dir != "" {
		dir = fmt.Sprintf(`,{do:"subdir", dir:"%s"}`, git.Dir)
	}

	return compiler.Compile("", fmt.Sprintf(
		`#up: [{do:"fetch-git", remote:"%s", ref:"%s"}%s]`,
		git.Remote,
		ref,
		dir,
	))
}

// An input value encoded as text
func TextInput(data string) Input {
	i := textInput(data)
	return Input{
		Text: &i,
	}
}

type textInput string

func (i textInput) Compile(_ *State) (*compiler.Value, error) {
	return compiler.Compile("", fmt.Sprintf("%q", i))
}

// A secret input value
func SecretInput(data string) Input {
	i := secretInput(data)
	return Input{
		Secret: &i,
	}
}

type secretInput string

func (i secretInput) Compile(st *State) (*compiler.Value, error) {
	secret := st.Context.Secrets.New(i.PlainText())
	return secret.Value(), nil
}

func (i secretInput) PlainText() string {
	return string(i)
}

// An input value encoded as Bool
func BoolInput(data string) Input {
	i := boolInput(data)
	return Input{
		Bool: &i,
	}
}

type boolInput string

func (i boolInput) Compile(_ *State) (*compiler.Value, error) {
	s := map[boolInput]struct{}{
		"true":  {},
		"false": {},
	}
	if _, ok := s[i]; ok {
		return compiler.DecodeJSON("", []byte(i))
	}
	return nil, fmt.Errorf("%q is not a valid boolean: <true|false>", i)
}

// An input value encoded as JSON
func JSONInput(data string) Input {
	i := jsonInput(data)
	return Input{
		JSON: &i,
	}
}

type jsonInput string

func (i jsonInput) Compile(_ *State) (*compiler.Value, error) {
	return compiler.DecodeJSON("", []byte(i))
}

// An input value encoded as YAML
func YAMLInput(data string) Input {
	i := yamlInput(data)
	return Input{
		YAML: &i,
	}
}

type yamlInput string

func (i yamlInput) Compile(_ *State) (*compiler.Value, error) {
	return compiler.DecodeYAML("", []byte(i))
}

func FileInput(data string) Input {
	return Input{
		File: &fileInput{
			Path: data,
		},
	}
}

type fileInput struct {
	Path string `yaml:"path,omitempty"`
}

func (i fileInput) Compile(_ *State) (*compiler.Value, error) {
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

// A socket input value
func SocketInput(data, socketType string) Input {
	i := socketInput{}

	switch socketType {
	case "npipe":
		i.Npipe = data
	case "unix":
		i.Unix = data
	}

	return Input{
		Socket: &i,
	}
}

type socketInput struct {
	Unix  string `json:"unix,omitempty" yaml:"unix,omitempty"`
	Npipe string `json:"npipe,omitempty" yaml:"npipe,omitempty"`
}

func (i socketInput) Compile(st *State) (*compiler.Value, error) {
	service := st.Context.Services.New(i.Unix, i.Npipe)
	return service.Value(), nil
}
