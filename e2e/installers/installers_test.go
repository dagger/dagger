// Package installers contains e2e contract tests for installer scripts.
//
//go:test:include ../../install.sh
//go:test:include ../../install.ps1
package installers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/containerd/platforms"
	"golang.org/x/mod/semver"
)

func TestBashScript(t *testing.T) {
	ctx := t.Context()

	client, err := dagger.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to dagger: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close dagger client: %v", err)
		}
	})

	installScript := client.CurrentWorkspace().
		Directory("/", dagger.WorkspaceDirectoryOpts{Include: []string{"install.sh"}}).
		File("/install.sh")
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

// Every main publish overwrites the files in main/head one at a time. An
// install that runs in between can get the new head archive with the old head
// checksums.txt. Both installers must still install a consistent build.
func TestInstallHeadDuringPublish(t *testing.T) {
	ctx := t.Context()

	client, err := dagger.Connect(ctx)
	if err != nil {
		t.Fatalf("connect to dagger: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close dagger client: %v", err)
		}
	})

	const (
		oldCommit = "1111111111111111111111111111111111111111"
		newCommit = "2222222222222222222222222222222222222222"
		dlURL     = "http://dl:8000"
	)
	dl := client.Container().
		From("alpine").
		WithExec([]string{"apk", "add", "--no-cache", "python3", "zip"}).
		WithEnvVariable("OLD_COMMIT", oldCommit).
		WithEnvVariable("NEW_COMMIT", newCommit).
		WithExec([]string{"sh", "-ec", `
for commit in "$OLD_COMMIT" "$NEW_COMMIT"; do
	mkdir -p "/build/$commit" "/dist/$commit" "/srv/dagger/main/$commit"
	printf '#!/bin/sh\necho %s\n' "$commit" > "/build/$commit/dagger"
	chmod +x "/build/$commit/dagger"
	cp "/build/$commit/dagger" "/build/$commit/dagger.exe"
	for label in "$commit" head; do
		for arch in amd64 arm64 armv7; do
			tar -czf "/dist/$commit/dagger_${label}_linux_${arch}.tar.gz" -C "/build/$commit" dagger
		done
		(cd "/build/$commit" && zip -q "/dist/$commit/dagger_${label}_windows_amd64.zip" dagger.exe)
	done
	(cd "/dist/$commit" && sha256sum dagger_* > checksums.txt)
	cp "/dist/$commit/dagger_${commit}_"* "/dist/$commit/checksums.txt" "/srv/dagger/main/$commit/"
done
# a publish of NEW_COMMIT has replaced the head archives, but not yet the head checksums.txt
mkdir -p /srv/dagger/main/head
cp "/dist/$NEW_COMMIT/dagger_head_"* "/srv/dagger/main/head/"
cp "/dist/$OLD_COMMIT/checksums.txt" "/srv/dagger/main/head/"
echo 0.0.0 > /srv/dagger/latest_version
`}).
		WithWorkdir("/srv").
		WithExposedPort(8000).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{"python3", "-m", "http.server", "8000"}})

	// The installers download from dl.dagger.io; point them at the fake server.
	localScript := func(t *testing.T, path string) *dagger.File {
		t.Helper()
		contents, err := client.CurrentWorkspace().
			Directory("/", dagger.WorkspaceDirectoryOpts{Include: []string{path}}).
			File(path).
			Contents(ctx)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(contents, "https://dl.dagger.io") {
			t.Fatalf("%s does not download from https://dl.dagger.io", path)
		}
		contents = strings.ReplaceAll(contents, "https://dl.dagger.io", dlURL)
		return client.Directory().WithNewFile(path, contents).File(path)
	}

	t.Run("install.sh", func(t *testing.T) {
		t.Parallel()

		out, err := client.Container().
			From("alpine").
			WithExec([]string{"apk", "add", "--no-cache", "curl"}).
			WithServiceBinding("dl", dl).
			WithWorkdir("/opt/dagger").
			WithFile("/usr/local/bin/install.sh", localScript(t, "install.sh"), dagger.ContainerWithFileOpts{
				Permissions: 0755,
			}).
			WithEnvVariable("DAGGER_COMMIT", "head").
			WithExec([]string{"install.sh"}).
			WithExec([]string{"./bin/dagger"}).
			Stdout(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out); got != oldCommit {
			t.Fatalf("expected the build of %s, got %q", oldCommit, got)
		}
	})

	t.Run("install.ps1", func(t *testing.T) {
		t.Parallel()

		out, err := client.Container().
			From("mcr.microsoft.com/powershell:7.5-alpine-3.20").
			WithServiceBinding("dl", dl).
			WithFile("/install.ps1", localScript(t, "install.ps1")).
			WithEnvVariable("PROCESSOR_ARCHITECTURE", "AMD64").
			WithExec([]string{"pwsh", "-NoProfile", "-File", "/install.ps1", "-DaggerCommit", "head", "-InstallPath", "/opt/dagger"}).
			WithExec([]string{"cat", "/opt/dagger/dagger.exe"}).
			Stdout(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "echo "+oldCommit) {
			t.Fatalf("expected the build of %s, got %q", oldCommit, out)
		}
	})
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

	return checkDaggerVersionOutput(out, platform, assertVersion)
}

