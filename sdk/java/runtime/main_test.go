package main

import "testing"

func TestNewStyleFromConfig(t *testing.T) {
	cases := []struct {
		name string
		json string
		want bool
	}{
		{"automaticGitignore false => new-style", `{"codegen":{"automaticGitignore":false}}`, true},
		{"automaticGitignore true => legacy", `{"codegen":{"automaticGitignore":true}}`, false},
		{"flag absent => legacy", `{"sdk":{"source":"java"}}`, false},
		{"no codegen block => legacy", `{"name":"x"}`, false},
		{"garbage => legacy", `not json`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := newStyleFromConfig([]byte(c.json)); got != c.want {
				t.Fatalf("newStyleFromConfig(%s) = %v, want %v", c.json, got, c.want)
			}
		})
	}
}
