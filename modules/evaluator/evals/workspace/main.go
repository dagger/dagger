package main

import "errors"

type Workspace struct {
	Facts []string
}

func New(
	// A dummy arg just to prevent deduping telemetry across attempts.
	buster string,
) *Workspace {
	return &Workspace{}
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

// Find the next available fact.
func (m *Workspace) NextFact() (string, error) {
	number := len(m.Facts)
	if number >= len(facts) {
		return "", errors.New("out of facts")
	}
	return facts[number], nil
}
