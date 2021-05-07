package state

// Contents of an environment serialized to a file
type State struct {
	// State path
	Path string `yaml:"-"`

	// Human-friendly environment name.
	// A environment may have more than one name.
	// FIXME: store multiple names?
	Name string `yaml:"name,omitempty"`

	// User Inputs
	Inputs []inputKV `yaml:"inputs,omitempty"`

	// Computed values
	Computed string `yaml:"-"`
}

type inputKV struct {
	Key   string `yaml:"key,omitempty"`
	Value Input  `yaml:"value,omitempty"`
}

// Cue module containing the environment plan
// The input's top-level artifact is used as a module directory.
func (s *State) PlanSource() Input {
	return DirInput(s.Path, []string{"*.cue", "cue.mod"})
}

func (s *State) SetInput(key string, value Input) error {
	for i, inp := range s.Inputs {
		if inp.Key != key {
			continue
		}
		// Remove existing inputs with the same key
		s.Inputs = append(s.Inputs[:i], s.Inputs[i+1:]...)
	}

	s.Inputs = append(s.Inputs, inputKV{Key: key, Value: value})
	return nil
}

// Remove all inputs at the given key, including sub-keys.
// For example RemoveInputs("foo.bar") will remove all inputs
//   at foo.bar, foo.bar.baz, etc.
func (s *State) RemoveInputs(key string) error {
	newInputs := make([]inputKV, 0, len(s.Inputs))
	for _, i := range s.Inputs {
		if i.Key == key {
			continue
		}
		newInputs = append(newInputs, i)
	}
	s.Inputs = newInputs

	return nil
}
