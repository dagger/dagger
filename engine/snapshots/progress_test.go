package snapshots

import "testing"

func TestDisplayRef(t *testing.T) {
	for in, want := range map[string]string{
		"docker.io/library/nginx:latest@sha256:beefdbd8a1da6d2915566fde36db9db0b524eb737fc57cd1367effd16dc0d06d": "nginx:latest",
		"docker.io/library/nginx@sha256:beefdbd8a1da6d2915566fde36db9db0b524eb737fc57cd1367effd16dc0d06d":        "nginx",
		"ghcr.io/dagger/engine:v0.18.6": "ghcr.io/dagger/engine:v0.18.6",
		"nginx":                         "nginx",
		"not a ref":                     "not a ref",
	} {
		if got := DisplayRef(in); got != want {
			t.Errorf("DisplayRef(%q) = %q, want %q", in, got, want)
		}
	}
}
