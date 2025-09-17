package main

func New(
	// +default="hello"
	message string,
) *Defaults {
	return &Defaults{
		Message: message,
	}
}

type Defaults struct {
	Message string
}

func (m *Defaults) Hello() string {
	return m.Message
}
