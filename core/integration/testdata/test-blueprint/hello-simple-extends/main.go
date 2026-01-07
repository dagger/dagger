package main

type HelloSimpleExtends struct{}

// Override Message from base
func (m *HelloSimpleExtends) Message() string {
	return "extended: hello from simple"
}

// Goodbye is inherited from hello-simple base module
