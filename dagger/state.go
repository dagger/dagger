package dagger

// Contents of an environment serialized to a file
type EnvironmentState struct {
	// Globally unique environment ID
	ID string `json:"id,omitempty"`

	// Human-friendly environment name.
	// A environment may have more than one name.
	// FIXME: store multiple names?
	Name string `json:"name,omitempty"`

	// Cue module containing the environment plan
	// The input's top-level artifact is used as a module directory.
	PlanSource Input `json:"plan,omitempty"`

	// User Inputs
	Inputs []inputKV `json:"inputs,omitempty"`

	// Computed values
	Computed string `json:"output,omitempty"`
}

type inputKV struct {
	Key   string `json:"key,omitempty"`
	Value Input  `json:"value,omitempty"`
}

func (s *EnvironmentState) SetInput(key string, value Input) error {
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
func (s *EnvironmentState) RemoveInputs(key string) error {
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
