package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/tracing"
	"github.com/mitchellh/go-spdx"
)

const (
	defaultLicense = "Apache-2.0"
)

// TODO: dedupe this from Daggerverse, originally hoisted from pkg.go.dev
var licenseFiles = []string{
	"COPYING",
	"COPYING.md",
	"COPYING.markdown",
	"COPYING.txt",
	"LICENCE",
	"LICENCE.md",
	"LICENCE.markdown",
	"LICENCE.txt",
	"LICENSE",
	"LICENSE.md",
	"LICENSE.markdown",
	"LICENSE.txt",
	"LICENSE-2.0.txt",
	"LICENCE-2.0.txt",
	"LICENSE-APACHE",
	"LICENCE-APACHE",
	"LICENSE-APACHE-2.0.txt",
	"LICENCE-APACHE-2.0.txt",
	"LICENSE-MIT",
	"LICENCE-MIT",
	"LICENSE.MIT",
	"LICENCE.MIT",
	"LICENSE.code",
	"LICENCE.code",
	"LICENSE.docs",
	"LICENCE.docs",
	"LICENSE.rst",
	"LICENCE.rst",
	"MIT-LICENSE",
	"MIT-LICENCE",
	"MIT-LICENSE.md",
	"MIT-LICENCE.md",
	"MIT-LICENSE.markdown",
	"MIT-LICENCE.markdown",
	"MIT-LICENSE.txt",
	"MIT-LICENCE.txt",
	"MIT_LICENSE",
	"MIT_LICENCE",
	"UNLICENSE",
	"UNLICENCE",
}

func findOrCreateLicense(ctx context.Context, dir string) error {
	log := tracing.ContextLogger(ctx, slog.LevelWarn) // TODO

	id := licenseID
	if id == "" {
		if foundLicense, err := searchForLicense(dir); err == nil {
			log.Debug("found existing LICENSE file", "path", foundLicense)
			return nil
		}

		id = defaultLicense
	}

	log.Warn("no LICENSE file found; generating one for you, feel free to change or remove",
		"license", id)

	license, err := spdx.License(id)
	if err != nil {
		return fmt.Errorf("failed to get license: %w", err)
	}

	newLicense := filepath.Join(dir, "LICENSE")

	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(newLicense, []byte(license.Text), 0600); err != nil {
		return fmt.Errorf("failed to write license: %w", err)
	}

	return nil
}

func searchForLicense(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}

	for _, fileName := range licenseFiles {
		licensePath := filepath.Join(dir, fileName)
		if _, err := os.Stat(licensePath); err == nil {
			return licensePath, nil
		}
	}

	var atRoot bool
	if dir == "/" {
		atRoot = true
	} else if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		atRoot = true
	}

	if atRoot {
		// we reached the module root; time to give up
		return "", errors.New("not found")
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	return searchForLicense(filepath.Dir(abs))
}
