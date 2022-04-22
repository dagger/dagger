package mod

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
)

func TestDownload(t *testing.T) {
	cases := []struct {
		name    string
		require Require
		auth    string
	}{
		{
			name: "download archives",
			require: Require{
				cloneRepo: "https://github.com/dagger/dagger/archive/refs/tags/v0.2.7.tar.gz",
				clonePath: "",
				version:   "main",
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

			err = download(context.TODO(), &c.require, tmpDir, c.auth)
			if err != nil {
				t.Error(err)
			}
		})
	}
}
