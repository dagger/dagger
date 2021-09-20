package state

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/ioutil"
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
	Docker *dockerInput `yaml:"docker,omitempty"`
	Secret *secretInput `yaml:"secret,omitempty"`
	Text   *textInput   `yaml:"text,omitempty"`
	JSON   *jsonInput   `yaml:"json,omitempty"`
	YAML   *yamlInput   `yaml:"yaml,omitempty"`
	File   *fileInput   `yaml:"file,omitempty"`
	Bool   *boolInput   `yaml:"bool,omitempty"`
	Socket *socketInput `yaml:"socket,omitempty"`
}

func (i Input) Compile(key string, state *State) (*compiler.Value, error) {
	switch {
	case i.Dir != nil:
		return i.Dir.Compile(key, state)
	case i.Git != nil:
		return i.Git.Compile(key, state)
	case i.Docker != nil:
		return i.Docker.Compile(key, state)
	case i.Text != nil:
		return i.Text.Compile(key, state)
	case i.Secret != nil:
		return i.Secret.Compile(key, state)
	case i.JSON != nil:
		return i.JSON.Compile(key, state)
	case i.YAML != nil:
		return i.YAML.Compile(key, state)
	case i.File != nil:
		return i.File.Compile(key, state)
	case i.Bool != nil:
		return i.Bool.Compile(key, state)
	case i.Socket != nil:
		return i.Socket.Compile(key, state)
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

func (dir dirInput) Compile(_ string, state *State) (*compiler.Value, error) {
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
		p = filepath.Clean(path.Join(state.Workspace, dir.Path))
	}
	if !strings.HasPrefix(p, state.Workspace) {
		return nil, fmt.Errorf("%q is outside the workspace", dir.Path)
	}

	llb := fmt.Sprintf(
		`#up: [{do:"local",dir:"%s", include:%s, exclude:%s}]`,
		p,
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

func (git gitInput) Compile(_ string, _ *State) (*compiler.Value, error) {
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

// An input artifact loaded from a docker container
func DockerInput(ref string) Input {
	return Input{
		Docker: &dockerInput{
			Ref: ref,
		},
	}
}

type dockerInput struct {
	Ref string `yaml:"ref,omitempty"`
}

func (i dockerInput) Compile(_ string, _ *State) (*compiler.Value, error) {
	panic("NOT IMPLEMENTED")
}

// An input value encoded as text
func TextInput(data string) Input {
	i := textInput(data)
	return Input{
		Text: &i,
	}
}

type textInput string

func (i textInput) Compile(_ string, _ *State) (*compiler.Value, error) {
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

func (i secretInput) Compile(key string, _ *State) (*compiler.Value, error) {
	hash := sha256.New()
	hash.Write([]byte(key))
	checksum := hash.Sum([]byte(i.PlainText()))
	secretValue := fmt.Sprintf(`{id:"secret=%s;hash=%x"}`, key, checksum)
	return compiler.Compile("", secretValue)
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

func (i boolInput) Compile(_ string, _ *State) (*compiler.Value, error) {
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

func (i jsonInput) Compile(_ string, _ *State) (*compiler.Value, error) {
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

func (i yamlInput) Compile(_ string, _ *State) (*compiler.Value, error) {
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
	Path string `json:"data,omitempty"`
}

func (i fileInput) Compile(_ string, _ *State) (*compiler.Value, error) {
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
func SocketInput(data string) Input {
	i := socketInput{
		Unix: data,
	}
	return Input{
		Socket: &i,
	}
}

type socketInput struct {
	Unix string `json:"unix,omitempty"`
}

func (i socketInput) Compile(_ string, _ *State) (*compiler.Value, error) {
	socketValue := fmt.Sprintf(`{unix: %q}`, i.Unix)
	return compiler.Compile("", socketValue)
}
