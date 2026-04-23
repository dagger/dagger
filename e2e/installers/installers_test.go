// Package installers contains e2e contract tests for installer scripts.
//
// workspace:include ../../install.sh
package installers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"golang.org/x/mod/semver"
)

func TestBashScript(t *testing.T) {
	ctx := t.Context()

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		t.Fatalf("connect to dagger: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close dagger client: %v", err)
		}
	})

	installScriptPath, err := filepath.Abs("../../install.sh")
	if err != nil {
		t.Fatalf("resolve install.sh path: %v", err)
	}
	installScript := client.Host().File(installScriptPath)
	base := client.Container().
		From("alpine").
		WithExec([]string{"apk", "add", "--no-cache", "curl"}).
		WithWorkdir("/opt/dagger").
		WithFile("/usr/local/bin/install.sh", installScript, dagger.ContainerWithFileOpts{
			Permissions: 0755,
		})

	platform, err := base.Platform(ctx)
	if err != nil {
		t.Fatalf("resolve test container platform: %v", err)
	}

	tests := []struct {
		name          string
		env           map[string]string
		binaryPath    string
		assertVersion func(string) error
	}{
		{
			name:       "default install",
			binaryPath: "/opt/dagger/bin/dagger",
		},
		{
			name:       "install to custom BIN_DIR",
			env:        map[string]string{"BIN_DIR": "/opt/special-bin"},
			binaryPath: "/opt/special-bin/dagger",
		},
		{
			name:          "install exact DAGGER_VERSION vX.Y.Z",
			env:           map[string]string{"DAGGER_VERSION": "v0.16.1"},
			binaryPath:    "./bin/dagger",
			assertVersion: matchExactVersion("v0.16.1"),
		},
		{
			name:          "install minor DAGGER_VERSION vX.Y",
			env:           map[string]string{"DAGGER_VERSION": "v0.15"},
			binaryPath:    "./bin/dagger",
			assertVersion: matchExactVersion("v0.15.4"),
		},
		{
			name:          "install exact DAGGER_VERSION X.Y.Z without v",
			env:           map[string]string{"DAGGER_VERSION": "0.16.1"},
			binaryPath:    "./bin/dagger",
			assertVersion: matchExactVersion("v0.16.1"),
		},
		{
			name:          "install DAGGER_VERSION latest",
			env:           map[string]string{"DAGGER_VERSION": "latest"},
			binaryPath:    "./bin/dagger",
			assertVersion: isVersion(),
		},
		{
			name:          "install fixed DAGGER_COMMIT",
			env:           map[string]string{"DAGGER_COMMIT": "976cd0bf4be8d1cacbc3ee23a7ab057e8868ac2d"},
			binaryPath:    "./bin/dagger",
			assertVersion: matchExactVersion("v0.16.2-250227135944-976cd0bf4be8"),
		},
		{
			name:          "install DAGGER_COMMIT head",
			env:           map[string]string{"DAGGER_COMMIT": "head"},
			binaryPath:    "./bin/dagger",
			assertVersion: isVersion(),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctr := base
			for key, value := range test.env {
				ctr = ctr.WithEnvVariable(key, value)
			}
			ctr = ctr.WithExec([]string{"install.sh"})

			if err := checkDaggerVersion(t.Context(), ctr, test.binaryPath, platform, test.assertVersion); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func matchExactVersion(target string) func(string) error {
	return func(v string) error {
		if semver.Compare(target, v) != 0 {
			return fmt.Errorf("expected version %q to match %q", v, target)
		}
		return nil
	}
}

func isVersion() func(string) error {
	return func(v string) error {
		if !semver.IsValid(v) {
			return fmt.Errorf("expected version %q to be valid semver", v)
		}
		return nil
	}
}

func checkDaggerVersion(ctx context.Context, ctr *dagger.Container, path string, platform dagger.Platform, assertVersion func(string) error) error {
	out, err := ctr.
		WithExec([]string{path, "version"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	out = strings.TrimSpace(out)
	fields := strings.Fields(out)
	if len(fields) < 3 {
		return fmt.Errorf("malformed dagger version output %q: expected at least 3 fields", out)
	}
	if fields[0] != "dagger" {
		return fmt.Errorf("malformed dagger version output %q: expected first field to be %q", out, "dagger")
	}

	version := fields[1]
	if !semver.IsValid(version) {
		return fmt.Errorf("malformed dagger version output %q: expected second field %q to be valid semver", out, version)
	}
	if assertVersion != nil {
		if err := assertVersion(version); err != nil {
			return err
		}
	}

	gotPlatform := fields[len(fields)-1]
	if gotPlatform != string(platform) {
		return fmt.Errorf("malformed dagger version output %q: expected final field to match container platform %q", out, platform)
	}

	return nil
}
