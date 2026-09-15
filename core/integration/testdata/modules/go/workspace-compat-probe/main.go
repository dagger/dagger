package main

type Probe struct {
	Value string
}

func New(
	// +optional
	value string,
) *Probe {
	return &Probe{Value: value}
}

func (m *Probe) Selected() string {
	return m.Value
}
