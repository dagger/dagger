package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/rust-sdk-dev/internal/dagger"
)

const (
	completenessContractPath  = "sdk/rust/completeness"
	completenessHarnessRev    = "8c164424b7a8a37b33a77367ef7547490d5b87b5"
	completenessCLIVersion    = "v1.0.0-beta.9"
	completenessCLIArchiveSHA = "776a390ecef59ff2ad8c0a3b3ca6d793bb62556bb8a512f475a725bdc830e40c"
	completenessCLIBinarySHA  = "e670234e6f8c0544e209423f8c42c8300e06cd9780921d19a9a22ef9e3890a40"
	completenessEngineImage   = "registry.dagger.io/engine:v1.0.0-beta.9@sha256:df96f6801fea0f511b1c62e143461af7daa6074216d62ea8f092e035c4afaffd"
)

// CompletenessIntegrity reconstructs the F1 contract from its pinned local inputs.
//
// +check
func (t *RustSdkDev) CompletenessIntegrity(ctx context.Context) error {
	freshGo := t.completenessGoOutputs()
	candidate := t.Workspace.WithDirectory(completenessContractPath+"/sources/go", freshGo)

	// Comparing before the overlay makes helper drift visible even when Rust would otherwise see
	// only the freshly normalized protocol. No raw Go source crosses into the Rust process.
	ctr := t.DevContainer(false).
		WithMountedDirectory("/fresh-go", freshGo).
		WithExec([]string{
			"diff", "-ruN",
			"/src/" + completenessContractPath + "/sources/go",
			"/fresh-go",
		}).
		WithMountedDirectory("/src", candidate).
		WithWorkdir("/src/sdk/rust").
		WithExec([]string{
			"sh", "-c",
			"cargo run -p dagger-sdk-completeness --bin dagger-sdk-completeness --locked -- verify --root /src --gate integrity --format json >/tmp/completeness-report.json",
		})
	_, err := ctr.Sync(ctx)
	return err
}

// CompletenessArtifacts captures the current engine schema and stages canonical derived files.
//
// The active workspace is immutable: callers receive only the Changeset between the original
// input and a graph-local candidate tree.
//
// +generate
func (t *RustSdkDev) CompletenessArtifacts() *dagger.Changeset {
	freshGo := t.completenessGoOutputs()
	candidate := t.Workspace.
		WithDirectory(completenessContractPath+"/sources/go", freshGo).
		WithFile(
			completenessContractPath+"/snapshots/schema.json",
			dag.DaggerEngine(dagger.DaggerEngineOpts{Ws: t.Ws}).IntrospectionJSON(),
		)
	rendered := t.DevContainer(false).
		WithMountedDirectory("/src", candidate).
		WithWorkdir("/src/sdk/rust").
		WithExec([]string{
			"cargo", "run", "-p", "dagger-sdk-completeness", "--bin", "dagger-sdk-completeness", "--locked", "--",
			"render", "--root", "/src", "--output", "/out",
		}).
		Directory("/out")
	after := candidate.WithDirectory(completenessContractPath, rendered)
	return after.Changes(t.Workspace)
}

