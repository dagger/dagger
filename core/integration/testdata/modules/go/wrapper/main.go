package main

type Wrapper struct{}

func (m *Wrapper) Container() *WrappedContainer {
	return &WrappedContainer{
		dag.Container().From("alpine"),
	}
}

type WrappedContainer struct {
	Unwrap *Container `json:"unwrap"`
}

func (c *WrappedContainer) Echo(msg string) *WrappedContainer {
	return &WrappedContainer{
		c.Unwrap.WithExec([]string{"echo", "-n", msg}),
	}
}
