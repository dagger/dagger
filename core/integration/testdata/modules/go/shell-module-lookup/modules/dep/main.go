// Dependency module
package main

func New() *Dep {
	return &Dep{
		Version: "dep function",
	}
}

type Dep struct {
	// Dep version
	Version string
}
