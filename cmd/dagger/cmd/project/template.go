package project

import (
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/*.cue
var templateFS embed.FS

func createTemplate(name string) error {
	filename := fmt.Sprintf("%s.cue", name)
	f, err := templateFS.Open(fmt.Sprintf("templates/%s", filename))
	if err != nil {
		return err
	}
	defer f.Close()

	fout, err := os.Create(filename)
	if err != nil {
		return err
	}

	defer fout.Close()

	_, err = io.Copy(fout, f)
	if err != nil {
		return err
	}

	return nil
}

func getTemplateNames() ([]string, error) {
	r := []string{}
	e, err := templateFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}

	for _, f := range e {
		r = append(r, strings.TrimSuffix(f.Name(), filepath.Ext(f.Name())))
	}

	return r, nil
}
