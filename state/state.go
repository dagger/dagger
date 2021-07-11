package state

import (
	"context"
	"path"
)

// Contents of an environment serialized to a file
type State struct {
	// State path
	Path string `yaml:"-"`

	// Workspace path
	Workspace string `yaml:"-"`

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
func (s *State) Source() Input {
	w := s.Workspace
	// FIXME: backward compatibility
	if mod := s.Plan.Module; mod != "" {
		w = path.Join(w, mod)
	}
	return DirInput(w, []string{}, []string{})
}

// VendorUniverse vendors the latest (built-in) version of the universe into the
// environment's `cue.mod`.
// FIXME: This has nothing to do in `State` and should be tied to a `Workspace`.
// However, since environments could point to different modules before, we have
// to handle vendoring on a per environment basis.
func (s *State) VendorUniverse(ctx context.Context) error {
	w := s.Workspace
	// FIXME: backward compatibility
	if mod := s.Plan.Module; mod != "" {
		w = path.Join(w, mod)
	}

	return vendorUniverse(ctx, w)
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
