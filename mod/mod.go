package mod

import (
	"path"

	"github.com/gofrs/flock"
)

func Install(workspace, repoName, versionConstraint string) (*Require, error) {
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

func InstallAll(workspace string, repoNames []string) ([]*Require, error) {
	installedRequires := make([]*Require, 0, len(repoNames))
	var err error

	for _, repoName := range repoNames {
		var require *Require

		if require, err = Install(workspace, repoName, ""); err != nil {
			continue
		}

		installedRequires = append(installedRequires, require)
	}

	return installedRequires, err
}

func Update(workspace, repoName, versionConstraint string) (*Require, error) {
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

func UpdateAll(workspace string, repoNames []string) ([]*Require, error) {
	updatedRequires := make([]*Require, 0, len(repoNames))
	var err error

	for _, repoName := range repoNames {
		var require *Require

		if require, err = Update(workspace, repoName, ""); err != nil {
			continue
		}

		updatedRequires = append(updatedRequires, require)
	}

	return updatedRequires, err
}

func UpdateInstalled(workspace string) ([]*Require, error) {
	modfile, err := readPath(workspace)
	if err != nil {
		return nil, err
	}

	repoNames := make([]string, 0, len(modfile.requires))

	for _, require := range modfile.requires {
		repoNames = append(repoNames, require.String())
	}

	return UpdateAll(workspace, repoNames)
}

func Ensure(workspace string) error {
	modfile, err := readPath(workspace)
	if err != nil {
		return err
	}

	return modfile.ensure()
}
