package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"strings"
	"text/template"
	"time"

	"toolchains/release/internal/dagger"
)

const (
	publishCheckReleaseTag      = "v0.21.4"
	publishCheckRegistryUser    = "dagger"
	publishCheckRegistryPass    = "xFlejaPdjrt25Dvr" // #nosec G101 -- fake password for the local test registry.
	publishCheckRegistryAddress = "registry:5000"
	publishCheckRegistryImage   = publishCheckRegistryAddress + "/dagger/engine"
	publishCheckEngineStateDir  = "/var/lib/dagger"
)

type publishCheckEnv struct {
	source        *dagger.Directory
	goreleaserKey *dagger.Secret

	releaseTag     string
	releaseVersion string
	commit         string
	moduleRef      string

	awsBucket                 string
	awsCloudfrontDistribution string

	gitSvc           *dagger.Service
	gitHost          string
	repoURL          string
	gitWorktree      *dagger.Directory
	goSDKDestRemote  string
	phpSDKDestRemote string

	registrySvc *dagger.Service
	motoSvc     *dagger.Service
	verdaccio   *dagger.Service
	mockSvc     *dagger.Service

	certs       *releaseCheckCerts
	mockRecords *dagger.CacheVolume

	platform        dagger.Platform
	platformArchive string
}

// Exercise the release publish path against local mock endpoints.
func (r *Release) PublishWithMockEndpoints(
	ctx context.Context,

	// Source tree to publish. The check commits this exact tree to a local git
	// service and invokes release through a nested engine using that git ref.
	// +defaultPath="/"
	source *dagger.Directory,

	// GoReleaser Pro key. This unlocks the real GoReleaser release config but
	// does not grant publish credentials to any external service.
	goreleaserKey *dagger.Secret,
) error {
	env, err := newPublishCheckEnv(ctx, source.WithoutDirectory(".git"), goreleaserKey)
	if err != nil {
		return err
	}

	engine, err := env.releaseEngine(ctx)
	if err != nil {
		return err
	}
	engine, err = engine.Start(ctx)
	if err != nil {
		return err
	}
	env.awsCloudfrontDistribution, err = env.createAWSFixtures(ctx)
	if err != nil {
		return err
	}

	initialOut, err := env.runReleasePublish(ctx, engine, "main")
	if err != nil {
		return err
	}
	if err := requireContains(initialOut, "- [x] 🚙 Engine", "initial main publish should publish the engine"); err != nil {
		return err
	}
	if err := requireContains(initialOut, "- [x] 🚗 CLI", "initial main publish should publish the CLI"); err != nil {
		return err
	}
	if err := requireContains(initialOut, ".goreleaser.nightly.yml", "initial main publish should use the nightly GoReleaser config"); err != nil {
		return err
	}
	if err := env.assertInitialGoReleaserOutputs(ctx); err != nil {
		return err
	}

	if _, err := env.gitWorktreeStdout(ctx, `
git tag "$RELEASE_TAG" "$RELEASE_COMMIT"
git push "$REPO_URL" "$RELEASE_TAG"
git ls-remote --tags "$REPO_URL" "$RELEASE_TAG"
`); err != nil {
		return err
	}

	taggedOut, err := env.runReleasePublish(ctx, engine, env.releaseTag)
	if err != nil {
		return err
	}
	for _, check := range []struct {
		needle string
		msg    string
	}{
		{fmt.Sprintf("- [x] 🚙 Engine ([`%s`]", env.releaseTag), "release publish should publish the engine"},
		{fmt.Sprintf("- [x] 🚗 CLI ([`%s`]", env.releaseTag), "release publish should publish the CLI"},
		{"- [x] 📖 Docs", "release publish should publish docs"},
		{"- [x] 🐹 Go SDK", "release publish should publish the Go SDK"},
		{"- [x] 🐍 Python SDK", "release publish should publish the Python SDK"},
		{"- [x] ⬢ TypeScript SDK", "release publish should publish the TypeScript SDK"},
		{"- [x] 🧪 Elixir SDK", "release publish should publish the Elixir SDK"},
		{"- [x] ⚙️ Rust SDK", "release publish should publish the Rust SDK"},
		{"- [x] 🐘 PHP SDK", "release publish should publish the PHP SDK"},
		{"- [x] ☸️ Helm Chart", "release publish should publish the Helm chart"},
		{".goreleaser.yml", "tagged release publish should use the stable GoReleaser config"},
	} {
		if err := requireContains(taggedOut, check.needle, check.msg); err != nil {
			return err
		}
	}
	if err := requireNotContains(taggedOut, "Error while publishing", "release publish should complete against mock endpoints"); err != nil {
		return err
	}

	if err := env.assertStableGoReleaserArtifacts(ctx); err != nil {
		return err
	}
	if err := env.assertGoReleaserGitHubRelease(ctx); err != nil {
		return err
	}
	if err := env.assertGoReleaserPackageManagers(ctx); err != nil {
		return err
	}
	if err := env.assertMockEvents(ctx); err != nil {
		return err
	}
	if err := env.assertRegistryTags(ctx); err != nil {
		return err
	}
	if err := env.assertEngineVersion(ctx); err != nil {
		return err
	}
	if err := env.assertCLIVersion(ctx); err != nil {
		return err
	}
	if err := env.assertNpmVersion(ctx); err != nil {
		return err
	}
	if err := env.assertHelmTags(ctx); err != nil {
		return err
	}
	if err := env.assertSDKTags(ctx); err != nil {
		return err
	}
	return nil
}

