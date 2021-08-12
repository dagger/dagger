package mod

import (
	"io/ioutil"
	"os"
	"testing"
)

func TestClone(t *testing.T) {
	cases := []struct {
		name               string
		require            require
		privateKeyFile     string
		privateKeyPassword string
	}{
		{
			name: "resolving shorter hash version",
			require: require{
				cloneRepo: "github.com/tjovicic/gcpcloudrun-cue",
				clonePath: "",
				version:   "d530f2ea2099",
			},
		},
		{
			name: "resolving branch name",
			require: require{
				cloneRepo: "github.com/tjovicic/gcpcloudrun-cue",
				clonePath: "",
				version:   "main",
			},
		},
		{
			name: "resolving tag",
			require: require{
				cloneRepo: "github.com/tjovicic/gcpcloudrun-cue",
				clonePath: "",
				version:   "v0.3",
			},
		},
		{
			name: "Dagger private test repo",
			require: require{
				cloneRepo: "github.com/dagger/test",
				clonePath: "",
				version:   "v0.2",
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

	r, err := clone(&require{
		cloneRepo: "github.com/tjovicic/gcpcloudrun-cue",
		clonePath: "",
		version:   "",
	}, tmpDir, "", "")
	if err != nil {
		t.Error(err)
	}

	tags, err := r.listTags()
	if err != nil {
		t.Error(err)
	}

	if len(tags) == 0 {
		t.Errorf("could not list repo tags")
	}
}
