package mod

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
)

func TestClone(t *testing.T) {
	cases := []struct {
		name               string
		require            Require
		privateKeyFile     string
		privateKeyPassword string
	}{
		{
			name: "resolving branch name",
			require: Require{
				cloneRepo: "github.com/dagger/universe",
				clonePath: "stdlib",
				version:   "main",
			},
		},
		{
			name: "resolving tag",
			require: Require{
				cloneRepo: "github.com/dagger/universe",
				clonePath: "stdlib",
				version:   "v0.1.0",
			},
		},
		// FIXME: disabled until we find a fix: "repo_test.go:56: ssh: handshake failed: knownhosts: key mismatch"
		// {
		// 	name: "dagger private repo",
		// 	require: Require{
		// 		cloneRepo: "github.com/dagger/test",
		// 		clonePath: "",
		// 		version:   "main",
		// 	},
		// 	privateKeyFile:     "./test-ssh-keys/id_ed25519_test",
		// 	privateKeyPassword: "",
		// },
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpDir, err := ioutil.TempDir("", "clone")
			if err != nil {
				t.Fatal("error creating tmp dir")
			}

			defer os.Remove(tmpDir)

			_, err = clone(context.TODO(), &c.require, tmpDir, c.privateKeyFile, c.privateKeyPassword)
			if err != nil {
				t.Error(err)
			}
		})
	}
}

func TestListTags(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "clone")
	if err != nil {
		t.Fatal("error creating tmp dir")
	}
	defer os.Remove(tmpDir)

	ctx := context.TODO()

	r, err := clone(ctx, &Require{
		cloneRepo: "github.com/dagger/universe",
		clonePath: "stdlib",
		version:   "",
	}, tmpDir, "", "")
	if err != nil {
		t.Fatal(err)
	}

	tags, err := r.listTagVersions(ctx, "")
	if err != nil {
		t.Error(err)
	}

	if len(tags) == 0 {
		t.Errorf("could not list repo tags")
	}
}

func TestVersionConstraint(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "clone")
	if err != nil {
		t.Fatal("error creating tmp dir")
	}
	defer os.Remove(tmpDir)

	ctx := context.TODO()

	r, err := clone(ctx, &Require{
		cloneRepo: "github.com/dagger/universe",
		clonePath: "stdlib",
		version:   "",
	}, tmpDir, "", "")
	if err != nil {
		t.Fatal(err)
	}

	tagVersion, err := r.latestTag(ctx, "<= 0.1.0")
	if err != nil {
		t.Error(err)
	}

	// Make sure we select the right version based on constraint
	if tagVersion != "v0.1.0" {
		t.Errorf("wrong version: expected 0.1.0, got %v", tagVersion)
	}

	// Make sure an invalid constraint (version out of range) returns an error
	_, err = r.latestTag(ctx, "> 99999")
	if err == nil {
		t.Error("selected wrong version based on constraint")
	}
}
