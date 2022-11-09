package targets

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/dagger/dagger/magefiles/util"
	"github.com/magefile/mage/mg"
)

type Boot mg.Namespace

// Run runs a target with the bootstrap SDK
func (t Boot) Run(ctx context.Context, target string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	return t.run(ctx, target)
}

// Dev runs a target against a dev engine built with the bootstrap SDK
func (t Boot) Dev(ctx context.Context, target string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	err := t.run(ctx, "engine:dev")
	if err != nil {
		return err
	}

	mage := exec.Command("mage", target)
	mage.Env = append(os.Environ(), "DAGGER_HOST=docker-container://"+util.TestContainerName)
	mage.Stdout = os.Stdout
	mage.Stderr = os.Stderr
	return mage.Run()
}

func (t Boot) run(ctx context.Context, target string) error {
	repoRoot, err := os.Getwd()
	if err != nil {
		return err
	}

	magefiles := filepath.Join(repoRoot, "magefiles")
	for _, file := range []string{"go.mod", "go.sum"} {
		installedMod := filepath.Join(magefiles, file)

		err := os.Symlink(filepath.Join(magefiles, "mod", file), installedMod)
		if err != nil {
			return err
		}

		defer os.Remove(installedMod)
	}

	tmpBin := filepath.Join(os.TempDir(), "dagger-mage-bootstrap")

	compile := exec.CommandContext(ctx, "mage", "-compile", tmpBin)
	compile.Dir = magefiles
	compile.Stdout = os.Stdout
	compile.Stderr = os.Stderr
	if err := compile.Run(); err != nil {
		return err
	}

	mage := exec.CommandContext(ctx, tmpBin, target)
	mage.Stdout = os.Stdout
	mage.Stderr = os.Stderr
	return mage.Run()
}
