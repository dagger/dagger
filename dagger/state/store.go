package state

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"dagger.io/go/dagger/keychain"
	"gopkg.in/yaml.v3"
)

var (
	ErrNotInit     = errors.New("not initialized")
	ErrAlreadyInit = errors.New("already initialized")
	ErrNotExist    = errors.New("environment doesn't exist")
	ErrExist       = errors.New("environment already exists")
)

const (
	daggerDir    = ".dagger"
	envDir       = "env"
	stateDir     = "state"
	planDir      = "plan"
	manifestFile = "values.yaml"
	computedFile = "computed.json"
)

type Workspace struct {
	Path string
}

func Init(ctx context.Context, dir string) (*Workspace, error) {
	root := path.Join(dir, daggerDir)
	if err := os.Mkdir(root, 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrAlreadyInit
		}
		return nil, err
	}
	if err := os.Mkdir(path.Join(root, envDir), 0755); err != nil {
		return nil, err
	}
	return &Workspace{
		Path: root,
	}, nil
}

func Open(ctx context.Context, dir string) (*Workspace, error) {
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

	return &Workspace{
		Path: root,
	}, nil
}

func Current(ctx context.Context) (*Workspace, error) {
	current, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// Walk every parent directory to find .dagger
	for {
		_, err := os.Stat(path.Join(current, daggerDir, envDir))
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

func (w *Workspace) envPath(name string) string {
	return path.Join(w.Path, daggerDir, envDir, name)
}

func (w *Workspace) List(ctx context.Context) ([]*State, error) {
	var (
		environments = []*State{}
		err          error
	)

	files, err := os.ReadDir(path.Join(w.Path, daggerDir, envDir))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		st, err := w.Get(ctx, f.Name())
		if err != nil {
			return nil, err
		}
		environments = append(environments, st)
	}

	return environments, nil
}

func (w *Workspace) Get(ctx context.Context, name string) (*State, error) {
	envPath, err := filepath.Abs(w.envPath(name))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(envPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotExist
		}
		return nil, err
	}

	manifest, err := os.ReadFile(path.Join(envPath, manifestFile))
	if err != nil {
		return nil, err
	}
	manifest, err = keychain.Decrypt(ctx, manifest)
	if err != nil {
		return nil, fmt.Errorf("unable to decrypt state: %w", err)
	}

	var st State
	if err := yaml.Unmarshal(manifest, &st); err != nil {
		return nil, err
	}
	st.Path = envPath
	st.Workspace = w.Path

	computed, err := os.ReadFile(path.Join(envPath, stateDir, computedFile))
	if err == nil {
		st.Computed = string(computed)
	}

	return &st, nil
}

func (w *Workspace) Save(ctx context.Context, st *State) error {
	data, err := yaml.Marshal(st)
	if err != nil {
		return err
	}

	manifestPath := path.Join(st.Path, manifestFile)

	encrypted, err := keychain.Reencrypt(ctx, manifestPath, data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, encrypted, 0600); err != nil {
		return err
	}

	if st.Computed != "" {
		state := path.Join(st.Path, stateDir)
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

func (w *Workspace) Create(ctx context.Context, name string) (*State, error) {
	envPath, err := filepath.Abs(w.envPath(name))
	if err != nil {
		return nil, err
	}

	// Environment directory
	if err := os.MkdirAll(envPath, 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrExist
		}
		return nil, err
	}

	// Plan directory
	if err := os.Mkdir(path.Join(envPath, planDir), 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrExist
		}
		return nil, err
	}

	manifestPath := path.Join(envPath, manifestFile)

	st := &State{
		Path:      envPath,
		Workspace: w.Path,
		Name:      name,
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
		path.Join(envPath, ".gitignore"),
		[]byte("# dagger state\nstate/**\n"),
		0600,
	)
	if err != nil {
		return nil, err
	}

	return st, nil
}
