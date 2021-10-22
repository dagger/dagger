package mod

import (
	"context"
	"path"

	"github.com/gofrs/flock"
	"github.com/rs/zerolog/log"
)

func Install(ctx context.Context, workspace, repoName, versionConstraint string) (*Require, error) {
	lg := log.Ctx(ctx)

	lg.Info().Str("name", repoName).Msg("installing module")
	require, err := newRequire(repoName, versionConstraint)
	if err != nil {
		return nil, err
	}

	modfile, err := readPath(workspace)
	if err != nil {
		return nil, err
	}

	fileLock := flock.New(path.Join(workspace, lockFilePath))
	if err := fileLock.Lock(); err != nil {
		return nil, err
	}

	err = modfile.install(require)
	if err != nil {
		return nil, err
	}

	if err = modfile.write(); err != nil {
		return nil, err
	}

	if err := fileLock.Unlock(); err != nil {
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

	lg.Info().Str("name", repoName).Msg("updating module")
	require, err := newRequire(repoName, versionConstraint)
	if err != nil {
		return nil, err
	}

	modfile, err := readPath(workspace)
	if err != nil {
		return nil, err
	}

	fileLock := flock.New(path.Join(workspace, lockFilePath))
	if err := fileLock.Lock(); err != nil {
		return nil, err
	}

	updatedRequire, err := modfile.updateToLatest(require)
	if err != nil {
		return nil, err
	}

	if err = modfile.write(); err != nil {
		return nil, err
	}

	if err := fileLock.Unlock(); err != nil {
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
