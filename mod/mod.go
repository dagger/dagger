package mod

import (
	"context"
	"os"
	"path"
	"strings"

	"github.com/gofrs/flock"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/stdlib"
)

const (
	UniverseVersionConstraint = ">= 0.1.0, < 0.2"
)

func isUniverse(repoName string) bool {
	return strings.HasPrefix(strings.ToLower(repoName), stdlib.ModuleName)
}

func Install(ctx context.Context, workspace, repoName, versionConstraint string) (*Require, error) {
	lg := log.Ctx(ctx)

	if isUniverse(repoName) {
		// override versionConstraint to lock the version of universe we vendor
		versionConstraint = UniverseVersionConstraint
	}

	lg.Debug().Str("name", repoName).Msg("installing module")
	require, err := newRequire(repoName, versionConstraint)
	if err != nil {
		return nil, err
	}

	modfile, err := readPath(workspace)
	if err != nil {
		return nil, err
	}

	fileLockPath := path.Join(workspace, lockFilePath)
	fileLock := flock.New(fileLockPath)
	if err := fileLock.Lock(); err != nil {
		return nil, err
	}

	defer func() {
		fileLock.Unlock()
		os.Remove(fileLockPath)
	}()

	err = modfile.install(ctx, require)
	if err != nil {
		return nil, err
	}

	if err = modfile.write(); err != nil {
		return nil, err
	}

	return require, nil
}

func InstallAll(ctx context.Context, workspace string, repoNames []string) ([]*Require, error) {
	installedRequires := make([]*Require, 0, len(repoNames))
	var err error

	for _, repoName := range repoNames {
		var require *Require

		if require, err = Install(ctx, workspace, repoName, ""); err != nil {
			continue
		}

		installedRequires = append(installedRequires, require)
	}

	return installedRequires, err
}

func Update(ctx context.Context, workspace, repoName, versionConstraint string) (*Require, error) {
	lg := log.Ctx(ctx)

	if isUniverse(repoName) {
		// override versionConstraint to lock the version of universe we vendor
		versionConstraint = UniverseVersionConstraint
	}

	lg.Debug().Str("name", repoName).Msg("updating module")
	require, err := newRequire(repoName, versionConstraint)
	if err != nil {
		return nil, err
	}

	modfile, err := readPath(workspace)
	if err != nil {
		return nil, err
	}

	fileLockPath := path.Join(workspace, lockFilePath)
	fileLock := flock.New(fileLockPath)
	if err := fileLock.Lock(); err != nil {
		return nil, err
	}

	defer func() {
		fileLock.Unlock()
		os.Remove(fileLockPath)
	}()

	updatedRequire, err := modfile.updateToLatest(ctx, require)
	if err != nil {
		return nil, err
	}

	if err = modfile.write(); err != nil {
		return nil, err
	}

	return updatedRequire, nil
}

func UpdateAll(ctx context.Context, workspace string, repoNames []string) ([]*Require, error) {
	updatedRequires := make([]*Require, 0, len(repoNames))
	var err error

	for _, repoName := range repoNames {
		var require *Require

		if require, err = Update(ctx, workspace, repoName, ""); err != nil {
			continue
		}

		updatedRequires = append(updatedRequires, require)
	}

	return updatedRequires, err
}

func UpdateInstalled(ctx context.Context, workspace string) ([]*Require, error) {
	modfile, err := readPath(workspace)
	if err != nil {
		return nil, err
	}

	repoNames := make([]string, 0, len(modfile.requires))

	for _, require := range modfile.requires {
		repoNames = append(repoNames, require.String())
	}

	return UpdateAll(ctx, workspace, repoNames)
}

func Ensure(workspace string) error {
	modfile, err := readPath(workspace)
	if err != nil {
		return err
	}

	return modfile.ensure()
}