// CompletenessHarness runs the exact pinned sdk-sdk baseline profile.
//
// Subject failures are captured as normalized outcomes and remain completeness blockers. Only
// acquisition, checksum, invocation, or normalization failures fail this callable operation.
func (t *RustSdkDev) CompletenessHarness() *dagger.File {
	harness := t.Workspace.Directory(fmt.Sprintf(
		"%s/sources/sdk-sdk/%s",
		completenessContractPath,
		completenessHarnessRev,
	))
	checks := []string{
		"install-registers-sdk",
		"install-marks-as-sdk",
		"init-scaffolds-module",
		"init-writes-module-config",
		"init-registers-module",
		"init-records-authoring-sdk",
		"generate-succeeds",
		"scaffolded-module-loads",
		"sdk-reports-module-options",
		"engine-required-reports-version",
		"deps-list-succeeds",
		"generate-respects-cwd",
		"init-module-seeds-files",
		"init-module-does-not-write-config",
		"init-module-does-not-remove-existing-files",
		"init-module-honors-custom-path",
		"generate-exposes-generator",
		"init-module-renders-root-type",
	}
	runner := fmt.Sprintf(`set -eu
curl -fsSL https://dl.dagger.io/dagger/releases/%[1]s/dagger_v%[1]s_linux_amd64.tar.gz -o /tmp/dagger.tar.gz
printf '%%s  %%s\n' %[2]q /tmp/dagger.tar.gz | sha256sum -c -
mkdir -p /opt/dagger /raw
tar -xzf /tmp/dagger.tar.gz -C /opt/dagger dagger
printf '%%s  %%s\n' %[3]q /opt/dagger/dagger | sha256sum -c -
for check in %[4]s; do
  status=0
  workspace=/subject
  if [ "$check" = init-module-renders-root-type ]; then
    workspace=/harness
  fi
  /opt/dagger/dagger -m /harness -W "$workspace" check "$check" --no-generate >"/raw/$check.stdout" 2>"/raw/$check.stderr" || status=$?
  printf '%%s\n' "$status" >"/raw/$check.status"
done`, strings.TrimPrefix(completenessCLIVersion, "v"), completenessCLIArchiveSHA, completenessCLIBinarySHA, strings.Join(checks, " "))

	engine := dag.Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/amd64")}).
		From(completenessEngineImage).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(
			"/var/lib/dagger",
			dag.CacheVolume("rust-sdk-completeness-engine-v1.0.0-beta.9"),
			dagger.ContainerWithMountedCacheOpts{Sharing: dagger.CacheSharingModeLocked},
		).
		AsService(dagger.ContainerAsServiceOpts{
			Args:                     []string{"--addr", "tcp://0.0.0.0:1234", "--network-name", "dagger-f1", "--network-cidr", "10.88.0.0/16"},
			UseEntrypoint:            true,
			InsecureRootCapabilities: true,
		})

	return dag.Container(dagger.ContainerOpts{Platform: dagger.Platform("linux/amd64")}).
		From(rustSdkImage+"@"+rustSdkImageDigest).
		WithEnvVariable("CARGO_HOME", "/root/.cargo").
		WithMountedCache("/root/.cargo", dag.CacheVolume("rust-cargo-"+rustSdkImage+"-linux-amd64")).
		WithMountedDirectory("/src", t.Workspace).
		WithWorkdir("/src/"+t.SourcePath).
		WithServiceBinding("dagger-engine", engine).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://dagger-engine:1234").
		WithMountedDirectory("/harness", harness).
		WithMountedDirectory("/subject", t.Workspace.Directory(t.SourcePath)).
		WithExec([]string{"sh", "-c", runner}).
		WithExec([]string{
			"sh", "-c",
			"cargo run -p dagger-sdk-completeness --bin dagger-sdk-harness-profile --locked -- --root /src --raw /raw --cli /opt/dagger/dagger >/profile.json",
		}).
		File("/profile.json")
}

// completenessGoOutputs executes the dependency-free helper in a digest-pinned Go image.
func (t *RustSdkDev) completenessGoOutputs() *dagger.Directory {
	registry := "/src/" + completenessContractPath + "/authorities.json"
	type authority struct {
		name           string
		root           string
		versionLiteral string
	}
	authorities := []authority{
		{name: "go-client", root: "/src/sdk/go"},
		{name: "go-codegen", root: "/src"},
		{name: "go-engine-sdk", root: "/src", versionLiteral: "goSDKLibVersion"},
		{name: "go-integration-tests", root: "/src"},
	}
	ctr := dag.Container().
		From(goHelperImage+"@"+goHelperDigest).
		// The helper is deliberately dependency-free; disabling module discovery keeps its
		// execution boundary limited to the selected source bundle and authored registry.
		WithEnvVariable("GO111MODULE", "off").
		WithMountedDirectory("/src", t.Workspace).
		WithWorkdir("/src").
		WithExec([]string{"mkdir", "-p", "/out"})
	for _, authority := range authorities {
		args := []string{
			"go", "run", "./sdk/rust/completeness/extractors/go",
			"--root", authority.root,
			"--registry", registry,
			"--authority", authority.name,
		}
		if authority.versionLiteral != "" {
			args = append(args, "--version-literal", authority.versionLiteral)
		}
		// The direct process output is the only value handed to the Rust boundary.
		ctr = ctr.WithExec([]string{
			"sh", "-c",
			strings.Join(args, " ") + " >/out/" + authority.name + ".json",
		})
	}
	return ctr.Directory("/out")
}
