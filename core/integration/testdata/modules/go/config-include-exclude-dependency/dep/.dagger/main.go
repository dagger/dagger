package main

import (
	"io/fs"
	"path/filepath"
)

type Dep struct{}

func (m *Dep) ContextDirectory() ([]string, error) {
	var files []string
	err := filepath.WalkDir("/src", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
