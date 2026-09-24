package main

import (
	"reflect"
	"testing"
)

func TestDirectEngineEnv(t *testing.T) {
	nested := []string{"DAGGER_SESSION_PORT=1234", "DAGGER_SESSION_TOKEN=private", "DAGGER_SESSION_CLIENT_ID=outer", "DAGGER_ENGINE_NUM_CPU=4", "DAGGER_SESSION_PORT=", "DAGGER_SESSION_TOKEN=other", "PATH=/bin", "DAGGER_CLOUD_TOKEN=cloud", "XDG_CONFIG_HOME=/config"}
	for _, suffix := range [][]string{nil, {"_EXPERIMENTAL_DAGGER_RUNNER_HOST="}, {"_EXPERIMENTAL_DAGGER_RUNNER_HOST=tcp://old:1234", "_EXPERIMENTAL_DAGGER_RUNNER_HOST="}} {
		env := append(append([]string{}, nested...), suffix...)
		if got := directEngineEnv(env); !reflect.DeepEqual(got, env) {
			t.Fatal("ordinary nesting environment changed")
		}
	}
	env := append(append([]string{}, nested...), "_EXPERIMENTAL_DAGGER_RUNNER_HOST=tcp://source:1234")
	before := append([]string{}, env...)
	want := []string{"PATH=/bin", "DAGGER_CLOUD_TOKEN=cloud", "XDG_CONFIG_HOME=/config", "_EXPERIMENTAL_DAGGER_RUNNER_HOST=tcp://source:1234"}
	if got := directEngineEnv(env); !reflect.DeepEqual(got, want) {
		t.Fatal("direct environment did not remove all routing keys or preserve unrelated authentication")
	}
	if !reflect.DeepEqual(env, before) {
		t.Fatal("input environment mutated")
	}
}
