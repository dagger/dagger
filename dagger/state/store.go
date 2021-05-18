package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/go/dagger/keychain"
	"gopkg.in/yaml.v3"
)

var (
	ErrNotInit            = errors.New("not initialized")
	ErrAlreadyInit        = errors.New("already initialized")
	ErrNoCurrentWorkspace = errors.New("not in a git directory")
)

const (
	daggerDir    = ".dagger"
	stateDir     = "state"
	manifestFile = "values.yaml"
	computedFile = "computed.json"
)

func Init(ctx context.Context, dir, name string) (*State, error) {
	root := path.Join(dir, daggerDir)
	if err := os.Mkdir(root, 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrAlreadyInit
		}
		return nil, err
	}
	manifestPath := path.Join(dir, daggerDir, manifestFile)

	st := &State{
		Path: dir,
		Name: name,
	}
	data, err := yaml.Marshal(st)
	if err != nil {
		return nil, err
	}
	key, err := keychain.Default(ctx)
	if err != nil {
		return nil, err
	}
	encrypted, err := keychain.Encrypt(ctx, manifestPath, data, key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(manifestPath, encrypted, 0600); err != nil {
		return nil, err
	}

	err = os.WriteFile(
		path.Join(root, ".gitignore"),
		[]byte("# dagger state\nstate/**\n"),
		0600,
	)
	if err != nil {
		return nil, err
	}

	return st, nil
}

func Current(ctx context.Context) (*State, error) {
	current, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// Walk every parent directory to find .dagger
	for {
		_, err := os.Stat(path.Join(current, daggerDir))
		if err == nil {
			return Open(ctx, current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return nil, ErrNotInit
}

func Open(ctx context.Context, dir string) (*State, error) {
	_, err := os.Stat(path.Join(dir, daggerDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotInit
		}
		return nil, err
	}

	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path.Join(root, daggerDir, manifestFile))
	if err != nil {
		return nil, err
	}
	data, err = keychain.Decrypt(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("unable to decrypt state: %w", err)
	}

	var st State
	if err := yaml.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	st.Path = root

	computed, err := os.ReadFile(path.Join(root, daggerDir, stateDir, computedFile))
	if err == nil {
		st.Computed = string(computed)
	}

	return &st, nil
}

func Save(ctx context.Context, st *State) error {
	data, err := yaml.Marshal(st)
	if err != nil {
		return err
	}

	manifestPath := path.Join(st.Path, daggerDir, manifestFile)

	encrypted, err := keychain.Reencrypt(ctx, manifestPath, data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, encrypted, 0600); err != nil {
		return err
	}

	if st.Computed != "" {
		state := path.Join(st.Path, daggerDir, stateDir)
		if err := os.MkdirAll(state, 0755); err != nil {
			return err
		}
		err := os.WriteFile(
			path.Join(state, "computed.json"),
			[]byte(st.Computed),
			0600)
		if err != nil {
			return err
		}
	}

	return nil
}

func CurrentWorkspace(ctx context.Context) (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Walk every parent directory to find .dagger
	for {
		_, err := os.Stat(path.Join(current, ".git"))
		if err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return "", ErrNoCurrentWorkspace
}

func List(ctx context.Context, workspace string) ([]*State, error) {
	var (
		environments = []*State{}
		err          error
	)

	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}

	err = filepath.WalkDir(workspace, func(p string, info os.DirEntry, err error) error {
		// Ignore errors while we walk
		if err != nil {
			return nil
		}

		// Skip regular files
		if !info.IsDir() {
			return nil
		}

		// Skip non-dagger directories
		if info.Name() != daggerDir {
			// Caveat: limit traversal to a depth of 10 (arbitrary)
			relPath := strings.TrimPrefix(p, workspace)
			if strings.Count(relPath, string(os.PathSeparator)) > 10 {
				return filepath.SkipDir
			}

			// Otherwise, continue traversing
			return nil
		}

		st, err := Open(ctx, filepath.Dir(p))
		if err != nil {
			return err
		}
		environments = append(environments, st)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return environments, nil
}
