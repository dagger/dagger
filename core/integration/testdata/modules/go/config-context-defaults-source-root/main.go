package main

import "os"

type Test struct{}

func (m *Test) Fn() ([]string, error) {
	ents, err := os.ReadDir("/da-context")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ent := range ents {
		names = append(names, ent.Name())
	}
	return names, nil
}
