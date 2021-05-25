package state

// Contents of an environment serialized to a file
type State struct {
	// State path
	Path string `yaml:"-"`

	// Workspace path
	Workspace string `yaml:"-"`

	// Plan path
	Plan string `yaml:"-"`

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
// The input's top-level artifact is used as a module directory.
func (s *State) PlanSource() Input {
	return DirInput(s.Plan, []string{"*.cue", "cue.mod"})
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
