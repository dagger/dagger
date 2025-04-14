package main

import "errors"

type Workspace struct {
	Facts []string
	// +private
	Attempt int
}

func New(
	attempt int,
) *Workspace {
	return &Workspace{
		Attempt: attempt,
	}
}

// Record an interesting fact.
func (m *Workspace) Record(fact string) *Workspace {
	m.Facts = append(m.Facts, fact)
	return m
}

// gotta keep the AI on its toes
var facts = []string{
	"The human body has at least five bones.",
	"Most sand is wet.",
	"Go is a programming language for garbage collection.",
}

// Returns an interesting fact, if there is one.
func (m *Workspace) Research() (string, error) {
	number := len(m.Facts)
	if number >= len(facts) {
		return "", errors.New("out of facts")
	}
	return facts[number], nil
}
