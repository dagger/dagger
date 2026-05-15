package main

type Test struct{}

type X struct {
	Message string `json:"message"`
}

func (m *Test) MyFunction() X {
	return X{Message: "foo"}
}