func checkDaggerVersionOutput(out string, platform dagger.Platform, assertVersion func(string) error) error {
	out = strings.TrimSpace(out)
	if strings.HasPrefix(out, "version:") {
		return checkDaggerVersionFields(out, platform, assertVersion)
	}

	// Installer tests exercise historical Dagger releases too. Those older
	// binaries still print the previous one-line `dagger version` format, so
	// keep this parser as compatibility coverage for installed old CLIs.
	fields := strings.Fields(out)
	if len(fields) < 4 {
		return fmt.Errorf("malformed dagger version output %q: expected at least 4 fields", out)
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

	gotPlatform := fields[3]
	return checkVersionPlatform(out, gotPlatform, platform)
}

func checkDaggerVersionFields(out string, platform dagger.Platform, assertVersion func(string) error) error {
	fields := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return fmt.Errorf("malformed dagger version output %q: expected key-value line %q", out, line)
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}

	version := fields["version"]
	if !semver.IsValid(version) {
		return fmt.Errorf("malformed dagger version output %q: expected version field %q to be valid semver", out, version)
	}
	if assertVersion != nil {
		if err := assertVersion(version); err != nil {
			return err
		}
	}

	if fields["runner-host"] == "" {
		return fmt.Errorf("malformed dagger version output %q: expected runner-host field", out)
	}

	gotPlatform := fields["platform"]
	if fields["commit"] == "" {
		return fmt.Errorf("malformed dagger version output %q: expected commit field", out)
	}
	if fields["dirty"] == "" {
		return fmt.Errorf("malformed dagger version output %q: expected dirty field", out)
	}

	return checkVersionPlatform(out, gotPlatform, platform)
}

func checkVersionPlatform(out, gotPlatform string, platform dagger.Platform) error {
	parsedGotPlatform, err := platforms.Parse(gotPlatform)
	if err != nil {
		return fmt.Errorf("malformed dagger version output %q: expected platform field %q to be valid: %w", out, gotPlatform, err)
	}
	parsedPlatform, err := platforms.Parse(string(platform))
	if err != nil {
		return fmt.Errorf("invalid container platform %q: %w", platform, err)
	}
	if !platforms.OnlyStrict(parsedPlatform).Match(parsedGotPlatform) {
		return fmt.Errorf("malformed dagger version output %q: expected platform field to match container platform %q", out, platform)
	}
	return nil
}

func TestCheckDaggerVersionOutputPlatformVariant(t *testing.T) {
	tests := []struct {
		name     string
		out      string
		platform dagger.Platform
		wantErr  string
	}{
		{
			name:     "arm64 v8 variant",
			out:      "dagger v0.20.6 (image://registry.dagger.io/engine:v0.20.6) linux/arm64/v8",
			platform: "linux/arm64",
		},
		{
			name:     "with commit state",
			out:      "dagger v0.20.6 (image://registry.dagger.io/engine:v0.20.6) linux/amd64 a33388f2+dirty",
			platform: "linux/amd64",
		},
		{
			name: "labeled output",
			out: `version:     v1.0.0
commit:      a33388f2
dirty:       yes
platform:    linux/amd64
runner-host: image://registry.dagger.io/engine:v1.0.0`,
			platform: "linux/amd64",
		},
		{
			name:     "compatible arm is not equal",
			out:      "dagger v0.20.6 (image://registry.dagger.io/engine:v0.20.6) linux/arm/v8",
			platform: "linux/arm64",
			wantErr:  "expected platform field to match container platform",
		},
		{
			name: "labeled compatible arm is not equal",
			out: `version:     v1.0.0
commit:      a33388f2
dirty:       no
platform:    linux/arm/v8
runner-host: image://registry.dagger.io/engine:v1.0.0`,
			platform: "linux/arm64",
			wantErr:  "expected platform field to match container platform",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkDaggerVersionOutput(test.out, test.platform, nil)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("check version output: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("expected error containing %q, got %q", test.wantErr, err)
			}
		})
	}
}