func newPublishCheckEnv(ctx context.Context, source *dagger.Directory, goreleaserKey *dagger.Secret) (*publishCheckEnv, error) {
	platform, platformArchive, err := publishCheckPlatform(ctx)
	if err != nil {
		return nil, err
	}

	env := &publishCheckEnv{
		source:          source,
		goreleaserKey:   goreleaserKey,
		releaseTag:      publishCheckReleaseTag,
		releaseVersion:  strings.TrimPrefix(publishCheckReleaseTag, "v"),
		awsBucket:       "dagger-release-test-" + strings.ToLower(randomID()),
		platform:        platform,
		platformArchive: platformArchive,
	}

	gitSetup := dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "git"}).
		WithDirectory("/root/repo", source).
		WithNewFile("/root/create-release-repo.sh", `#!/bin/sh
set -e -u -x

git config --global user.email "test@dagger.io"
git config --global user.name "Test User"
git config --global init.defaultBranch main

cd /root/repo
rm -rf .git
git init
git checkout -B main
find . -mindepth 2 -name .git -print -exec rm -rf {} +
git add -A
git commit -m "initial release source"

mkdir -p /root/srv
git clone --no-local --bare . /root/srv/repo
for repo in repo go-sdk.git php-sdk.git; do
	if [ "$repo" != "repo" ]; then
		git init --bare "/root/srv/$repo"
	fi
	git -C "/root/srv/$repo" config http.receivepack true
	git -C "/root/srv/$repo" update-server-info
done
`).
		WithExec([]string{"sh", "/root/create-release-repo.sh"})

	env.gitWorktree = gitSetup.Directory("/root/repo")
	gitDir := gitSetup.Directory("/root/srv")
	gitHostname := "git.test"
	env.gitSvc, env.repoURL = gitSmartHTTPServiceDir(gitHostname, gitDir)
	env.repoURL += "/repo"
	env.goSDKDestRemote = strings.TrimSuffix(env.repoURL, "/repo") + "/go-sdk.git"
	env.phpSDKDestRemote = strings.TrimSuffix(env.repoURL, "/repo") + "/php-sdk.git"
	env.gitHost, err = serviceHost(env.repoURL)
	if err != nil {
		return nil, err
	}

	commit, err := env.gitStdout(ctx, `git clone "$REPO_URL" .
git rev-parse HEAD
`)
	if err != nil {
		return nil, err
	}
	env.commit = strings.TrimSpace(commit)
	if env.commit == "" {
		return nil, fmt.Errorf("created release git repo has empty HEAD commit")
	}
	env.moduleRef = env.repoURL + "@" + env.commit

	env.registrySvc = dag.Container().
		From("registry:3").
		WithNewFile("/auth/htpasswd", publishCheckRegistryUser+":$2y$05$/iP8ud0Fs8o3NLlElyfVVOp6LesJl3oRLYoc3neArZKWX10OhynSC").
		WithEnvVariable("REGISTRY_AUTH", "htpasswd").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_REALM", "Registry Realm").
		WithEnvVariable("REGISTRY_AUTH_HTPASSWD_PATH", "/auth/htpasswd").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService()

	env.motoSvc = dag.Container().
		From("motoserver/moto:latest").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	env.verdaccio = dag.Container().
		From("verdaccio/verdaccio:5").
		WithNewFile("/verdaccio/conf/config.yaml", verdaccioConfig).
		WithExposedPort(4873, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	env.certs = newReleaseCheckCerts("ca")
	githubCert, githubKey := env.certs.newServerCerts("github.test")
	env.mockRecords = dag.CacheVolume("release-mock-records-" + randomID())
	env.mockSvc = dag.Container().
		From("python:3.12-alpine").
		WithMountedFile("/certs/github.test.crt", githubCert).
		WithMountedFile("/certs/github.test.key", githubKey).
		WithMountedCache("/records", env.mockRecords).
		WithNewFile("/server.py", releaseMockServerScript).
		WithExposedPort(443, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithExposedPort(8080, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithEntrypoint([]string{"python", "/server.py"}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	return env, nil
}

func (env *publishCheckEnv) releaseEngine(ctx context.Context) (*dagger.Service, error) {
	dev := dag.EngineDev(dagger.EngineDevOpts{
		Source:       env.source,
		SubnetNumber: 90,
	}).IncrementSubnet()
	networkCIDR, err := dev.NetworkCidr(ctx)
	if err != nil {
		return nil, err
	}

	engineCtr := dev.Container().
		WithNewFile("/etc/dagger/engine.json", `{
  "gc": {
    "enabled": true,
    "policies": [
      {
        "all": true,
        "reservedSpace": "0",
        "minFreeSpace": "1000000000000000",
        "maxUsedSpace": "1000000000000000"
      }
    ]
  },
  "registries": {
    "`+publishCheckRegistryAddress+`": {
      "http": true
    }
  }
}`).
		WithServiceBinding(env.gitHost, env.gitSvc).
		WithServiceBinding("registry", env.registrySvc).
		WithServiceBinding("moto", env.motoSvc).
		WithServiceBinding("verdaccio", env.verdaccio).
		WithServiceBinding("mock", env.mockSvc).
		WithServiceBinding("github.test", env.mockSvc).
		WithExposedPort(1234, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithMountedCache(publishCheckEngineStateDir, dag.CacheVolume("release-publish-nested-engine-state-"+randomID()), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.CacheSharingModeLocked,
		})

	return engineCtr.AsService(dagger.ContainerAsServiceOpts{
		Args: []string{
			"--addr", "tcp://0.0.0.0:1234",
			"--network-name", "dagger-dev",
			"--network-cidr", networkCIDR,
		},
		UseEntrypoint:            true,
		InsecureRootCapabilities: true,
	}), nil
}

func (env *publishCheckEnv) client(engine *dagger.Service) *dagger.Container {
	dev := dag.EngineDev(dagger.EngineDevOpts{Source: env.source})
	client := dag.Wolfi().
		Container(dagger.WolfiContainerOpts{
			Packages: []string{"apk-tools", "ca-certificates", "curl", "git"},
		}).
		WithEnvVariable("HOME", "/root").
		WithDirectory("/src", env.gitWorktree).
		WithWorkdir("/src").
		WithServiceBinding(env.gitHost, env.gitSvc).
		WithServiceBinding("registry", env.registrySvc).
		WithServiceBinding("moto", env.motoSvc).
		WithServiceBinding("verdaccio", env.verdaccio).
		WithServiceBinding("mock", env.mockSvc).
		WithServiceBinding("github.test", env.mockSvc).
		WithMountedFile("/github-ca.pem", env.certs.caRootCert)

	return dev.InstallClient(client, dagger.EngineDevInstallClientOpts{Service: engine})
}

func (env *publishCheckEnv) runReleasePublish(ctx context.Context, engine *dagger.Service, tag string) (string, error) {
	script := `set +e
	dagger --progress=plain -m "$MODULE_REF" call release publish \
  --tag "$RELEASE_TAG" --commit "$RELEASE_COMMIT" \
  --registry-image "` + publishCheckRegistryImage + `" \
  --registry-username "` + publishCheckRegistryUser + `" \
  --registry-password=env:REGISTRY_PASSWORD \
  --goreleaser-key=env:GORELEASER_KEY \
  --github-token=env:FAKE_GITHUB_TOKEN \
  --github-release-token=env:FAKE_GITHUB_TOKEN \
  --github-org-name "dagger" \
  --github-host "github.test" \
  --github-ca-cert "/github-ca.pem" \
  --netlify-token=env:FAKE_NETLIFY_TOKEN \
  --netlify-apiurl "http://mock:8080/netlify/api/v1" \
  --pypi-token=env:FAKE_PYPI_TOKEN \
  --pypi-url "http://mock:8080/pypi/legacy/" \
  --npm-token=env:FAKE_NPM_TOKEN \
  --npm-registry-url "http://verdaccio:4873" \
  --hex-apikey=env:FAKE_HEX_API_KEY \
  --hex-apiurl "http://mock:8080/hex/api" \
  --cargo-registry-token=env:FAKE_CARGO_REGISTRY_TOKEN \
  --cargo-registry-index "sparse+http://mock:8080/cargo/" \
  --aws-access-key-id=env:AWS_ACCESS_KEY_ID \
  --aws-secret-access-key=env:AWS_SECRET_ACCESS_KEY \
  --aws-region "us-east-1" \
  --aws-bucket "$AWS_BUCKET" \
  --aws-cloudfront-distribution "$AWS_CLOUDFRONT_DISTRIBUTION" \
  --aws-endpoint-url "http://moto:5000" \
  --artefacts-fqdn "mock:8080" \
  --go-sdk-dest-remote "$GO_SDK_DEST_REMOTE" \
  --php-sdk-dest-remote "$PHP_SDK_DEST_REMOTE" \
  --helm-registry "registry:5000/dagger" \
  markdown > /tmp/publish.log 2>&1
status=$?
cat /tmp/publish.log
exit "$status"
`

	out, err := env.client(engine).
		WithSecretVariable("GORELEASER_KEY", env.goreleaserKey).
		WithSecretVariable("REGISTRY_PASSWORD", dag.SetSecret("release-registry-password-"+randomID(), publishCheckRegistryPass)).
		WithSecretVariable("FAKE_GITHUB_TOKEN", dag.SetSecret("fake-github-token-"+randomID(), publishCheckRegistryPass)).
		WithSecretVariable("FAKE_NETLIFY_TOKEN", dag.SetSecret("fake-netlify-token-"+randomID(), "fake-netlify-token")).
		WithSecretVariable("FAKE_PYPI_TOKEN", dag.SetSecret("fake-pypi-token-"+randomID(), "fake-pypi-token")).
		WithSecretVariable("FAKE_NPM_TOKEN", dag.SetSecret("fake-npm-token-"+randomID(), "fake-npm-token")).
		WithSecretVariable("FAKE_HEX_API_KEY", dag.SetSecret("fake-hex-api-key-"+randomID(), "fake-hex-api-key")).
		WithSecretVariable("FAKE_CARGO_REGISTRY_TOKEN", dag.SetSecret("fake-cargo-token-"+randomID(), "fake-cargo-token")).
		WithSecretVariable("AWS_ACCESS_KEY_ID", dag.SetSecret("fake-aws-access-key-"+randomID(), "test")).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", dag.SetSecret("fake-aws-secret-key-"+randomID(), "test")).
		WithEnvVariable("MODULE_REF", env.moduleRef).
		WithEnvVariable("RELEASE_TAG", tag).
		WithEnvVariable("RELEASE_COMMIT", env.commit).
		WithEnvVariable("AWS_BUCKET", env.awsBucket).
		WithEnvVariable("AWS_CLOUDFRONT_DISTRIBUTION", env.awsCloudfrontDistribution).
		WithEnvVariable("GO_SDK_DEST_REMOTE", env.goSDKDestRemote).
		WithEnvVariable("PHP_SDK_DEST_REMOTE", env.phpSDKDestRemote).
		WithEnvVariable("PUBLISH_RUN_CACHE_BUSTER", randomID()).
		WithExec([]string{"sh", "-ec", script}).
		Stdout(ctx)
	if err != nil {
		return out, fmt.Errorf("release publish %s failed: %w\n%s", tag, err, out)
	}
	return out, nil
}

func (env *publishCheckEnv) gitStdout(ctx context.Context, script string) (string, error) {
	out, err := dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithServiceBinding(env.gitHost, env.gitSvc).
		WithWorkdir("/src").
		WithEnvVariable("REPO_URL", env.repoURL).
		WithEnvVariable("RELEASE_TAG", env.releaseTag).
		WithEnvVariable("RELEASE_COMMIT", env.commit).
		WithEnvVariable("GIT_CACHE_BUSTER", randomID()).
		WithExec([]string{"sh", "-ec", script}).
		Stdout(ctx)
	if err != nil {
		return out, fmt.Errorf("git command failed: %w\n%s", err, out)
	}
	return strings.TrimSpace(out), nil
}

func (env *publishCheckEnv) gitWorktreeStdout(ctx context.Context, script string) (string, error) {
	out, err := dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "git"}).
		With(gitUserConfig).
		WithServiceBinding(env.gitHost, env.gitSvc).
		WithDirectory("/src", env.gitWorktree).
		WithWorkdir("/src").
		WithEnvVariable("REPO_URL", env.repoURL).
		WithEnvVariable("RELEASE_TAG", env.releaseTag).
		WithEnvVariable("RELEASE_COMMIT", env.commit).
		WithEnvVariable("GIT_CACHE_BUSTER", randomID()).
		WithExec([]string{"sh", "-ec", script}).
		Stdout(ctx)
	if err != nil {
		return out, fmt.Errorf("git worktree command failed: %w\n%s", err, out)
	}
	return strings.TrimSpace(out), nil
}

func (env *publishCheckEnv) awsCLI(opts ...dagger.ContainerOpts) *dagger.Container {
	var containerOpts dagger.ContainerOpts
	if len(opts) > 0 {
		containerOpts = opts[0]
	}
	return dag.Container(containerOpts).
		From("alpine:latest").
		WithExec([]string{"apk", "add", "aws-cli", "curl"}).
		WithServiceBinding("moto", env.motoSvc).
		WithEnvVariable("AWS_ACCESS_KEY_ID", "test").
		WithEnvVariable("AWS_SECRET_ACCESS_KEY", "test").
		WithEnvVariable("AWS_REGION", "us-east-1").
		WithEnvVariable("AWS_ENDPOINT_URL", "http://moto:5000").
		WithEnvVariable("AWS_EC2_METADATA_DISABLED", "true")
}

func (env *publishCheckEnv) createAWSFixtures(ctx context.Context) (string, error) {
	out, err := env.awsCLI().
		WithEnvVariable("AWS_BUCKET", env.awsBucket).
		WithNewFile("/tmp/distribution.json", `{
  "CallerReference": "`+randomID()+`",
  "Comment": "dagger release publish check",
  "Enabled": true,
  "Origins": {
    "Quantity": 1,
    "Items": [{
      "Id": "origin",
      "DomainName": "example.com",
      "CustomOriginConfig": {
        "HTTPPort": 80,
        "HTTPSPort": 443,
        "OriginProtocolPolicy": "http-only",
        "OriginSslProtocols": {"Quantity": 1, "Items": ["TLSv1.2"]}
      }
    }]
  },
  "DefaultCacheBehavior": {
    "TargetOriginId": "origin",
    "ViewerProtocolPolicy": "allow-all",
    "TrustedSigners": {"Enabled": false, "Quantity": 0},
    "ForwardedValues": {"QueryString": false, "Cookies": {"Forward": "none"}},
    "MinTTL": 0
  }
}`).
		WithExec([]string{"sh", "-ec", `
aws --endpoint-url "$AWS_ENDPOINT_URL" s3 mb "s3://$AWS_BUCKET" >/dev/null
aws --endpoint-url "$AWS_ENDPOINT_URL" cloudfront create-distribution \
  --distribution-config file:///tmp/distribution.json \
  --query 'Distribution.Id' --output text
`}).
		Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("create mock AWS fixtures: %w", err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("create mock AWS fixtures: empty CloudFront distribution id")
	}
	return out, nil
}

func (env *publishCheckEnv) assertMockEvents(ctx context.Context) error {
	events, err := env.mockEvents(ctx)
	if err != nil {
		return err
	}

	for _, needle := range []string{
		`"kind": "netlify_list_deploys"`,
		`"kind": "netlify_restore"`,
		`"kind": "pypi_publish"`,
		`"kind": "hex_publish"`,
		`"kind": "hex_docs_publish"`,
		`"kind": "cargo_publish"`,
		`"crate_version": "` + env.releaseVersion + `"`,
	} {
		if err := requireContains(events, needle, "mock endpoint should receive expected request"); err != nil {
			return err
		}
	}
	return nil
}

func (env *publishCheckEnv) assertInitialGoReleaserOutputs(ctx context.Context) error {
	if err := env.assertS3ArchiveSet(ctx, "dagger/main/"+env.commit, publishCheckArchiveNames(env.commit), false); err != nil {
		return fmt.Errorf("check main SHA CLI artifacts: %w", err)
	}
	if err := env.assertS3ArchiveSet(ctx, "dagger/main/head", publishCheckArchiveNames("head"), false); err != nil {
		return fmt.Errorf("check main head CLI artifacts: %w", err)
	}

	events, err := env.mockEvents(ctx)
	if err != nil {
		return err
	}
	for _, needle := range []string{
		`"kind": "github_release_create"`,
		`"kind": "github_release_asset_upload"`,
		`/api/v3/repos/dagger/nix/contents/pkgs/dagger/default.nix`,
		`/api/v3/repos/dagger/homebrew-tap/contents/dagger.rb`,
		`/api/v3/repos/dagger/winget-pkgs/contents/`,
		`/api/v3/repos/microsoft/winget-pkgs/pulls`,
	} {
		if err := requireNotContains(events, needle, "initial main publish should not perform stable GoReleaser publishing"); err != nil {
			return err
		}
	}
	return nil
}

func (env *publishCheckEnv) assertStableGoReleaserArtifacts(ctx context.Context) error {
	return env.assertS3ArchiveSet(ctx, "dagger/releases/"+env.releaseVersion, publishCheckArchiveNames(env.releaseTag), true)
}

func (env *publishCheckEnv) assertS3ArchiveSet(ctx context.Context, dir string, archives []string, inspectArchives bool) error {
	inspect := "0"
	if inspectArchives {
		inspect = "1"
	}

	_, err := env.awsCLI().
		WithExec([]string{"apk", "add", "coreutils", "unzip"}).
		WithEnvVariable("AWS_BUCKET", env.awsBucket).
		WithEnvVariable("S3_DIR", dir).
		WithEnvVariable("INSPECT_ARCHIVES", inspect).
		WithNewFile("/tmp/expected-artifacts", strings.Join(archives, "\n")+"\n").
		WithExec([]string{"sh", "-ec", `
set -eu
mkdir -p /tmp/artifacts

while IFS= read -r file; do
	[ -n "$file" ] || continue
	aws --endpoint-url "$AWS_ENDPOINT_URL" s3 cp "s3://$AWS_BUCKET/$S3_DIR/$file" "/tmp/artifacts/$file" >/dev/null
done < /tmp/expected-artifacts
aws --endpoint-url "$AWS_ENDPOINT_URL" s3 cp "s3://$AWS_BUCKET/$S3_DIR/checksums.txt" /tmp/artifacts/checksums.txt >/dev/null

cd /tmp/artifacts
while IFS= read -r file; do
	[ -n "$file" ] || continue
	matches="$(grep -F "  $file" checksums.txt | wc -l | tr -d ' ')"
	if [ "$matches" != "1" ]; then
		echo "expected exactly one checksum entry for $file, got $matches" >&2
		cat checksums.txt >&2
		exit 1
	fi
done < /tmp/expected-artifacts
sha256sum -c checksums.txt --ignore-missing >/tmp/checksums.out
cat /tmp/checksums.out

if [ "$INSPECT_ARCHIVES" = "1" ]; then
	while IFS= read -r file; do
		[ -n "$file" ] || continue
		case "$file" in
			*.tar.gz)
				tar -tzf "$file" > /tmp/archive-list
				grep -Fx LICENSE /tmp/archive-list >/dev/null
				grep -Fx dagger /tmp/archive-list >/dev/null
				;;
			*.zip)
				unzip -Z1 "$file" > /tmp/archive-list
				grep -Fx LICENSE /tmp/archive-list >/dev/null
				grep -Fx dagger.exe /tmp/archive-list >/dev/null
				;;
			*)
				echo "unknown archive format: $file" >&2
				exit 1
				;;
		esac
	done < /tmp/expected-artifacts
fi
`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check S3 archive set %s: %w", dir, err)
	}
	return nil
}

func (env *publishCheckEnv) assertGoReleaserGitHubRelease(ctx context.Context) error {
	assets := append(publishCheckArchiveNames(env.releaseTag), "checksums.txt")
	_, err := dag.Container().
		From("python:3.12-alpine").
		WithMountedCache("/records", env.mockRecords).
		WithEnvVariable("RELEASE_TAG", env.releaseTag).
		WithEnvVariable("RELEASE_COMMIT", env.commit).
		WithNewFile("/tmp/expected-assets", strings.Join(assets, "\n")+"\n").
		WithExec([]string{"python", "-c", `
import json
import os
import sys

def fail(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)

events = []
with open("/records/events.jsonl", encoding="utf-8") as f:
    for line in f:
        if line.strip():
            events.append(json.loads(line))

tag = os.environ["RELEASE_TAG"]
commit = os.environ["RELEASE_COMMIT"]

releases = [e for e in events if e.get("kind") == "github_release_create" and e.get("tag_name") == tag]
if len(releases) != 1:
    fail(f"expected exactly one root release create for {tag}, got {len(releases)}")
release = releases[0]
if release.get("target_commitish") not in ("", commit):
    fail(f"release target mismatch: expected {commit}, got {release.get('target_commitish')}")
if release.get("draft") is not True:
    fail(f"release should be created as draft before asset uploads: {release.get('draft')}")
if release.get("prerelease") not in (None, False):
    fail(f"stable release should not be marked prerelease: {release.get('prerelease')}")
if "Fix a regression in the 1Password secret provider where secrets with spaces could not be resolved" not in release.get("release_body", ""):
    fail("root release body did not include expected changelog entry")

expected_assets = sorted(line.strip() for line in open("/tmp/expected-assets", encoding="utf-8") if line.strip())
actual_assets = sorted(e.get("asset_name", "") for e in events if e.get("kind") == "github_release_asset_upload")
if actual_assets != expected_assets:
    fail("release assets mismatch\nexpected: %r\nactual:   %r" % (expected_assets, actual_assets))

publishes = [e for e in events if e.get("kind") == "github_release_publish"]
if len(publishes) != 1:
    fail(f"expected exactly one root release publish after asset upload, got {len(publishes)}")
if publishes[0].get("draft") is not False:
    fail(f"release publish should undraft the release: {publishes[0].get('draft')}")
`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check GoReleaser GitHub release: %w", err)
	}
	return nil
}

func (env *publishCheckEnv) assertGoReleaserPackageManagers(ctx context.Context) error {
	_, err := env.awsCLI().
		WithExec([]string{"apk", "add", "python3"}).
		WithMountedCache("/records", env.mockRecords).
		WithEnvVariable("AWS_BUCKET", env.awsBucket).
		WithEnvVariable("RELEASE_TAG", env.releaseTag).
		WithEnvVariable("RELEASE_VERSION", env.releaseVersion).
		WithExec([]string{"sh", "-ec", `
set -eu
aws --endpoint-url "$AWS_ENDPOINT_URL" s3 cp "s3://$AWS_BUCKET/dagger/releases/$RELEASE_VERSION/checksums.txt" /tmp/checksums.txt >/dev/null
python3 - <<'PY'
import json
import os
import re
import sys

def fail(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)

def need(condition, msg):
    if not condition:
        fail(msg)

def read_record(path):
    full = os.path.join("/records/github-content", path)
    try:
        with open(full, encoding="utf-8") as f:
            return f.read()
    except FileNotFoundError:
        fail(f"missing recorded GitHub content write: {path}")

tag = os.environ["RELEASE_TAG"]
version = os.environ["RELEASE_VERSION"]
base_url = f"https://mock:8080/dagger/releases/{version}"

checksums = {}
with open("/tmp/checksums.txt", encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        checksum, name = line.split(None, 1)
        checksums[name] = checksum

def archive(suffix):
    name = f"dagger_{tag}_{suffix}"
    need(name in checksums, f"missing checksum for {name}")
    return name

homebrew = read_record("dagger/homebrew-tap/dagger.rb")
need("This file was generated by GoReleaser" in homebrew, "homebrew formula should be GoReleaser generated")
need("class Dagger < Formula" in homebrew, "homebrew formula should define Dagger formula")
need(f'version "{version}"' in homebrew, "homebrew formula should set release version")
need('system "#{bin}/dagger version"' in homebrew, "homebrew formula should keep smoke test")
for suffix in ("darwin_amd64.tar.gz", "darwin_arm64.tar.gz", "linux_amd64.tar.gz", "linux_arm64.tar.gz"):
    name = archive(suffix)
    need(f'url "{base_url}/{name}"' in homebrew, f"homebrew formula missing URL for {name}")
    need(f'sha256 "{checksums[name]}"' in homebrew, f"homebrew formula missing sha256 for {name}")
need("linux_armv7" not in homebrew, "homebrew formula should not include linux armv7")

nix = read_record("dagger/nix/pkgs/dagger/default.nix")
need("This file was generated by GoReleaser" in nix, "nix package should be GoReleaser generated")
need('pname = "dagger";' in nix, "nix package should set pname")
need(f'version = "{version}";' in nix, "nix package should set release version")
need("installShellCompletion --cmd dagger" in nix, "nix package should install shell completions")
need("lib.licenses.asl20" in nix, "nix package should set license")
nix_archives = {
    "x86_64-linux": "linux_amd64.tar.gz",
    "armv7l-linux": "linux_armv7.tar.gz",
    "aarch64-linux": "linux_arm64.tar.gz",
    "x86_64-darwin": "darwin_amd64.tar.gz",
    "aarch64-darwin": "darwin_arm64.tar.gz",
}
for platform, suffix in nix_archives.items():
    name = archive(suffix)
    need(f'{platform} = "{base_url}/{name}";' in nix, f"nix package missing URL for {platform}")
    need(re.search(rf'{re.escape(platform)} = "[0-9a-z]{{20,}}";', nix), f"nix package missing hash for {platform}")
need("windows_amd64" not in nix and "windows_arm64" not in nix, "nix package should not include windows archives")

version_manifest = read_record(f"dagger/winget-pkgs/manifests/d/Dagger/Cli/{version}/Dagger.Cli.yaml")
need("PackageIdentifier: Dagger.Cli" in version_manifest, "winget version manifest should set package id")
need(f"PackageVersion: {version}" in version_manifest, "winget version manifest should set version")
need("ManifestType: version" in version_manifest, "winget version manifest should be a version manifest")

installer_manifest = read_record(f"dagger/winget-pkgs/manifests/d/Dagger/Cli/{version}/Dagger.Cli.installer.yaml")
need("InstallerType: zip" in installer_manifest, "winget installer manifest should use zip installers")
need("NestedInstallerType: portable" in installer_manifest, "winget installer manifest should use portable nested installer")
need("RelativeFilePath: dagger.exe" in installer_manifest, "winget installer manifest should point at dagger.exe")
need("PortableCommandAlias: dagger" in installer_manifest, "winget installer manifest should set portable alias")
for arch, suffix in (("arm64", "windows_arm64.zip"), ("x64", "windows_amd64.zip")):
    name = archive(suffix)
    need(f"Architecture: {arch}" in installer_manifest, f"winget installer manifest missing {arch}")
    need(f"InstallerUrl: {base_url}/{name}" in installer_manifest, f"winget installer manifest missing URL for {name}")
    need(f"InstallerSha256: {checksums[name]}" in installer_manifest, f"winget installer manifest missing sha256 for {name}")

locale_manifest = read_record(f"dagger/winget-pkgs/manifests/d/Dagger/Cli/{version}/Dagger.Cli.locale.en-US.yaml")
for needle in (
    "PackageIdentifier: Dagger.Cli",
    f"PackageVersion: {version}",
    "Publisher: Dagger",
    "PublisherUrl: https://dagger.io",
    "PublisherSupportUrl: https://github.com/dagger/dagger/issues/new/choose",
    "PackageName: dagger",
    "License: asl20",
    "ShortDescription: Dagger is an integrated platform to orchestrate the delivery of applications",
    "ManifestType: defaultLocale",
):
    need(needle in locale_manifest, f"winget locale manifest missing {needle}")

events = [json.loads(line) for line in open("/records/events.jsonl", encoding="utf-8") if line.strip()]
content_writes = {
    (e.get("github_owner"), e.get("github_repo"), e.get("content_path"))
    for e in events
    if e.get("kind") == "github_content_write" and e.get("github_repo") in {"homebrew-tap", "nix", "winget-pkgs"}
}
expected_writes = {
    ("dagger", "homebrew-tap", "dagger.rb"),
    ("dagger", "nix", "pkgs/dagger/default.nix"),
    ("dagger", "winget-pkgs", f"manifests/d/Dagger/Cli/{version}/Dagger.Cli.yaml"),
    ("dagger", "winget-pkgs", f"manifests/d/Dagger/Cli/{version}/Dagger.Cli.installer.yaml"),
    ("dagger", "winget-pkgs", f"manifests/d/Dagger/Cli/{version}/Dagger.Cli.locale.en-US.yaml"),
}
need(content_writes == expected_writes, f"package manager content writes mismatch\nexpected: {sorted(expected_writes)}\nactual:   {sorted(content_writes)}")

content_by_path = {
    (e.get("github_owner"), e.get("github_repo"), e.get("content_path")): e
    for e in events
    if e.get("kind") == "github_content_write"
}
expected_write_details = {
    ("dagger", "homebrew-tap", "dagger.rb"): {
        "branch": "main",
        "message": f"Brew formula update for dagger version {tag}",
    },
    ("dagger", "nix", "pkgs/dagger/default.nix"): {
        "branch": "main",
        "message": f"dagger:  -> {tag}",
    },
    ("dagger", "winget-pkgs", f"manifests/d/Dagger/Cli/{version}/Dagger.Cli.yaml"): {
        "branch": f"dagger-{version}",
        "message": f"New version: Dagger.Cli {version}: add version",
    },
    ("dagger", "winget-pkgs", f"manifests/d/Dagger/Cli/{version}/Dagger.Cli.installer.yaml"): {
        "branch": f"dagger-{version}",
        "message": f"New version: Dagger.Cli {version}: add installer",
    },
    ("dagger", "winget-pkgs", f"manifests/d/Dagger/Cli/{version}/Dagger.Cli.locale.en-US.yaml"): {
        "branch": f"dagger-{version}",
        "message": f"New version: Dagger.Cli {version}: add locale",
    },
}
for key, expected in expected_write_details.items():
    event = content_by_path.get(key)
    need(event is not None, f"missing content write event for {key}")
    for field, value in expected.items():
        need(event.get(field) == value, f"content write {key} should have {field}={value!r}, got {event.get(field)!r}")
    need(event.get("committer_name") == "dagger-bot", f"content write {key} should use dagger-bot committer")
    need(event.get("committer_email") == "noreply@dagger.io", f"content write {key} should use dagger-bot email")

winget_refs = [
    e for e in events
    if e.get("kind") == "github_ref_create" and e.get("path") == "/api/v3/repos/dagger/winget-pkgs/git/refs"
]
need(len(winget_refs) == 1, f"expected one winget branch create, got {len(winget_refs)}")
need(winget_refs[0].get("ref") == f"refs/heads/dagger-{version}", f"unexpected winget branch ref: {winget_refs[0].get('ref')}")
winget_syncs = [
    e for e in events
    if e.get("kind") == "github_merge_upstream" and e.get("path") == "/api/v3/repos/dagger/winget-pkgs/merge-upstream"
]
need(len(winget_syncs) == 1, f"expected one winget fork sync, got {len(winget_syncs)}")
winget_prs = [e for e in events if e.get("kind") == "github_pull_request_create" and e.get("path") == "/api/v3/repos/microsoft/winget-pkgs/pulls"]
need(len(winget_prs) == 1, f"expected one winget pull request, got {len(winget_prs)}")
need(winget_prs[0].get("title") == f"New version: Dagger.Cli {version}", f"unexpected winget PR title: {winget_prs[0].get('title')}")
need(winget_prs[0].get("head") == f"dagger:winget-pkgs:dagger-{version}", f"unexpected winget PR head: {winget_prs[0].get('head')}")
need(winget_prs[0].get("base") == "master", f"unexpected winget PR base: {winget_prs[0].get('base')}")
need("Automated with [GoReleaser]" in winget_prs[0].get("body", ""), "winget PR body should include GoReleaser footer")
PY
`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check GoReleaser package manager outputs: %w", err)
	}
	return nil
}

func (env *publishCheckEnv) mockEvents(ctx context.Context) (string, error) {
	events, err := dag.Container().
		From("alpine:latest").
		WithMountedCache("/records", env.mockRecords).
		WithEnvVariable("MOCK_EVENTS_CACHE_BUSTER", randomID()).
		WithExec([]string{"sh", "-ec", "cat /records/events.jsonl 2>/dev/null || true"}).
		Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("read mock events: %w", err)
	}
	return events, nil
}

func (env *publishCheckEnv) assertRegistryTags(ctx context.Context) error {
	tags, err := dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("registry", env.registrySvc).
		WithEnvVariable("REGISTRY_USERNAME", publishCheckRegistryUser).
		WithSecretVariable("REGISTRY_PASSWORD", dag.SetSecret("registry-list-password-"+randomID(), publishCheckRegistryPass)).
		WithExec([]string{"sh", "-ec", `curl -fsS -u "$REGISTRY_USERNAME:$REGISTRY_PASSWORD" http://registry:5000/v2/dagger/engine/tags/list`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("list registry tags: %w", err)
	}
	for _, tag := range []string{env.releaseTag, env.commit, "latest"} {
		if err := requireContains(tags, tag, "registry should contain engine tag"); err != nil {
			return err
		}
	}
	return nil
}

func (env *publishCheckEnv) assertEngineVersion(ctx context.Context) error {
	version, err := dag.Container(dagger.ContainerOpts{Platform: env.platform}).
		From("gcr.io/go-containerregistry/crane:debug").
		WithServiceBinding("registry", env.registrySvc).
		WithEnvVariable("REGISTRY_USERNAME", publishCheckRegistryUser).
		WithSecretVariable("REGISTRY_PASSWORD", dag.SetSecret("registry-crane-password-"+randomID(), publishCheckRegistryPass)).
		WithExec([]string{"sh", "-ec", "crane auth login registry:5000 --insecure --username \"$REGISTRY_USERNAME\" --password \"$REGISTRY_PASSWORD\" && crane export --insecure --platform " + string(env.platform) + " registry:5000/dagger/engine:" + env.releaseTag + " - | tar -xOf - usr/local/bin/dagger-engine > /tmp/dagger-engine && chmod +x /tmp/dagger-engine && /tmp/dagger-engine --version"}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check engine version in published image: %w", err)
	}
	return requireContains(version, env.releaseTag, "published engine binary should report release tag")
}

func (env *publishCheckEnv) assertCLIVersion(ctx context.Context) error {
	version, err := env.awsCLI(dagger.ContainerOpts{Platform: env.platform}).
		WithEnvVariable("AWS_BUCKET", env.awsBucket).
		WithExec([]string{"sh", "-ec", `
aws --endpoint-url "$AWS_ENDPOINT_URL" s3 cp "s3://$AWS_BUCKET/dagger/releases/` + env.releaseVersion + `/dagger_` + env.releaseTag + `_` + env.platformArchive + `.tar.gz" /tmp/dagger.tgz
mkdir -p /tmp/dagger
tar -xzf /tmp/dagger.tgz -C /tmp/dagger
/tmp/dagger/dagger version
`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check CLI version in published archive: %w", err)
	}
	return requireContains(version, env.releaseTag, "published CLI binary should report release tag")
}

func (env *publishCheckEnv) assertNpmVersion(ctx context.Context) error {
	version, err := dag.Container().
		From("node:20-alpine").
		WithServiceBinding("verdaccio", env.verdaccio).
		WithExec([]string{"npm", "view", "@dagger.io/dagger@" + env.releaseVersion, "version", "--registry", "http://verdaccio:4873"}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check npm package version: %w", err)
	}
	if strings.TrimSpace(version) != env.releaseVersion {
		return fmt.Errorf("npm package version mismatch: expected %s, got %s", env.releaseVersion, strings.TrimSpace(version))
	}
	return nil
}

func (env *publishCheckEnv) assertHelmTags(ctx context.Context) error {
	tags, err := dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("registry", env.registrySvc).
		WithEnvVariable("REGISTRY_USERNAME", publishCheckRegistryUser).
		WithSecretVariable("REGISTRY_PASSWORD", dag.SetSecret("registry-helm-list-password-"+randomID(), publishCheckRegistryPass)).
		WithExec([]string{"sh", "-ec", `curl -fsS -u "$REGISTRY_USERNAME:$REGISTRY_PASSWORD" http://registry:5000/v2/dagger/dagger-helm/tags/list`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("list helm chart tags: %w", err)
	}
	return requireContains(tags, env.releaseVersion, "helm registry should contain release tag")
}

func (env *publishCheckEnv) assertSDKTags(ctx context.Context) error {
	goSDKTags, err := env.gitStdout(ctx, `git ls-remote --tags "`+env.goSDKDestRemote+`" "`+env.releaseTag+`"`)
	if err != nil {
		return err
	}
	if err := requireContains(goSDKTags, env.releaseTag, "Go SDK git remote should contain release tag"); err != nil {
		return err
	}

	phpSDKTags, err := env.gitStdout(ctx, `git ls-remote --tags "`+env.phpSDKDestRemote+`" "`+env.releaseTag+`"`)
	if err != nil {
		return err
	}
	return requireContains(phpSDKTags, env.releaseTag, "PHP SDK git remote should contain release tag")
}

func gitSmartHTTPServiceDir(hostname string, dir *dagger.Directory) (*dagger.Service, string) {
	tmpl := template.Must(template.New("").Parse(`
server {
	listen       80;
	server_name  localhost;

	location ~ ^/(.*)$ {
		if ($arg_go-get = "1") {
			return 200 '<html><head><meta name="go-import" content="{{ .ImportPath }} git {{ .RepoURL }}"></head></html>';
		}

		include               /etc/nginx/fastcgi_params;
		fastcgi_param         GIT_HTTP_EXPORT_ALL "";
		fastcgi_param         GIT_PROJECT_ROOT      /var/www;
		fastcgi_param         PATH_INFO             /$1;
		fastcgi_param         SCRIPT_FILENAME       /usr/lib/git-core/git-http-backend;
		fastcgi_pass          unix:/var/run/fcgiwrap.socket;
	}
}
`))

	var config bytes.Buffer
	_ = tmpl.Execute(&config, struct {
		ImportPath string
		RepoURL    string
	}{
		ImportPath: hostname + "/repo",
		RepoURL:    "http://" + hostname + "/repo",
	})

	svc := dag.Container().
		From("nginx").
		WithExec([]string{"sh", "-lc", `
set -eux
apt-get update
apt-get install -y --no-install-recommends git fcgiwrap spawn-fcgi ca-certificates
rm -rf /var/lib/apt/lists/*
test -x /usr/lib/git-core/git-http-backend
`}).
		WithNewFile("/etc/nginx/conf.d/default.conf", config.String()).
		WithMountedDirectory("/var/www", dir).
		WithEnvVariable("CACHE_BUSTER", time.Now().Format(time.RFC3339Nano)).
		WithExposedPort(80).
		WithEntrypoint([]string{"sh", "-lc", "spawn-fcgi -s /var/run/fcgiwrap.socket -M 766 /usr/sbin/fcgiwrap && exec nginx -g 'daemon off;'"}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true}).
		WithHostname("git")

	return svc, "http://" + hostname
}

func gitUserConfig(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithExec([]string{"git", "config", "--global", "user.email", "test@dagger.io"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Test User"}).
		WithExec([]string{"git", "config", "--global", "init.defaultBranch", "main"})
}

type releaseCheckCerts struct {
	ctr        *dagger.Container
	caRootCert *dagger.File
	password   string
}

func newReleaseCheckCerts(caHostname string) *releaseCheckCerts {
	const password = "hunter4"
	ctr := dag.Container().
		From("alpine:latest").
		WithExec([]string{"apk", "add", "openssl"}).
		WithExec([]string{"openssl", "genrsa", "-des3", "-out", "/ca.key", "-passout", "pass:" + password, "2048"}).
		WithExec([]string{"openssl", "req", "-new", "-key", "/ca.key", "-out", "/ca.csr", "-passin", "pass:" + password, "-subj", "/C=US/ST=CA/L=San Francisco/O=Example/CN=" + caHostname}).
		WithNewFile("/ca.ext", fmt.Sprintf(`basicConstraints=critical,CA:TRUE,pathlen:0
keyUsage=critical,keyCertSign,cRLSign
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid:always,issuer:always
subjectAltName=@alt_names

[alt_names]
DNS.1 = %s
`, caHostname)).
		WithExec([]string{"openssl", "x509", "-req", "-in", "/ca.csr", "-signkey", "/ca.key", "-out", "/ca.pem", "-days", "99999", "-sha256", "-extfile", "/ca.ext", "-passin", "pass:" + password})

	return &releaseCheckCerts{
		ctr:        ctr,
		caRootCert: ctr.File("/ca.pem"),
		password:   password,
	}
}

func (g *releaseCheckCerts) newServerCerts(serverHostname string) (*dagger.File, *dagger.File) {
	ctr := g.ctr.
		WithExec([]string{"openssl", "genrsa", "-out", "/server.key", "2048"}).
		WithExec([]string{"openssl", "req", "-new", "-key", "/server.key", "-out", "/server.csr", "-passin", "pass:" + g.password, "-subj", "/C=US/ST=CA/L=San Francisco/O=Example/CN=" + serverHostname}).
		WithNewFile("/server.ext", fmt.Sprintf(`authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
subjectAltName = @alt_names

[alt_names]
DNS.1 = %s
`, serverHostname)).
		WithExec([]string{"openssl", "x509", "-req", "-in", "/server.csr", "-CA", "/ca.pem", "-CAkey", "/ca.key", "-CAcreateserial", "-out", "/server.pem", "-days", "99999", "-sha256", "-extfile", "/server.ext", "-passin", "pass:" + g.password})

	return ctr.File("/server.pem"), ctr.File("/server.key")
}

func publishCheckPlatform(ctx context.Context) (dagger.Platform, string, error) {
	defaultPlatform, err := dag.DefaultPlatform(ctx)
	if err != nil {
		return "", "", err
	}
	switch {
	case strings.HasPrefix(string(defaultPlatform), "linux/arm64"):
		return dagger.Platform("linux/arm64"), "linux_arm64", nil
	case strings.HasPrefix(string(defaultPlatform), "linux/amd64"):
		return dagger.Platform("linux/amd64"), "linux_amd64", nil
	default:
		return "", "", fmt.Errorf("unsupported default platform for publish check: %s", defaultPlatform)
	}
}

func publishCheckArchiveNames(version string) []string {
	prefix := "dagger_" + version + "_"
	return []string{
		prefix + "darwin_amd64.tar.gz",
		prefix + "darwin_arm64.tar.gz",
		prefix + "linux_amd64.tar.gz",
		prefix + "linux_arm64.tar.gz",
		prefix + "linux_armv7.tar.gz",
		prefix + "windows_amd64.zip",
		prefix + "windows_arm64.zip",
	}
}

func serviceHost(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("could not determine host from %q", rawURL)
	}
	return host, nil
}

func requireContains(haystack, needle, msg string) error {
	if !strings.Contains(haystack, needle) {
		return fmt.Errorf("%s: missing %q in:\n%s", msg, needle, haystack)
	}
	return nil
}

func requireNotContains(haystack, needle, msg string) error {
	if strings.Contains(haystack, needle) {
		return fmt.Errorf("%s: found %q in:\n%s", msg, needle, haystack)
	}
	return nil
}

func randomID() string {
	return strings.ToLower(rand.Text())
}

const verdaccioConfig = `storage: /verdaccio/storage
auth:
  htpasswd:
    file: /verdaccio/conf/htpasswd
    max_users: -1
uplinks: {}
packages:
  '@*/*':
    access: $all
    publish: $all
    unpublish: $all
  '**':
    access: $all
    publish: $all
    unpublish: $all
log: { type: stdout, format: pretty, level: http }
`

const releaseMockServerScript = `import base64
import http.server
import hashlib
import json
import os
import ssl
import struct
import threading
import time
import urllib.parse

records_path = "/records/events.jsonl"
os.makedirs(os.path.dirname(records_path), exist_ok=True)
github_content_records_dir = "/records/github-content"
github_release_records_dir = "/records/github-releases"
os.makedirs(github_content_records_dir, exist_ok=True)
os.makedirs(github_release_records_dir, exist_ok=True)
published_crates = {}
github_refs = {}
state_lock = threading.Lock()

def record(kind, handler, body, extra=None):
    event = {
        "time": time.time(),
        "kind": kind,
        "method": handler.command,
        "path": handler.path,
        "body_len": len(body),
    }
    if extra:
        event.update(extra)
    with open(records_path, "a", encoding="utf-8") as f:
        f.write(json.dumps(event, sort_keys=True) + "\n")

def safe_filename(value):
    return urllib.parse.quote(value, safe="")

def github_content_target(path):
    parsed = urllib.parse.urlparse(path)
    parts = parsed.path.split("/")
    if len(parts) < 8:
        return "dagger", "dagger", parsed.path.lstrip("/")
    owner = urllib.parse.unquote(parts[4])
    repo = urllib.parse.unquote(parts[5])
    content_path = urllib.parse.unquote("/".join(parts[7:]))
    return owner, repo, content_path

def github_repo_target(path):
    parsed = urllib.parse.urlparse(path)
    parts = parsed.path.split("/")
    owner = urllib.parse.unquote(parts[4]) if len(parts) > 4 else "dagger"
    repo = urllib.parse.unquote(parts[5]) if len(parts) > 5 else "dagger"
    return owner, repo

def github_default_branch(repo):
    return "master" if repo == "winget-pkgs" else "main"

def github_ref_sha(owner, repo, branch):
    if branch == github_default_branch(repo):
        return "1111111111111111111111111111111111111111"
    with state_lock:
        return github_refs.get((owner, repo, branch))

def write_github_content_record(owner, repo, content_path, content):
    full = os.path.join(github_content_records_dir, owner, repo, content_path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "wb") as f:
        f.write(content)

def write_github_release_record(tag, payload):
    with open(os.path.join(github_release_records_dir, safe_filename(tag) + ".json"), "w", encoding="utf-8") as f:
        json.dump(payload, f, sort_keys=True)

def cargo_index_path(crate_name):
    if len(crate_name) == 1:
        return "1/" + crate_name
    if len(crate_name) == 2:
        return "2/" + crate_name
    if len(crate_name) == 3:
        return "3/" + crate_name[:1] + "/" + crate_name
    return crate_name[:2] + "/" + crate_name[2:4] + "/" + crate_name

def cargo_index_entry(crate_name, meta, checksum):
    deps = meta.get("deps") or []
    return json.dumps({
        "name": crate_name,
        "vers": meta.get("vers"),
        "deps": deps,
        "cksum": checksum,
        "features": meta.get("features") or {},
        "features2": meta.get("features2") or {},
        "yanked": False,
        "links": meta.get("links"),
        "v": 2,
    }, sort_keys=True).encode("utf-8") + b"\n"

def decode_cargo_publish(body):
    if len(body) < 8:
        return {}, b""
    meta_len = struct.unpack("<I", body[:4])[0]
    meta_start = 4
    meta_end = meta_start + meta_len
    meta = json.loads(body[meta_start:meta_end].decode("utf-8"))
    crate_len = struct.unpack("<I", body[meta_end:meta_end + 4])[0]
    crate_start = meta_end + 4
    crate_end = crate_start + crate_len
    return meta, body[crate_start:crate_end]

def record_cargo_publish(handler, body):
    meta, crate = decode_cargo_publish(body)
    crate_name = meta.get("name", "")
    crate_version = meta.get("vers", "")
    checksum = hashlib.sha256(crate).hexdigest()
    with state_lock:
        published_crates[crate_name] = {
            "name": crate_name,
            "meta": meta,
            "checksum": checksum,
        }
    record("cargo_publish", handler, body, {"crate_name": crate_name, "crate_version": crate_version})
    handler.send_json(200, {"warnings": {"invalid_categories": [], "invalid_badges": [], "other": []}})

def etf(value):
    if isinstance(value, dict):
        out = b"t" + struct.pack(">I", len(value))
        for key, item in value.items():
            out += etf(key) + etf(item)
        return out
    if isinstance(value, list):
        if not value:
            return b"j"
        out = b"l" + struct.pack(">I", len(value))
        for item in value:
            out += etf(item)
        return out + b"j"
    if isinstance(value, bool):
        atom = b"true" if value else b"false"
        return b"w" + bytes([len(atom)]) + atom
    if isinstance(value, int):
        if 0 <= value <= 255:
            return b"a" + bytes([value])
        return b"b" + struct.pack(">i", value)
    if value is None:
        return b"w\x03nil"
    data = str(value).encode("utf-8")
    return b"m" + struct.pack(">I", len(data)) + data

def etf_body(value):
    return b"\x83" + etf(value)

class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        return

    def read_body(self):
        length = int(self.headers.get("content-length", "0") or "0")
        return self.rfile.read(length) if length else b""

    def send_bytes(self, status, body, content_type="application/json", headers=None):
        self.send_response(status)
        self.send_header("content-type", content_type)
        self.send_header("content-length", str(len(body)))
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def send_json(self, status, data):
        self.send_bytes(status, json.dumps(data).encode("utf-8"))

    def send_etf(self, status, data, headers=None):
        self.send_bytes(status, etf_body(data), "application/vnd.hex+erlang", headers)

    def do_HEAD(self):
        record("head", self, b"")
        self.send_bytes(200, b"")

    def do_GET(self):
        if self.path.startswith("/netlify/api/v1/sites/docs.dagger.io/deploys"):
            record("netlify_list_deploys", self, b"")
            self.send_json(200, [{"id": "deploy-1"}])
            return
        if self.path == "/hex/api/users/me":
            record("hex_user_me", self, b"")
            self.send_etf(200, {
                "username": "mock",
                "organizations": [],
            })
            return
        if self.path == "/cargo/config.json":
            record("cargo_config", self, b"")
            self.send_json(200, {
                "dl": "http://mock:8080/cargo/api/v1/crates",
                "api": "http://mock:8080/cargo",
            })
            return
        if self.path.startswith("/cargo/"):
            crate_path = self.path.removeprefix("/cargo/")
            with state_lock:
                match = next((entry for entry in published_crates.values() if cargo_index_path(entry["name"]) == crate_path), None)
            if match is None:
                record("cargo_index_missing", self, b"")
                self.send_bytes(404, b"", "text/plain")
                return
            record("cargo_index", self, b"", {"crate_name": match["name"], "crate_version": match["meta"].get("vers", "")})
            self.send_bytes(200, cargo_index_entry(match["name"], match["meta"], match["checksum"]), "text/plain")
            return
        if self.path == "/api/v3/rate_limit":
            record("github_rate_limit", self, b"")
            self.send_json(200, {
                "resources": {"core": {"limit": 5000, "remaining": 4999, "reset": int(time.time()) + 3600}},
                "rate": {"limit": 5000, "remaining": 4999, "reset": int(time.time()) + 3600},
            })
            return
        if self.path.startswith("/api/v3/repos/") and "/releases/tags/" in self.path:
            record("github_release_lookup", self, b"")
            self.send_json(404, {"message": "Not Found"})
            return
        if self.path in ("/api/v3", "/api/v3/"):
            record("github_api_root", self, b"")
            self.send_json(200, {"verifiable_password_authentication": False})
            return
        if self.path.startswith("/api/v3/repos/") and "/contents/" in self.path:
            record("github_content_lookup", self, b"")
            self.send_json(404, {"message": "Not Found"})
            return
        if self.path.startswith("/api/v3/repos/") and "/branches/" in self.path:
            record("github_branch_lookup", self, b"")
            owner, repo = github_repo_target(self.path)
            branch = urllib.parse.unquote(self.path.split("/branches/", 1)[1].split("?", 1)[0])
            sha = github_ref_sha(owner, repo, branch)
            if sha is None:
                self.send_json(404, {"message": "Not Found"})
                return
            self.send_json(200, {"name": branch, "commit": {"sha": sha}})
            return
        if self.path.startswith("/api/v3/repos/") and "/git/ref/heads/" in self.path:
            record("github_ref_lookup", self, b"")
            owner, repo = github_repo_target(self.path)
            branch = urllib.parse.unquote(self.path.split("/git/ref/heads/", 1)[1].split("?", 1)[0])
            sha = github_ref_sha(owner, repo, branch)
            if sha is None:
                self.send_json(404, {"message": "Not Found"})
                return
            self.send_json(200, {"ref": "refs/heads/" + branch, "object": {"sha": sha}})
            return
        if self.path.startswith("/api/v3/repos/"):
            record("github_repo", self, b"")
            parts = self.path.split("/")
            owner = parts[4] if len(parts) > 4 else "dagger"
            name = parts[5] if len(parts) > 5 else "dagger"
            default_branch = "master" if name == "winget-pkgs" else "main"
            self.send_json(200, {"full_name": owner + "/" + name, "default_branch": default_branch})
            return
        record("get", self, b"")
        self.send_json(200, {})

    def do_POST(self):
        body = self.read_body()
        if self.path.startswith("/netlify/api/v1/sites/docs.dagger.io/deploys/") and self.path.endswith("/restore"):
            record("netlify_restore", self, body)
            self.send_json(200, {"id": "deploy-1"})
            return
        if self.path.startswith("/pypi/"):
            record("pypi_publish", self, body)
            self.send_bytes(200, b"OK", "text/plain")
            return
        if self.path.startswith("/hex/api/packages/dagger/releases/") and self.path.endswith("/docs"):
            record("hex_docs_publish", self, body)
            self.send_etf(201, {}, {"location": "http://mock:8080/hexdocs/dagger/" + self.path.split("/releases/", 1)[-1].split("/docs", 1)[0]})
            return
        if self.path.startswith("/hex/api/packages/dagger/releases"):
            record("hex_publish", self, body)
            version = "` + publishCheckReleaseTag + `".lstrip("v")
            self.send_etf(201, {
                "html_url": "http://mock:8080/hex/packages/dagger/" + version,
                "url": "http://mock:8080/hex/api/packages/dagger/releases/" + version,
                "version": version,
            })
            return
        if self.path == "/cargo/api/v1/crates/new":
            record_cargo_publish(self, body)
            return
        if self.path == "/api/v3/repos/dagger/dagger/releases":
            payload = json.loads(body.decode("utf-8") or "{}")
            tag = payload.get("tag_name", "")
            write_github_release_record(tag, payload)
            record("github_release_create", self, body, {
                "tag_name": tag,
                "target_commitish": payload.get("target_commitish", ""),
                "name": payload.get("name", ""),
                "draft": payload.get("draft"),
                "prerelease": payload.get("prerelease"),
                "release_body": payload.get("body", ""),
            })
            self.send_json(201, {
                "id": int(time.time() * 1000),
                "tag_name": tag,
                "name": payload.get("name", tag),
                "html_url": "https://github.test/dagger/dagger/releases/tag/" + tag,
                "upload_url": "https://github.test/api/uploads/repos/dagger/dagger/releases/1/assets{?name,label}",
            })
            return
        if self.path.startswith("/api/uploads/repos/dagger/dagger/releases/") and "/assets" in self.path:
            parsed = urllib.parse.urlparse(self.path)
            asset_name = urllib.parse.parse_qs(parsed.query).get("name", [""])[0]
            record("github_release_asset_upload", self, body, {"asset_name": asset_name})
            self.send_json(201, {"id": int(time.time() * 1000), "name": asset_name})
            return
        if self.path.startswith("/api/v3/repos/") and self.path.endswith("/merge-upstream"):
            record("github_merge_upstream", self, body)
            self.send_json(200, {"message": "mock merge upstream", "merge_type": "none", "base_branch": "master"})
            return
        if self.path.startswith("/api/v3/repos/") and self.path.endswith("/git/refs"):
            payload = json.loads(body.decode("utf-8") or "{}")
            owner, repo = github_repo_target(self.path)
            ref = payload.get("ref", "")
            sha = (payload.get("sha") or payload.get("object", {}).get("sha") or "")
            if ref.startswith("refs/heads/"):
                with state_lock:
                    github_refs[(owner, repo, ref.removeprefix("refs/heads/"))] = sha
            record("github_ref_create", self, body, {
                "ref": ref,
                "sha": sha,
            })
            self.send_json(201, {"ref": "refs/heads/mock", "object": {"sha": "1111111111111111111111111111111111111111"}})
            return
        if self.path.startswith("/api/v3/repos/") and self.path.endswith("/pulls"):
            payload = json.loads(body.decode("utf-8") or "{}")
            record("github_pull_request_create", self, body, {
                "title": payload.get("title", ""),
                "head": payload.get("head", ""),
                "base": payload.get("base", ""),
                "body": payload.get("body", ""),
            })
            self.send_json(201, {"html_url": "https://github.test/mock/pull/1", "number": 1})
            return
        record("post", self, body)
        self.send_json(200, {})

    def do_PUT(self):
        body = self.read_body()
        if self.path == "/cargo/api/v1/crates/new":
            record_cargo_publish(self, body)
            return
        if self.path.startswith("/api/v3/repos/") and "/contents/" in self.path:
            owner, repo, content_path = github_content_target(self.path)
            payload = json.loads(body.decode("utf-8") or "{}")
            content = base64.b64decode(payload.get("content", ""))
            write_github_content_record(owner, repo, content_path, content)
            record("github_content_write", self, body, {
                "github_owner": owner,
                "github_repo": repo,
                "content_path": content_path,
                "message": payload.get("message", ""),
                "branch": payload.get("branch", ""),
                "committer_name": payload.get("committer", {}).get("name", ""),
                "committer_email": payload.get("committer", {}).get("email", ""),
                "decoded_len": len(content),
            })
            self.send_json(201, {
                "content": {"path": self.path.split("/contents/", 1)[-1].split("?", 1)[0], "sha": "2222222222222222222222222222222222222222"},
                "commit": {"sha": "3333333333333333333333333333333333333333"},
            })
            return
        record("put", self, body)
        self.send_json(200, {})

    def do_PATCH(self):
        body = self.read_body()
        if self.path.startswith("/api/v3/repos/dagger/dagger/releases/"):
            payload = json.loads(body.decode("utf-8") or "{}")
            record("github_release_publish", self, body, {
                "draft": payload.get("draft"),
                "prerelease": payload.get("prerelease"),
            })
            self.send_json(200, {"id": 1, "tag_name": "mock", "html_url": "https://github.test/dagger/dagger/releases/tag/mock"})
            return
        record("patch", self, body)
        self.send_json(200, {})

def serve_http():
    http.server.ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()

threading.Thread(target=serve_http, daemon=True).start()
https = http.server.ThreadingHTTPServer(("0.0.0.0", 443), Handler)
ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain("/certs/github.test.crt", "/certs/github.test.key")
https.socket = ctx.wrap_socket(https.socket, server_side=True)
https.serve_forever()
`
