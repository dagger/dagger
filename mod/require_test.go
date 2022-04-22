package mod

import (
	"testing"
)

func assertRequire(in string, want, got *Require, t *testing.T) {
	if got.repo != want.repo {
		t.Errorf("repos differ %q: want %s, got %s", in, want.repo, got.repo)
	}

	if got.path != want.path {
		t.Errorf("paths differ %q: want %s, got %s", in, want.path, got.path)
	}

	if got.version != want.version {
		t.Errorf("versions differ (%q): want %s, got %s", in, want.version, got.version)
	}

	if got.sourcePath != want.sourcePath {
		t.Errorf("source paths differ (%q): want %s, got %s", in, want.sourcePath, got.sourcePath)
	}

	switch ws := want.source.(type) {
	case *GitRepoSource:
		gs, ok := got.source.(*GitRepoSource)
		if !ok {
			t.Errorf("source types differ (%q): want %T, got %T", in, want.source, got.source)
		}

		if gs.repo != ws.repo {
			t.Errorf("source repos differ (%q): want %s, got %s", in, ws.repo, gs.repo)
		}
	case *HTTPRepoSource:
		gs, ok := got.source.(*HTTPRepoSource)
		if !ok {
			t.Errorf("source types differ (%q): want %T, got %T", in, want.source, got.source)
		}

		if gs.repo != ws.repo {
			t.Errorf("source repos differ (%q): want %s, got %s", in, ws.repo, gs.repo)
		}
	case *GithubRepoSource:
		gs, ok := got.source.(*GithubRepoSource)
		if !ok {
			t.Errorf("source types differ (%q): want %T, got %T", in, want.source, got.source)
		}

		if gs.owner != ws.owner {
			t.Errorf("source owners differ (%q): want %s, got %s", in, ws.owner, gs.owner)
		}

		if gs.repo != ws.repo {
			t.Errorf("source repos differ (%q): want %s, got %s", in, ws.repo, gs.repo)
		}

		if gs.ref != ws.ref {
			t.Errorf("source refs differ (%q): want %s, got %s", in, ws.ref, gs.ref)
		}
	default:
		t.Errorf("unknown source type (%q): %T", in, want.source)
	}
}
