package main

import (
	"crypto/sha256"
	"fmt"
)

type Person struct {
	Name string
	Job  string

	// +private
	Age int
}

func New(name string, job string, age int) *Person {
	return &Person{
		Name: name,
		Job:  job,
		Age:  age,
	}
}

// Get the identity of the person based on its personal information.
func (p *Person) Identity() string {
	str := fmt.Sprintf("%s-%s-%d", p.Name, p.Job, p.Age)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(str)))
}
