package state

import (
	"context"
	"path"

	"cuelang.org/go/cue"
	"go.dagger.io/dagger/compiler"
)

// Contents of an environment serialized to a file
type State struct {
	// State path
	Path string `yaml:"-"`

	// Project path
	Project string `yaml:"-"`

	// Plan
	Plan Plan `yaml:"plan,omitempty"`

	// Human-friendly environment name.
	// A environment may have more than one name.
	// FIXME: store multiple names?
	Name string `yaml:"name,omitempty"`

	// User Inputs
	Inputs map[string]Input `yaml:"inputs,omitempty"`

	// Computed values
	Computed string `yaml:"-"`
}

// Cue module containing the environment plan
func (s *State) CompilePlan(ctx context.Context) (*compiler.Value, error) {
	w := s.Project
	// FIXME: backward compatibility
	if planModule := s.Plan.Module; planModule != "" {
		w = path.Join(w, planModule)
	}

	// FIXME: universe vendoring
	// This is already done on `dagger init` and shouldn't be done here too.
	// However:
	// 1) As of right now, there's no way to update universe through the
	// CLI, so we are lazily updating on `dagger up` using the embedded `universe`
	// 2) For backward compatibility: if the project was `dagger
	// init`-ed before we added support for vendoring universe, it might not
	// contain a `cue.mod`.
	if err := vendorUniverse(ctx, w); err != nil {
		return nil, err
	}

	var args []string
	if pkg := s.Plan.Package; pkg != "" {
		args = append(args, pkg)
	}

	return compiler.Build(w, nil, args...)
}

func (s *State) CompileInputs() (*compiler.Value, error) {
	v := compiler.NewValue()

	// Prepare inputs
	for key, input := range s.Inputs {
		i, err := input.Compile(key, s)
		if err != nil {
			return nil, err
		}
		if key == "" {
			err = v.FillPath(cue.MakePath(), i)
		} else {
			err = v.FillPath(cue.ParsePath(key), i)
		}
		if err != nil {
			return nil, err
		}
	}

	return v, nil
}

type Plan struct {
	Module  string `yaml:"module,omitempty"`
	Package string `yaml:"package,omitempty"`
}

func (s *State) SetInput(key string, value Input) error {
	if s.Inputs == nil {
		s.Inputs = make(map[string]Input)
	}
	s.Inputs[key] = value
	return nil
}

// Remove all inputs at the given key, including sub-keys.
// For example RemoveInputs("foo.bar") will remove all inputs
//   at foo.bar, foo.bar.baz, etc.
func (s *State) RemoveInputs(key string) error {
	delete(s.Inputs, key)
	return nil
}
