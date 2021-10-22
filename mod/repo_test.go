package mod

import (
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
		{
			name: "dagger private repo",
			require: Require{
				cloneRepo: "github.com/dagger/test",
				clonePath: "",
				version:   "main",
			},
			privateKeyFile:     "./test-ssh-keys/id_ed25519_test",
			privateKeyPassword: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpDir, err := ioutil.TempDir("", "clone")
			if err != nil {
				t.Fatal("error creating tmp dir")
			}

			defer os.Remove(tmpDir)

			_, err = clone(&c.require, tmpDir, c.privateKeyFile, c.privateKeyPassword)
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

	r, err := clone(&Require{
		cloneRepo: "github.com/dagger/universe",
		clonePath: "stdlib",
		version:   "",
	}, tmpDir, "", "")
	if err != nil {
		t.Fatal(err)
	}

	tags, err := r.listTagVersions("")
	if err != nil {
		t.Error(err)
	}

	if len(tags) == 0 {
		t.Errorf("could not list repo tags")
	}
}
