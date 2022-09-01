package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

var errNoCloakFile = errors.New("no cloak.yaml or cloak.yml file found")

func getConfigFS(initConfigPath string) (fs.FS, string) {
	initConfigPath = filepath.Clean(initConfigPath)

	dir, base := filepath.Split(initConfigPath)
	if dir == "" {
		dir = "."
	}

	dirFS := os.DirFS(dir)

	return dirFS, base
}

func getCloakYAMLFilePath(dir fs.FS, baseConfigPath string) (string, error) {
	fi, err := fs.Stat(dir, baseConfigPath)
	if err != nil {
		return "", err
	}

	if !fi.IsDir() {
		return baseConfigPath, nil
	}

	sub, err := fs.Sub(dir, baseConfigPath)
	if err != nil {
		return "", err
	}

	matches, err := fs.Glob(sub, "cloak.y*ml")
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return "", errNoCloakFile
	}

	found := matches[0]

	// since fs.Glob is limited in its pattern
	// we make sure we found cloak.y{a,}ml and not cloak.youml, cloak.yiml, etc.
	if found != "cloak.yaml" &&
		found != "cloak.yml" {
		return "", errNoCloakFile
	}

	full := filepath.Join(baseConfigPath, found)

	return full, nil
}
