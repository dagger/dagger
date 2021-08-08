package mod

import (
	"io/ioutil"
	"os"
	"testing"
)

func TestClone(t *testing.T) {
	cases := []struct {
		name    string
		require require
	}{
		{
			name: "resolving shorter hash version",
			require: require{
				prefix:    "https://",
				cloneRepo: "github.com/tjovicic/gcpcloudrun-cue",
				clonePath: "",
				version:   "d530f2ea2099",
			},
		},
		{
			name: "resolving branch name",
			require: require{
				prefix:    "https://",
				cloneRepo: "github.com/tjovicic/gcpcloudrun-cue",
				clonePath: "",
				version:   "main",
			},
		},
		{
			name: "resolving tag",
			require: require{
				prefix:    "https://",
				cloneRepo: "github.com/tjovicic/gcpcloudrun-cue",
				clonePath: "",
				version:   "v0.3",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmpDir, err := ioutil.TempDir("", "clone")
			if err != nil {
				t.Fatal("error creating tmp dir")
			}

			defer os.Remove(tmpDir)

			_, err = clone(&c.require, tmpDir)
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
		prefix:    "https://",
		cloneRepo: "github.com/tjovicic/gcpcloudrun-cue",
		clonePath: "",
		version:   "",
	}, tmpDir)
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
