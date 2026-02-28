package core

import "testing"

func TestIsKnownDockerfileSyntaxFrontend(t *testing.T) {
	t.Parallel()

	for _, syntax := range []string{
		"docker/dockerfile:1",
		"docker/dockerfile:1.7",
		"docker/dockerfile-upstream:master",
		"docker.io/docker/dockerfile:1",
		"index.docker.io/docker/dockerfile:1",
		"moby/dockerfile:1",
		"docker.io/moby/dockerfile:1",
		"docker/dockerfile@sha256:1111111111111111111111111111111111111111111111111111111111111111",
	} {
		if !isKnownDockerfileSyntaxFrontend(syntax) {
			t.Fatalf("expected syntax %q to be recognized as known dockerfile frontend", syntax)
		}
	}
}

func TestIsKnownDockerfileSyntaxFrontendUnknown(t *testing.T) {
	t.Parallel()

	for _, syntax := range []string{
		"",
		"example.com/custom/frontend:1",
		"docker/dockerfil:1",
		"docker/dockerfilex:1",
		"custom/dockerfile:1",
	} {
		if isKnownDockerfileSyntaxFrontend(syntax) {
			t.Fatalf("expected syntax %q to be treated as unknown", syntax)
		}
	}
}
