package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/keychain"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/stdlib"
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

type Project struct {
	Path string
}

func Init(ctx context.Context, dir string) (*Project, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	daggerRoot := path.Join(root, daggerDir)
	if err := os.Mkdir(daggerRoot, 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrAlreadyInit
		}
		return nil, err
	}

	if err := os.Mkdir(path.Join(daggerRoot, envDir), 0755); err != nil {
		return nil, err
	}

	if err := vendorUniverse(ctx, root); err != nil {
		return nil, err
	}

	return &Project{
		Path: root,
	}, nil
}

func Open(ctx context.Context, dir string) (*Project, error) {
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

	return &Project{
		Path: root,
	}, nil
}

func Current(ctx context.Context) (*Project, error) {
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

func (w *Project) envPath(name string) string {
	return path.Join(w.Path, daggerDir, envDir, name)
}

func (w *Project) List(ctx context.Context) ([]*State, error) {
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
			// If the environment doesn't exist (e.g. no values.yaml, skip silently)
			if !errors.Is(err, ErrNotExist) {
				log.
					Ctx(ctx).
					Err(err).
					Str("name", f.Name()).
					Msg("failed to load environment")
			}
			continue
		}
		environments = append(environments, st)
	}

	return environments, nil
}

func (w *Project) Get(ctx context.Context, name string) (*State, error) {
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
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotExist
		}
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
	st.Context = plancontext.New()
	if platform := st.Platform; platform != "" {
		if err := st.Context.Platform.Set(platform); err != nil {
			return nil, err
		}
	}
	st.Path = envPath
	// FIXME: Backward compat: Support for old-style `.dagger/env/<name>/plan`
	if st.Plan.Module == "" {
		planPath := path.Join(envPath, planDir)
		if _, err := os.Stat(planPath); err == nil {
			planRelPath, err := filepath.Rel(w.Path, planPath)
			if err != nil {
				return nil, err
			}
			st.Plan.Module = planRelPath
		}
	}
	st.Project = w.Path

	computed, err := os.ReadFile(path.Join(envPath, stateDir, computedFile))
	if err == nil {
		st.Computed = string(computed)
	}

	return &st, nil
}

func (w *Project) Save(ctx context.Context, st *State) error {
	data, err := yaml.Marshal(st)
	if err != nil {
		return err
	}

	manifestPath := path.Join(st.Path, manifestFile)

	currentEncrypted, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	currentPlain, err := keychain.Decrypt(ctx, currentEncrypted)
	if err != nil {
		return fmt.Errorf("unable to decrypt state: %w", err)
	}

	// Only update the encrypted file if there were changes
	if !bytes.Equal(data, currentPlain) {
		encrypted, err := keychain.Reencrypt(ctx, manifestPath, data)
		if err != nil {
			return err
		}
		if err := os.WriteFile(manifestPath, encrypted, 0600); err != nil {
			return err
		}
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

func (w *Project) Create(ctx context.Context, name string, plan Plan, platform string) (*State, error) {
	if _, err := w.Get(ctx, name); err == nil {
		return nil, ErrExist
	}

	pkg, err := w.cleanPackageName(ctx, plan.Package)
	if err != nil {
		return nil, err
	}

	envPath, err := filepath.Abs(w.envPath(name))
	if err != nil {
		return nil, err
	}

	// Environment directory
	if err := os.MkdirAll(envPath, 0755); err != nil {
		return nil, err
	}

	manifestPath := path.Join(envPath, manifestFile)

	st := &State{
		Context: plancontext.New(),

		Path:    envPath,
		Project: w.Path,
		Plan: Plan{
			Package: pkg,
		},
		Name:     name,
		Platform: platform,
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

func (w *Project) cleanPackageName(ctx context.Context, pkg string) (string, error) {
	lg := log.
		Ctx(ctx).
		With().
		Str("package", pkg).
		Logger()

	if pkg == "" {
		return pkg, nil
	}

	// If the package is not a path, then it must be a domain (e.g. foo.bar/mypackage)
	if _, err := os.Stat(pkg); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		// Make sure the domain is in the correct form
		if !strings.Contains(pkg, ".") || !strings.Contains(pkg, "/") {
			return "", fmt.Errorf("invalid package %q", pkg)
		}

		return pkg, nil
	}

	p, err := filepath.Abs(pkg)
	if err != nil {
		lg.Error().Err(err).Msg("unable to resolve path")
		return "", err
	}

	if !strings.HasPrefix(p, w.Path) {
		lg.Fatal().Err(err).Msg("package is outside the project")
		return "", err
	}

	p, err = filepath.Rel(w.Path, p)
	if err != nil {
		lg.Fatal().Err(err).Msg("unable to resolve path")
		return "", err
	}

	if !strings.HasPrefix(p, ".") {
		p = "./" + p
	}

	return p, nil
}

func cueModInit(ctx context.Context, parentDir string) error {
	lg := log.Ctx(ctx)

	modDir := path.Join(parentDir, "cue.mod")
	if err := os.Mkdir(modDir, 0755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}

	modFile := path.Join(modDir, "module.cue")
	if _, err := os.Stat(modFile); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		lg.Debug().Str("mod", parentDir).Msg("initializing cue.mod")

		if err := os.WriteFile(modFile, []byte("module: \"\"\n"), 0600); err != nil {
			return err
		}
	}

	if err := os.Mkdir(path.Join(modDir, "usr"), 0755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	if err := os.Mkdir(path.Join(modDir, "pkg"), 0755); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}

	return nil
}

func vendorUniverse(ctx context.Context, p string) error {
	// ensure cue module is initialized
	if err := cueModInit(ctx, p); err != nil {
		return err
	}

	// add universe and lock file to `.gitignore`
	if err := os.WriteFile(
		path.Join(p, "cue.mod", "pkg", ".gitignore"),
		[]byte(fmt.Sprintf("# generated by dagger\n%s\ndagger.lock\n", stdlib.PackageName)),
		0600,
	); err != nil {
		return err
	}

	log.Ctx(ctx).Debug().Str("mod", p).Msg("vendoring universe")
	if err := stdlib.Vendor(ctx, p); err != nil {
		// FIXME(samalba): disabled install remote stdlib temporarily
		// if _, err := mod.Install(ctx, p, "alpha.dagger.io", ""); err != nil {
		return err
	}

	return nil
}
