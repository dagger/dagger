package dagger

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"cuelang.org/go/cue"
	"github.com/spf13/pflag"

	"dagger.io/go/dagger/compiler"
)

// A mutable cue value with an API suitable for user inputs,
// such as command-line flag parsing.
type InputValue struct {
	root *compiler.Value
}

func (iv *InputValue) Value() *compiler.Value {
	return iv.root
}

func (iv *InputValue) String() string {
	s, _ := iv.root.SourceString()
	return s
}

func NewInputValue(base interface{}) (*InputValue, error) {
	root, err := compiler.Compile("base", base)
	if err != nil {
		return nil, err
	}
	return &InputValue{
		root: root,
	}, nil
}

func (iv *InputValue) Set(s string, enc func(string) (interface{}, error)) error {
	// Split from eg. 'foo.bar={bla:"bla"}`
	k, vRaw := splitkv(s)
	v, err := enc(vRaw)
	if err != nil {
		return err
	}
	root, err := iv.root.MergePath(v, k)
	if err != nil {
		return err
	}
	iv.root = root
	return nil
}

// Adapter to receive string values from pflag
func (iv *InputValue) StringFlag() pflag.Value {
	return stringFlag{
		iv: iv,
	}
}

type stringFlag struct {
	iv *InputValue
}

func (sf stringFlag) Set(s string) error {
	return sf.iv.Set(s, func(s string) (interface{}, error) {
		return s, nil
	})
}

func (sf stringFlag) String() string {
	return sf.iv.String()
}

func (sf stringFlag) Type() string {
	return "STRING"
}

// DIR FLAG
// Receive a local directory path and translate it into a component
func (iv *InputValue) DirFlag(include ...string) pflag.Value {
	if include == nil {
		include = []string{}
	}
	return dirFlag{
		iv:      iv,
		include: include,
	}
}

type dirFlag struct {
	iv      *InputValue
	include []string
}

func (f dirFlag) Set(s string) error {
	return f.iv.Set(s, func(s string) (interface{}, error) {
		// FIXME: this is a hack because cue API can't merge into a list
		include, err := json.Marshal(f.include)
		if err != nil {
			return nil, err
		}
		return compiler.Compile("", fmt.Sprintf(
			`#run: [{do:"local",dir:"%s", include:%s}] @dagger()`,
			s,
			include,
		))
	})
}

func (f dirFlag) String() string {
	return f.iv.String()
}

func (f dirFlag) Type() string {
	return "PATH"
}

// GIT FLAG
// Receive a git repository reference and translate it into a component
func (iv *InputValue) GitFlag() pflag.Value {
	return gitFlag{
		iv: iv,
	}
}

type gitFlag struct {
	iv *InputValue
}

func (f gitFlag) Set(s string) error {
	return f.iv.Set(s, func(s string) (interface{}, error) {
		u, err := url.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid git url")
		}
		ref := u.Fragment // eg. #main
		u.Fragment = ""
		remote := u.String()

		return compiler.Compile("", fmt.Sprintf(
			`#dagger: compute: [{do:"fetch-git", remote:"%s", ref:"%s"}]`,
			remote,
			ref,
		))
	})
}

func (f gitFlag) String() string {
	return f.iv.String()
}

func (f gitFlag) Type() string {
	return "REMOTE,REF"
}

// SOURCE FLAG
// Adapter to receive a simple source description and translate it to a loader script.
// For example 'git+https://github.com/cuelang/cue#master` -> [{do:"git",remote:"https://github.com/cuelang/cue",ref:"master"}]

func (iv *InputValue) SourceFlag() pflag.Value {
	return sourceFlag{
		iv: iv,
	}
}

type sourceFlag struct {
	iv *InputValue
}

func (f sourceFlag) Set(s string) error {
	return f.iv.Set(s, func(s string) (interface{}, error) {
		u, err := url.Parse(s)
		if err != nil {
			return nil, err
		}
		switch u.Scheme {
		case "", "file":
			return compiler.Compile(
				"source",
				// FIXME: include only cue files as a shortcut. Make this configurable somehow.
				fmt.Sprintf(`[{do:"local",dir:"%s",include:["*.cue","cue.mod"]}]`, u.Host+u.Path),
			)
		default:
			return nil, fmt.Errorf("unsupported source scheme: %q", u.Scheme)
		}
	})
}

func (f sourceFlag) String() string {
	return f.iv.String()
}

func (f sourceFlag) Type() string {
	return "PATH | file://PATH | git+ssh://HOST/PATH | git+https://HOST/PATH"
}

// RAW CUE FLAG
// Adapter to receive raw cue values from pflag
func (iv *InputValue) CueFlag() pflag.Value {
	return cueFlag{
		iv: iv,
	}
}

type cueFlag struct {
	iv *InputValue
}

func (f cueFlag) Set(s string) error {
	return f.iv.Set(s, func(s string) (interface{}, error) {
		return compiler.Compile("cue input", s)
	})
}

func (f cueFlag) String() string {
	return f.iv.String()
}

func (f cueFlag) Type() string {
	return "CUE"
}

// UTILITIES

func splitkv(kv string) (cue.Path, string) {
	parts := strings.SplitN(kv, "=", 2)
	if len(parts) == 2 {
		if parts[0] == "." || parts[0] == "" {
			return cue.MakePath(), parts[1]
		}
		return cue.ParsePath(parts[0]), parts[1]
	}
	if len(parts) == 1 {
		return cue.MakePath(), parts[0]
	}
	return cue.MakePath(), ""
}
