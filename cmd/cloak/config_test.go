package main

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestGetConfigFSFile(t *testing.T) {
	cases := []struct {
		path string

		base    string
		content string
	}{
		{"testdata/dir/file.yaml", "file.yaml", "file in dir\n"},
		{"testdata/file.yml", "file.yml", "file in .\n"},
		{"../../cmd/cloak/testdata/dir/file.yaml", "file.yaml", "file in dir\n"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			dirFS, base := getConfigFS(c.path)
			require.Equal(t, c.base, base)

			b, err := fs.ReadFile(dirFS, base)
			require.NoError(t, err)
			require.Equal(t, c.content, string(b))
		})
	}
}

func TestGetConfigFSDir(t *testing.T) {
	cases := []struct {
		path string

		base     string
		filename string
	}{
		{"testdata/dir/", "dir", "file.yaml"},
		{"testdata/dir", "dir", "file.yaml"},
		{"testdata", "testdata", "file.yml"},
		{"testdata/", "testdata", "file.yml"},
		{"./testdata", "testdata", "file.yml"},
		{"./", ".", "config_test.go"},
		{".", ".", "config_test.go"},
		{"", ".", "config_test.go"},
		{"../../cmd/cloak/testdata/dir/", "dir", "file.yaml"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			dirFS, base := getConfigFS(c.path)
			require.Equal(t, c.base, base)

			de, err := fs.ReadDir(dirFS, base)
			require.NoError(t, err)

			found := false
			for _, f := range de {
				if f.Name() == c.filename {
					found = true
				}
			}
			require.True(t, found)
		})
	}
}

func TestGetCloakYAMLFilePath(t *testing.T) {
	testFS := fstest.MapFS{
		"yml/cloak.yml": &fstest.MapFile{
			Data: []byte(`name: yml`),
		},
		"yaml/cloak.yaml": &fstest.MapFile{
			Data: []byte(`name: yaml`),
		},
		"dir/cloak.yaml": &fstest.MapFile{
			Data: []byte(`name: yaml`),
		},
		"youml/cloak.youml": &fstest.MapFile{
			Data: []byte(`name: youml`),
		},
		"noext/cloak": &fstest.MapFile{
			Data: []byte(`name: empty`),
		},
		"both/cloak.yml": &fstest.MapFile{
			Data: []byte(`name: yml`),
		},
		"both/cloak.yaml": &fstest.MapFile{
			Data: []byte(`name: yaml`),
		},
	}

	cases := map[string]struct {
		fs   fs.FS
		path string

		file string
		err  error
	}{
		"dir":            {testFS, "dir", "dir/cloak.yaml", nil},
		"yml":            {testFS, "yml", "yml/cloak.yml", nil},
		"yaml":           {testFS, "yaml", "yaml/cloak.yaml", nil},
		"youml":          {testFS, "youml", "", errNoCloakFile},
		"noext":          {testFS, "noext", "", errNoCloakFile},
		"both":           {testFS, "both", "both/cloak.yaml", nil},
		"file not found": {testFS, "fnf", "", &fs.PathError{}},
		"yml/cloak.yml":  {testFS, "yml/cloak.yml", "yml/cloak.yml", nil},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			file, err := getCloakYAMLFilePath(c.fs, c.path)
			if err != nil && err != errNoCloakFile {
				terr := c.err
				require.ErrorAs(t, err, &terr)
			} else {
				require.Equal(t, c.err, err)
			}
			require.Equal(t, c.file, file)
		})
	}
}
