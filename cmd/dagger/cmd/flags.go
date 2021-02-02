package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"dagger.cloud/go/dagger"
	"github.com/spf13/pflag"
)

var (
	empty *dagger.Value
)

func init() {
	var err error
	empty, err = cc.Compile("", "")
	if err != nil {
		panic(err)
	}
}

type UserConfig struct {
	Name        string
	Description string
	Dir         bool
	DirInclude  []string
	Git         bool
	String      bool
	Cue         bool

	v *dagger.Value
}

func (cfg *UserConfig) Install(fl *pflag.FlagSet) {
	if cfg.Dir {
		fl.Var(
			// Value
			newFlagValue("TARGET=PATH", cfg.SetDir),
			// Name
			cfg.Name+"-dir",
			// Usage
			cfg.Description+" (local directory)",
		)
	}
	if cfg.Git {
		fl.Var(
			newFlagValue("TARGET=REMOTE#REF", cfg.SetGit),
			cfg.Name+"-git",
			cfg.Description+" (git repository)",
		)
	}
	if cfg.String {
		fl.Var(
			newFlagValue("TARGET=STRING", cfg.SetString),
			cfg.Name+"-string",
			cfg.Description+" (string value)",
		)
	}
	if cfg.Cue {
		fl.Var(
			newFlagValue("CUE", cfg.SetCue),
			cfg.Name+"-cue",
			cfg.Description+" (cue expression)",
		)
	}
}
func (cfg *UserConfig) Set(target string, v interface{}) error {
	if cfg.v == nil {
		cfg.v = empty
	}
	v2, err := cfg.v.MergeTarget(v, target)
	if err != nil {
		return err
	}
	cfg.v = v2
	return nil
}

func (cfg *UserConfig) SetString(target, s string) error {
	return cfg.Set(target, s)
}

func (cfg *UserConfig) SetCue(target string, src string) error {
	v, err := cc.Compile(cfg.Name, src)
	if err != nil {
		return err
	}
	return cfg.Set(target, v)
}

func (cfg *UserConfig) SetDir(target, dir string) error {
	// FIXME: this is a hack because cue API can't merge into a list
	incl, err := json.Marshal(cfg.DirInclude)
	if err != nil {
		return err
	}
	src := fmt.Sprintf(`#dagger: compute: [{do:"local",dir:"%s",include:%s}]`, dir, incl)
	return cfg.SetCue(target, src)
}

func (cfg *UserConfig) SetGit(target, gitinfo string) error {
	u, err := url.Parse(gitinfo)
	if err != nil {
		return fmt.Errorf("invalid git url")
	}
	ref := u.Fragment // eg. #main
	u.Fragment = ""
	remote := u.String()
	src := fmt.Sprintf(`#dagger: compute: [{do:"fetch-git",remote:"%s",ref:"%s"}]`, remote, ref)
	return cfg.SetCue(target, src)
}

func (cfg *UserConfig) Value() *dagger.Value {
	if cfg.v == nil {
		return empty
	}
	return cfg.v
}

func newFlagValue(typ string, set func(k, v string) error) *flagValue {
	return &flagValue{
		typ: typ,
		set: set,
	}
}

type flagValue struct {
	typ string
	set func(k, v string) error
	str string
}

func (fv *flagValue) Set(s string) error {
	fv.str = s
	k, v := splitkv(s)
	return fv.set(k, v)
}

func (fv flagValue) String() string {
	return fv.str
}

func (fv flagValue) Type() string {
	return fv.typ
}

func splitkv(kv string) (string, string) {
	parts := strings.SplitN(kv, "=", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	if len(parts) == 1 {
		return "", parts[0]
	}
	return "", ""
}
