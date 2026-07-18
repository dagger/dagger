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

	"golang.org/x/mod/semver"
	"sigs.k8s.io/yaml"
)

const (
	publishCheckRegistryUser    = "dagger"
	publishCheckRegistryPass    = "xFlejaPdjrt25Dvr" // #nosec G101 -- fake password for the local test registry.
	publishCheckRegistryAddress = "registry:5000"
	publishCheckRegistryImage   = publishCheckRegistryAddress + "/dagger/engine"
	publishCheckEngineStateDir  = "/var/lib/dagger"
)

type publishCheckEnv struct {
	source *dagger.Directory

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
// +check
func (r *Release) PublishWithMockEndpoints(
	ctx context.Context,

	// Source tree to publish. The check commits this exact tree to a local git
	// service and invokes release through a nested engine using that git ref.
	// +defaultPath="/"
	source *dagger.Directory,
) (rerr error) {
	env, err := newPublishCheckEnv(ctx, source.WithoutDirectory(".git"))
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
	defer func() {
		_, err := engine.Stop(ctx, dagger.ServiceStopOpts{Kill: true})
		if rerr == nil && err != nil {
			rerr = fmt.Errorf("stop nested release engine: %w", err)
		}
	}()
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
	if err := env.assertInitialCLIReleaseOutputs(ctx); err != nil {
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
	type publishCheck struct {
		needle string
		msg    string
	}
	checks := []publishCheck{
		{fmt.Sprintf("- [x] 🚙 Engine ([`%s`]", env.releaseTag), "release publish should publish the engine"},
		{fmt.Sprintf("- [x] 🚗 CLI ([`%s`]", env.releaseTag), "release publish should publish the CLI"},
	}
	if semver.Prerelease(env.releaseTag) == "" {
		checks = append(checks,
			publishCheck{"- [x] 📖 Docs", "release publish should publish docs"},
			publishCheck{"- [x] 🐹 Go SDK", "release publish should publish the Go SDK"},
			publishCheck{"- [x] 🐍 Python SDK", "release publish should publish the Python SDK"},
			publishCheck{"- [x] ⬢ TypeScript SDK", "release publish should publish the TypeScript SDK"},
			publishCheck{"- [x] 🧪 Elixir SDK", "release publish should publish the Elixir SDK"},
			publishCheck{"- [x] ⚙️ Rust SDK", "release publish should publish the Rust SDK"},
			publishCheck{"- [x] 🐘 PHP SDK", "release publish should publish the PHP SDK"},
			publishCheck{"- [x] ☸️ Helm Chart", "release publish should publish the Helm chart"},
		)
	}
	for _, check := range checks {
		if err := requireContains(taggedOut, check.needle, check.msg); err != nil {
			return err
		}
	}
	if err := requireNotContains(taggedOut, "Error while publishing", "release publish should complete against mock endpoints"); err != nil {
		return err
	}

	if err := env.assertStableCLIReleaseArtifacts(ctx); err != nil {
		return err
	}
	if err := env.assertCLIPublishMetadata(ctx); err != nil {
		return err
	}
	if err := env.assertCLIGitHubRelease(ctx); err != nil {
		return err
	}
	if err := env.assertCLIPackageManagers(ctx); err != nil {
		return err
	}
	if err := env.assertComponentGitHubReleases(ctx); err != nil {
		return err
	}
	if err := env.assertPackageRegistryRequests(ctx); err != nil {
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

func newPublishCheckEnv(ctx context.Context, source *dagger.Directory) (*publishCheckEnv, error) {
	platform, platformArchive, err := publishCheckPlatform(ctx)
	if err != nil {
		return nil, err
	}
	releaseTag, releaseVersion, err := publishCheckRelease(ctx, source)
	if err != nil {
		return nil, err
	}

	// A stable release reads release notes from a changelog file per published
	// component: the root .changes/<tag>.md for the engine and CLI (cli-dev's
	// publish step), and <component>/.changes/<tag>.md for each SDK and the Helm
	// chart (Changelog.lookupEntry, used when cutting their GitHub releases). The
	// pre-release working tree under test carries none of these until the release
	// is actually cut, so stub them all.
	//
	// The check inspects each resulting release body (see the embedded python
	// below), asserting it mentions its own release tag, carries a "What to do
	// next?" follow-up section, and (for the root release) has at least one
	// "### Added/Changed/Fixed" section. Component release tags are
	// "<component>/<version>" (e.g. "sdk/go/v1.0.0") while the root engine/CLI
	// tag is just the version, so title each stub with its own tag — mirroring
	// the real changelog headers (e.g. "## sdk/go/v0.11.0 - <date>").
	// A component's changelog path and its release tag usually match, but not
	// always: the Helm chart's notes live under helm/dagger/ while its release
	// is tagged helm/chart/. Write each stub to its path but title it with its
	// tag (the string the check looks for in the body).
	for _, c := range []struct{ path, tag string }{
		{"", ""}, // engine + CLI (root .changes, tagged just <version>)
		{"sdk/go/", "sdk/go/"},
		{"sdk/python/", "sdk/python/"},
		{"sdk/typescript/", "sdk/typescript/"},
		{"sdk/elixir/", "sdk/elixir/"},
		{"sdk/rust/", "sdk/rust/"},
		{"sdk/php/", "sdk/php/"},
		{"helm/dagger/", "helm/chart/"},
	} {
		tag := c.tag + releaseTag // "v1.0.0", "sdk/go/v1.0.0", "helm/chart/v1.0.0"
		notes := "## " + tag + "\n\n" +
			"### Changed\n\n" +
			"- Publish-check placeholder entry.\n\n" +
			"### What to do next?\n\n" +
			"- Read the [documentation](https://docs.dagger.io)\n"
		source = source.WithNewFile(c.path+".changes/"+releaseTag+".md", notes)
	}

	env := &publishCheckEnv{
		source:          source,
		releaseTag:      releaseTag,
		releaseVersion:  releaseVersion,
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
		WithEnvVariable("PUBLISH_CHECK_TAG", env.releaseTag).
		WithExposedPort(443, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithExposedPort(8080, dagger.ContainerWithExposedPortOpts{Protocol: dagger.NetworkProtocolTcp}).
		WithEntrypoint([]string{"python", "/server.py"}).
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

	return env, nil
}

func publishCheckRelease(ctx context.Context, source *dagger.Directory) (tag, version string, rerr error) {
	chartYaml, err := source.File("helm/dagger/Chart.yaml").Contents(ctx)
	if err != nil {
		return "", "", fmt.Errorf("read Helm chart metadata: %w", err)
	}
	var chart struct {
		Version string `json:"version"`
	}
	if err := yaml.Unmarshal([]byte(chartYaml), &chart); err != nil {
		return "", "", fmt.Errorf("parse Helm chart metadata: %w", err)
	}

	version = strings.TrimSpace(chart.Version)
	if version == "" {
		return "", "", fmt.Errorf("helm chart version is empty")
	}
	tag = version
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	if !semver.IsValid(tag) {
		return "", "", fmt.Errorf("helm chart version %q does not produce a valid release tag", version)
	}
	return tag, strings.TrimPrefix(tag, "v"), nil
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
	dagger --progress=plain -W "$MODULE_REF" call release publish \
  --tag "$RELEASE_TAG" --commit "$RELEASE_COMMIT" \
  --registry-image "` + publishCheckRegistryImage + `" \
  --registry-username "` + publishCheckRegistryUser + `" \
  --registry-password=env:REGISTRY_PASSWORD \
  --github-token=env:FAKE_GITHUB_TOKEN \
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
		WithEnvVariable("GO_SDK_DEST_REMOTE", env.goSDKDestRemote).
		WithEnvVariable("PHP_SDK_DEST_REMOTE", env.phpSDKDestRemote).
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

func (env *publishCheckEnv) assertInitialCLIReleaseOutputs(ctx context.Context) error {
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
		if err := requireNotContains(events, needle, "initial main publish should not perform stable CLI publishing"); err != nil {
			return err
		}
	}
	return nil
}

func (env *publishCheckEnv) assertStableCLIReleaseArtifacts(ctx context.Context) error {
	return env.assertS3ArchiveSet(ctx, "dagger/releases/"+env.releaseVersion, publishCheckArchiveNames(env.releaseTag), true)
}

func (env *publishCheckEnv) assertCLIPublishMetadata(ctx context.Context) error {
	_, err := env.awsCLI().
		WithExec([]string{"apk", "add", "python3"}).
		WithEnvVariable("AWS_BUCKET", env.awsBucket).
		WithEnvVariable("AWS_CLOUDFRONT_DISTRIBUTION", env.awsCloudfrontDistribution).
		WithEnvVariable("RELEASE_TAG", env.releaseTag).
		WithEnvVariable("RELEASE_VERSION", env.releaseVersion).
		WithEnvVariable("RELEASE_COMMIT", env.commit).
		WithExec([]string{"sh", "-ec", `
set -eu
aws --endpoint-url "$AWS_ENDPOINT_URL" s3api list-objects-v2 --bucket "$AWS_BUCKET" > /tmp/s3-objects.json
for key in dagger/latest_version dagger/versions/latest dagger/versions/${RELEASE_VERSION%.*} dagger/install.sh dagger/install.ps1; do
	aws --endpoint-url "$AWS_ENDPOINT_URL" s3 cp "s3://$AWS_BUCKET/$key" "/tmp/$(basename "$key")" >/dev/null
done
aws --endpoint-url "$AWS_ENDPOINT_URL" cloudfront list-invalidations --distribution-id "$AWS_CLOUDFRONT_DISTRIBUTION" > /tmp/cloudfront-invalidations.json
python3 - <<'PY' > /tmp/cloudfront-invalidation-ids
import json
data = json.load(open("/tmp/cloudfront-invalidations.json", encoding="utf-8"))
items = data.get("InvalidationList", {}).get("Items") or []
for item in items:
    if item.get("Id"):
        print(item["Id"])
PY
: > /tmp/cloudfront-invalidation-details.jsonl
while IFS= read -r invalidation_id; do
	[ -n "$invalidation_id" ] || continue
	aws --endpoint-url "$AWS_ENDPOINT_URL" cloudfront get-invalidation \
		--distribution-id "$AWS_CLOUDFRONT_DISTRIBUTION" \
		--id "$invalidation_id" >> /tmp/cloudfront-invalidation-details.jsonl
	printf '\n' >> /tmp/cloudfront-invalidation-details.jsonl
done < /tmp/cloudfront-invalidation-ids
python3 - <<'PY'
import json
import os
import sys

def fail(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)

tag = os.environ["RELEASE_TAG"]
version = os.environ["RELEASE_VERSION"]
commit = os.environ["RELEASE_COMMIT"]
suffixes = [
    "darwin_amd64.tar.gz",
    "darwin_arm64.tar.gz",
    "linux_amd64.tar.gz",
    "linux_arm64.tar.gz",
    "linux_armv7.tar.gz",
    "windows_amd64.zip",
    "windows_arm64.zip",
]

expected = set()
for label, prefix in (
    (commit, "dagger/main/" + commit),
    ("head", "dagger/main/head"),
    (tag, "dagger/releases/" + version),
):
    for suffix in suffixes:
        expected.add(prefix + "/dagger_" + label + "_" + suffix)
    expected.add(prefix + "/checksums.txt")
expected.update({
    "dagger/install.sh",
    "dagger/install.ps1",
    "dagger/latest_version",
    "dagger/versions/latest",
    "dagger/versions/" + ".".join(version.split(".")[:2]),
})

objects = json.load(open("/tmp/s3-objects.json", encoding="utf-8"))
actual = {item["Key"] for item in objects.get("Contents", [])}
if actual != expected:
    fail("S3 object set mismatch\nexpected: %r\nactual:   %r" % (sorted(expected), sorted(actual)))

for pointer in ("latest_version", "latest", ".".join(version.split(".")[:2])):
    path = "/tmp/" + pointer
    got = open(path, encoding="utf-8").read()
    if got != version:
        fail(f"{pointer} should contain {version!r}, got {got!r}")

for script in ("install.sh", "install.ps1"):
    contents = open("/tmp/" + script, encoding="utf-8").read()
    if "dagger" not in contents.lower():
        fail(f"{script} did not look like a Dagger install script")

details = open("/tmp/cloudfront-invalidation-details.jsonl", encoding="utf-8").read()
if "/dagger/install.sh" not in details or "/dagger/install.ps1" not in details:
    fail("CloudFront invalidation should include install script paths: " + details)
PY
`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check CLI publish metadata: %w", err)
	}
	return nil
}

func (env *publishCheckEnv) assertS3ArchiveSet(ctx context.Context, dir string, archives []string, inspectArchives bool) error {
	inspect := "0"
	if inspectArchives {
		inspect = "1"
	}

	_, err := env.awsCLI().
		WithExec([]string{"apk", "add", "coreutils", "python3", "unzip"}).
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
awk '{print $2}' checksums.txt > /tmp/checksum-names
if ! sort /tmp/checksum-names | cmp -s - /tmp/checksum-names; then
	echo "checksums.txt should be sorted by artifact filename" >&2
	cat checksums.txt >&2
	exit 1
fi

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

	while IFS= read -r file; do
		[ -n "$file" ] || continue
		disposition="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-object --bucket "$AWS_BUCKET" --key "$S3_DIR/$file" --query ContentDisposition --output text)"
		if [ "$disposition" != "attachment;filename=$file" ]; then
			echo "unexpected content disposition for $file: $disposition" >&2
			exit 1
		fi
		case "$file" in
			*.tar.gz) expected_type="application/x-gzip" ;;
			*.zip) expected_type="application/zip" ;;
			*) echo "unknown artifact extension for $file" >&2; exit 1 ;;
		esac
		content_type="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-object --bucket "$AWS_BUCKET" --key "$S3_DIR/$file" --query ContentType --output text)"
		if [ "$content_type" != "$expected_type" ]; then
			echo "unexpected content type for $file: expected $expected_type, got $content_type" >&2
			exit 1
		fi
		content_encoding="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-object --bucket "$AWS_BUCKET" --key "$S3_DIR/$file" --query ContentEncoding --output text)"
		if [ "$content_encoding" != "None" ] && [ "$content_encoding" != "null" ] && [ -n "$content_encoding" ]; then
			echo "unexpected content encoding for $file: $content_encoding" >&2
			exit 1
		fi
	done < /tmp/expected-artifacts
	disposition="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-object --bucket "$AWS_BUCKET" --key "$S3_DIR/checksums.txt" --query ContentDisposition --output text)"
	if [ "$disposition" != "attachment;filename=checksums.txt" ]; then
		echo "unexpected content disposition for checksums.txt: $disposition" >&2
		exit 1
	fi
	content_type="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-object --bucket "$AWS_BUCKET" --key "$S3_DIR/checksums.txt" --query ContentType --output text)"
	if [ "$content_type" != "text/plain; charset=utf-8" ]; then
		echo "unexpected content type for checksums.txt: $content_type" >&2
		exit 1
	fi
	content_encoding="$(aws --endpoint-url "$AWS_ENDPOINT_URL" s3api head-object --bucket "$AWS_BUCKET" --key "$S3_DIR/checksums.txt" --query ContentEncoding --output text)"
	if [ "$content_encoding" != "None" ] && [ "$content_encoding" != "null" ] && [ -n "$content_encoding" ]; then
		echo "unexpected content encoding for checksums.txt: $content_encoding" >&2
		exit 1
	fi

	if [ "$INSPECT_ARCHIVES" = "1" ]; then
	while IFS= read -r file; do
		[ -n "$file" ] || continue
		case "$file" in
			*.tar.gz)
				tar -tzf "$file" > /tmp/archive-list
				printf 'LICENSE\ndagger\n' > /tmp/expected-archive-list
				if ! cmp -s /tmp/expected-archive-list /tmp/archive-list; then
					echo "unexpected tar archive entries for $file" >&2
					cat /tmp/archive-list >&2
					exit 1
				fi
				;;
			*.zip)
				unzip -Z1 "$file" > /tmp/archive-list
				printf 'LICENSE\ndagger.exe\n' > /tmp/expected-archive-list
				if ! cmp -s /tmp/expected-archive-list /tmp/archive-list; then
					echo "unexpected zip archive entries for $file" >&2
					cat /tmp/archive-list >&2
					exit 1
				fi
				;;
			*)
				echo "unknown archive format: $file" >&2
				exit 1
				;;
		esac
	done < /tmp/expected-artifacts
	python3 - <<'PY'
import os
import stat
import struct
import sys
import tarfile
import zipfile

def fail(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)

def check_macho(file, data, expected_cputype):
    if len(data) < 8 or data[:4] != b"\xcf\xfa\xed\xfe":
        fail(f"{file} should be a 64-bit little-endian Mach-O binary")
    cputype = struct.unpack("<I", data[4:8])[0]
    if cputype != expected_cputype:
        fail(f"{file} Mach-O cputype mismatch: expected {expected_cputype:#x}, got {cputype:#x}")

def check_elf(file, data, expected_machine, expected_class):
    if len(data) < 20 or data[:4] != b"\x7fELF":
        fail(f"{file} should be an ELF binary")
    if data[4] != expected_class:
        fail(f"{file} ELF class mismatch: expected {expected_class}, got {data[4]}")
    if data[5] != 1:
        fail(f"{file} should be little-endian ELF, got data encoding {data[5]}")
    machine = struct.unpack("<H", data[18:20])[0]
    if machine != expected_machine:
        fail(f"{file} ELF machine mismatch: expected {expected_machine}, got {machine}")

def check_pe(file, data, expected_machine):
    if len(data) < 0x40 or data[:2] != b"MZ":
        fail(f"{file} should be a PE binary")
    pe_offset = struct.unpack("<I", data[0x3c:0x40])[0]
    if len(data) < pe_offset + 6 or data[pe_offset:pe_offset+4] != b"PE\0\0":
        fail(f"{file} should contain a PE header")
    machine = struct.unpack("<H", data[pe_offset+4:pe_offset+6])[0]
    if machine != expected_machine:
        fail(f"{file} PE machine mismatch: expected {expected_machine:#x}, got {machine:#x}")

def check_binary_arch(file, data):
    if not data:
        fail(f"{file} binary should be non-empty")
    if "_darwin_amd64." in file:
        check_macho(file, data, 0x01000007)
    elif "_darwin_arm64." in file:
        check_macho(file, data, 0x0100000c)
    elif "_linux_amd64." in file:
        check_elf(file, data, 62, 2)
    elif "_linux_arm64." in file:
        check_elf(file, data, 183, 2)
    elif "_linux_armv7." in file:
        check_elf(file, data, 40, 1)
    elif "_windows_amd64." in file:
        check_pe(file, data, 0x8664)
    elif "_windows_arm64." in file:
        check_pe(file, data, 0xaa64)
    else:
        fail(f"missing binary architecture assertion for {file}")

expected = [line.strip() for line in open("/tmp/expected-artifacts", encoding="utf-8") if line.strip()]
for file in expected:
    if file.endswith(".tar.gz"):
        with tarfile.open(file, "r:gz") as archive:
            entries = archive.getmembers()
            by_name = {entry.name: entry for entry in entries}
            binary = archive.extractfile(by_name["dagger"]).read(4096) if "dagger" in by_name else b""
        names = [entry.name for entry in entries]
        if names != ["LICENSE", "dagger"]:
            fail(f"unexpected tar entries for {file}: {names!r}")
        for name, expected_mode in (("LICENSE", 0o644), ("dagger", 0o755)):
            entry = by_name[name]
            if entry.uid != 0 or entry.gid != 0:
                fail(f"{file}:{name} should be owned by uid/gid 0, got {entry.uid}/{entry.gid}")
            if entry.mtime != 0:
                fail(f"{file}:{name} should have deterministic mtime 0, got {entry.mtime}")
            mode = stat.S_IMODE(entry.mode)
            if mode != expected_mode:
                fail(f"{file}:{name} mode mismatch: expected {oct(expected_mode)}, got {oct(mode)}")
        check_binary_arch(file, binary)
    elif file.endswith(".zip"):
        with zipfile.ZipFile(file) as archive:
            entries = archive.infolist()
            binary = archive.read("dagger.exe")[:4096] if "dagger.exe" in archive.namelist() else b""
        names = [entry.filename for entry in entries]
        if names != ["LICENSE", "dagger.exe"]:
            fail(f"unexpected zip entries for {file}: {names!r}")
        by_name = {entry.filename: entry for entry in entries}
        for name, expected_mode in (("LICENSE", 0o644), ("dagger.exe", 0o755)):
            entry = by_name[name]
            if entry.date_time != (1980, 1, 1, 0, 0, 0):
                fail(f"{file}:{name} should have deterministic zip timestamp, got {entry.date_time!r}")
            mode = (entry.external_attr >> 16) & 0o777
            if mode and mode != expected_mode:
                fail(f"{file}:{name} mode mismatch: expected {oct(expected_mode)}, got {oct(mode)}")
        check_binary_arch(file, binary)
    else:
        fail(f"unknown archive format: {file}")
PY
fi
`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check S3 archive set %s: %w", dir, err)
	}
	return nil
}

func (env *publishCheckEnv) assertCLIGitHubRelease(ctx context.Context) error {
	assets := append(publishCheckArchiveNames(env.releaseTag), "checksums.txt")
	_, err := env.awsCLI().
		WithExec([]string{"apk", "add", "python3"}).
		WithMountedCache("/records", env.mockRecords).
		WithEnvVariable("AWS_BUCKET", env.awsBucket).
		WithEnvVariable("RELEASE_TAG", env.releaseTag).
		WithEnvVariable("RELEASE_VERSION", env.releaseVersion).
		WithEnvVariable("RELEASE_COMMIT", env.commit).
		WithNewFile("/tmp/expected-assets", strings.Join(assets, "\n")+"\n").
		WithExec([]string{"sh", "-ec", `
set -eu
mkdir -p /tmp/s3-assets
while IFS= read -r asset; do
	[ -n "$asset" ] || continue
	aws --endpoint-url "$AWS_ENDPOINT_URL" s3 cp "s3://$AWS_BUCKET/dagger/releases/$RELEASE_VERSION/$asset" "/tmp/s3-assets/$asset" >/dev/null
done < /tmp/expected-assets
python3 - <<'PY'
import json
import os
import hashlib
import sys
import urllib.parse

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

lookups = [e for e in events if e.get("kind") == "github_release_lookup" and e.get("tag_name") == tag]
if len(lookups) != 1:
    fail(f"expected exactly one root release lookup for {tag}, got {len(lookups)}")

creates = [e for e in events if e.get("kind") == "github_release_create" and e.get("tag_name") == tag]
if len(creates) != 1:
    fail(f"expected exactly one root release create for {tag}, got {len(creates)}")

updates = [e for e in events if e.get("kind") == "github_release_update" and e.get("tag_name") == tag]
if updates:
    fail(f"root release should exercise first-release create path, got update events: {updates}")
release = creates[0]
if release.get("target_commitish") != "main":
    fail(f"release target mismatch: expected main, got {release.get('target_commitish')}")
if release.get("name") != tag:
    fail(f"release name mismatch: expected {tag}, got {release.get('name')}")
if release.get("draft") is not True:
    fail(f"new release should be created as a draft before asset uploads: {release.get('draft')}")
if release.get("prerelease") not in (None, False):
    fail(f"stable release should not be marked prerelease: {release.get('prerelease')}")
body = release.get("release_body", "")
if tag not in body:
    fail("root release body should mention its release tag")
if "What to do next" not in body:
    fail("root release body should include standard follow-up section")
if not any(section in body for section in ("### Added", "### Changed", "### Fixed")):
    fail("root release body should include release note sections")

expected_assets = sorted(line.strip() for line in open("/tmp/expected-assets", encoding="utf-8") if line.strip())

asset_lists = [e for e in events if e.get("kind") == "github_release_asset_list"]
if len(asset_lists) != 1:
    fail(f"expected one release asset list before upload, got {len(asset_lists)}")

deleted_assets = sorted(e.get("asset_name", "") for e in events if e.get("kind") == "github_release_asset_delete")
if deleted_assets:
    fail(f"new root release should not delete existing assets, got {deleted_assets}")

actual_assets = sorted(e.get("asset_name", "") for e in events if e.get("kind") == "github_release_asset_upload")
if actual_assets != expected_assets:
    fail("release assets mismatch\nexpected: %r\nactual:   %r" % (expected_assets, actual_assets))

uploads = [e for e in events if e.get("kind") == "github_release_asset_upload"]
for asset in expected_assets:
    s3_path = os.path.join("/tmp/s3-assets", asset)
    record_path = os.path.join("/records/github-assets", urllib.parse.quote(asset, safe=""))
    with open(s3_path, "rb") as f:
        s3_bytes = f.read()
    try:
        with open(record_path, "rb") as f:
            uploaded_bytes = f.read()
    except FileNotFoundError:
        fail(f"missing recorded GitHub release asset bytes for {asset}")
    if uploaded_bytes != s3_bytes:
        fail(f"GitHub release asset bytes differ from S3 artifact for {asset}")
    expected_sha = hashlib.sha256(s3_bytes).hexdigest()
    upload = next((e for e in uploads if e.get("asset_name") == asset), None)
    if upload is None or upload.get("body_sha256") != expected_sha:
        fail(f"recorded GitHub upload hash mismatch for {asset}")

publishes = [e for e in events if e.get("kind") == "github_release_publish"]
if len(publishes) != 1:
    fail(f"expected exactly one root release publish after asset upload, got {len(publishes)}")
if publishes[0].get("draft") is not False:
    fail(f"release publish should undraft the release: {publishes[0].get('draft')}")
if uploads and publishes[0].get("time", 0) <= max(e.get("time", 0) for e in uploads):
    fail("root release was published before all assets were uploaded")
PY
`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check CLI GitHub release: %w", err)
	}
	return nil
}

func (env *publishCheckEnv) assertComponentGitHubReleases(ctx context.Context) error {
	_, err := dag.Container().
		From("python:3.12-alpine").
		WithMountedCache("/records", env.mockRecords).
		WithEnvVariable("RELEASE_TAG", env.releaseTag).
		WithEnvVariable("RELEASE_COMMIT", env.commit).
		WithNewFile("/tmp/expected-component-tags", strings.Join([]string{
			"sdk/go/" + env.releaseTag,
			"sdk/python/" + env.releaseTag,
			"sdk/typescript/" + env.releaseTag,
			"sdk/elixir/" + env.releaseTag,
			"sdk/rust/" + env.releaseTag,
			"sdk/php/" + env.releaseTag,
			"helm/chart/" + env.releaseTag,
		}, "\n")+"\n").
		WithExec([]string{"python", "-c", `
import json
import os
import sys

def fail(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)

events = [json.loads(line) for line in open("/records/events.jsonl", encoding="utf-8") if line.strip()]
expected = [line.strip() for line in open("/tmp/expected-component-tags", encoding="utf-8") if line.strip()]
commit = os.environ["RELEASE_COMMIT"]

creates = [e for e in events if e.get("kind") == "github_release_create"]
by_tag = {}
for event in creates:
    by_tag.setdefault(event.get("tag_name"), []).append(event)

for tag in expected:
    matches = by_tag.get(tag, [])
    if len(matches) != 1:
        fail(f"expected exactly one component GitHub release create for {tag}, got {len(matches)}")
    release = matches[0]
    if release.get("target_commitish") != commit:
        fail(f"component release {tag} target mismatch: expected {commit}, got {release.get('target_commitish')}")
    if release.get("name") != tag:
        fail(f"component release {tag} name mismatch: {release.get('name')}")
    if release.get("draft") not in (None, False):
        fail(f"component release {tag} should not be draft: {release.get('draft')}")
    if release.get("prerelease") not in (None, False):
        fail(f"component release {tag} should not be prerelease: {release.get('prerelease')}")
    if release.get("make_latest") != "false":
        fail(f"component release {tag} should pass latest=false, got {release.get('make_latest')!r}")
    body = release.get("release_body", "")
    if tag not in body:
        fail(f"component release {tag} body should mention its release tag")
    if "What to do next" not in body:
        fail(f"component release {tag} body should include standard follow-up section")

unexpected_components = sorted(tag for tag in by_tag if tag and tag.startswith(("sdk/", "helm/")) and tag not in expected)
if unexpected_components:
    fail(f"unexpected component GitHub release tags: {unexpected_components}")
`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check component GitHub releases: %w", err)
	}
	return nil
}

func (env *publishCheckEnv) assertCLIPackageManagers(ctx context.Context) error {
	_, err := env.awsCLI().
		WithExec([]string{"apk", "add", "python3"}).
		WithMountedCache("/records", env.mockRecords).
		WithEnvVariable("AWS_BUCKET", env.awsBucket).
		WithEnvVariable("RELEASE_TAG", env.releaseTag).
		WithEnvVariable("RELEASE_VERSION", env.releaseVersion).
		WithExec([]string{"sh", "-ec", `
set -eu
aws --endpoint-url "$AWS_ENDPOINT_URL" s3 cp "s3://$AWS_BUCKET/dagger/releases/$RELEASE_VERSION/checksums.txt" /tmp/checksums.txt >/dev/null
mkdir -p /tmp/nix-artifacts
cat > /tmp/nix-archives <<'EOF'
x86_64-linux linux_amd64.tar.gz
armv7l-linux linux_armv7.tar.gz
aarch64-linux linux_arm64.tar.gz
x86_64-darwin darwin_amd64.tar.gz
aarch64-darwin darwin_arm64.tar.gz
EOF
while read -r platform suffix; do
	name="dagger_${RELEASE_TAG}_${suffix}"
	aws --endpoint-url "$AWS_ENDPOINT_URL" s3 cp "s3://$AWS_BUCKET/dagger/releases/$RELEASE_VERSION/$name" "/tmp/nix-artifacts/$platform" >/dev/null
done < /tmp/nix-archives
python3 - <<'PY'
import hashlib
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

def nix_base32(data):
    alphabet = "0123456789abcdfghijklmnpqrsvwxyz"
    if not data:
        return ""
    out = []
    length = (len(data) * 8 - 1) // 5 + 1
    for n in range(length - 1, -1, -1):
        bit = n * 5
        i = bit // 8
        j = bit % 8
        value = data[i] >> j
        if i + 1 < len(data):
            value |= data[i + 1] << (8 - j)
        out.append(alphabet[value & 0x1f])
    return "".join(out)

homebrew = read_record("dagger/homebrew-tap/dagger.rb")
need("This file was generated by Dagger release tooling" in homebrew, "homebrew formula should be generated by Dagger release tooling")
need("class Dagger < Formula" in homebrew, "homebrew formula should define Dagger formula")
need(f'version "{version}"' in homebrew, "homebrew formula should set release version")
need('system "#{bin}/dagger version"' in homebrew, "homebrew formula should keep smoke test")
for suffix in ("darwin_amd64.tar.gz", "darwin_arm64.tar.gz", "linux_amd64.tar.gz", "linux_arm64.tar.gz"):
    name = archive(suffix)
    need(f'url "{base_url}/{name}"' in homebrew, f"homebrew formula missing URL for {name}")
    need(f'sha256 "{checksums[name]}"' in homebrew, f"homebrew formula missing sha256 for {name}")
need("linux_armv7" not in homebrew, "homebrew formula should not include linux armv7")

nix = read_record("dagger/nix/pkgs/dagger/default.nix")
need("This file was generated by Dagger release tooling" in nix, "nix package should be generated by Dagger release tooling")
need('pname = "dagger";' in nix, "nix package should set pname")
need(f'version = "{version}";' in nix, "nix package should set release version")
need('sourceRoot = ".";' in nix, "nix package should use the archive root as sourceRoot")
need("sourceRootMap" not in nix, "nix package should not need per-platform source roots for unwrapped archives")
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
    with open(f"/tmp/nix-artifacts/{platform}", "rb") as f:
        expected_nix_hash = nix_base32(hashlib.sha256(f.read()).digest())
    need(f'{platform} = "{expected_nix_hash}";' in nix, f"nix package hash mismatch for {platform}")
need("windows_amd64" not in nix and "windows_arm64" not in nix, "nix package should not include windows archives")

version_manifest = read_record(f"dagger/winget-pkgs/manifests/d/Dagger/Cli/{version}/Dagger.Cli.yaml")
need("# yaml-language-server: $schema=https://aka.ms/winget-manifest.version.1.10.0.schema.json" in version_manifest, "winget version manifest should set schema")
need("PackageIdentifier: Dagger.Cli" in version_manifest, "winget version manifest should set package id")
need(f"PackageVersion: {version}" in version_manifest, "winget version manifest should set version")
need("ManifestType: version" in version_manifest, "winget version manifest should be a version manifest")
need("ManifestVersion: 1.10.0" in version_manifest, "winget version manifest should use manifest version 1.10.0")

installer_manifest = read_record(f"dagger/winget-pkgs/manifests/d/Dagger/Cli/{version}/Dagger.Cli.installer.yaml")
need("# yaml-language-server: $schema=https://aka.ms/winget-manifest.installer.1.10.0.schema.json" in installer_manifest, "winget installer manifest should set schema")
need("InstallerType: zip" in installer_manifest, "winget installer manifest should use zip installers")
need(re.search(r'ReleaseDate: "\d{4}-\d{2}-\d{2}"', installer_manifest), "winget installer manifest should set a release date")
need("NestedInstallerType: portable" in installer_manifest, "winget installer manifest should use portable nested installer")
need("RelativeFilePath: dagger.exe" in installer_manifest, "winget installer manifest should point at dagger.exe")
need("PortableCommandAlias: dagger" in installer_manifest, "winget installer manifest should set portable alias")
need("ManifestVersion: 1.10.0" in installer_manifest, "winget installer manifest should use manifest version 1.10.0")
for arch, suffix in (("arm64", "windows_arm64.zip"), ("x64", "windows_amd64.zip")):
    name = archive(suffix)
    need(f"Architecture: {arch}" in installer_manifest, f"winget installer manifest missing {arch}")
    need(f"InstallerUrl: {base_url}/{name}" in installer_manifest, f"winget installer manifest missing URL for {name}")
    need(f"InstallerSha256: {checksums[name]}" in installer_manifest, f"winget installer manifest missing sha256 for {name}")

locale_manifest = read_record(f"dagger/winget-pkgs/manifests/d/Dagger/Cli/{version}/Dagger.Cli.locale.en-US.yaml")
need("# yaml-language-server: $schema=https://aka.ms/winget-manifest.defaultLocale.1.10.0.schema.json" in locale_manifest, "winget locale manifest should set schema")
for needle in (
    "PackageIdentifier: Dagger.Cli",
    f"PackageVersion: {version}",
    "Publisher: Dagger",
    "PublisherUrl: https://dagger.io",
    "PublisherSupportUrl: https://github.com/dagger/dagger/issues/new/choose",
    "PackageName: dagger",
    "Moniker: dagger",
    "License: asl20",
    "ShortDescription: Dagger is an integrated platform to orchestrate the delivery of applications",
    "ManifestType: defaultLocale",
    "ManifestVersion: 1.10.0",
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
		"sha": "9999999999999999999999999999999999999999",
	},
	("dagger", "nix", "pkgs/dagger/default.nix"): {
		"branch": "main",
		"message": f"dagger:  -> {tag}",
		"sha": "9999999999999999999999999999999999999999",
	},
	("dagger", "winget-pkgs", f"manifests/d/Dagger/Cli/{version}/Dagger.Cli.yaml"): {
		"branch": f"dagger-{version}",
		"message": f"New version: Dagger.Cli {version}: add version",
		"sha": "",
	},
	("dagger", "winget-pkgs", f"manifests/d/Dagger/Cli/{version}/Dagger.Cli.installer.yaml"): {
		"branch": f"dagger-{version}",
		"message": f"New version: Dagger.Cli {version}: add installer",
		"sha": "",
	},
	("dagger", "winget-pkgs", f"manifests/d/Dagger/Cli/{version}/Dagger.Cli.locale.en-US.yaml"): {
		"branch": f"dagger-{version}",
		"message": f"New version: Dagger.Cli {version}: add locale",
		"sha": "",
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
need(winget_refs[0].get("sha") == "4444444444444444444444444444444444444444", f"winget branch should be created from upstream master, got {winget_refs[0].get('sha')}")
winget_upstream_ref_lookups = [
    e for e in events
    if e.get("kind") == "github_ref_lookup" and e.get("path") == "/api/v3/repos/microsoft/winget-pkgs/git/ref/heads/master"
]
need(len(winget_upstream_ref_lookups) == 1, f"expected one winget upstream master ref lookup, got {len(winget_upstream_ref_lookups)}")
winget_syncs = [e for e in events if e.get("kind") == "github_merge_upstream"]
need(len(winget_syncs) == 0, f"winget should create release branch from upstream master directly, got sync events: {winget_syncs}")
winget_prs = [e for e in events if e.get("kind") == "github_pull_request_create" and e.get("path") == "/api/v3/repos/microsoft/winget-pkgs/pulls"]
need(len(winget_prs) == 1, f"expected one winget pull request, got {len(winget_prs)}")
need(winget_prs[0].get("title") == f"New version: Dagger.Cli {version}", f"unexpected winget PR title: {winget_prs[0].get('title')}")
need(winget_prs[0].get("head") == f"dagger:dagger-{version}", f"unexpected winget PR head: {winget_prs[0].get('head')}")
need(winget_prs[0].get("base") == "master", f"unexpected winget PR base: {winget_prs[0].get('base')}")
winget_body = winget_prs[0].get("body", "")
need("## 📖 Description" in winget_body, "winget PR body should include description section")
need("## ✅ Checklist" in winget_body, "winget PR body should include checklist section")
need("Signed the [Contributor License Agreement]" in winget_body, "winget PR body should include CLA checklist")
need("## 📦 Manifest Checklist" in winget_body, "winget PR body should include manifest checklist section")
need("This PR only modifies one (1) manifest" in winget_body, "winget PR body should include manifest checklist")
need("winget validate --manifest <path>" in winget_body, "winget PR body should include validation checklist")
need("Dagger release tooling" in winget_body, "winget PR body should include Dagger release tooling footer")
PY
	`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check CLI package manager outputs: %w", err)
	}
	return nil
}

func (env *publishCheckEnv) assertPackageRegistryRequests(ctx context.Context) error {
	_, err := dag.Container().
		From("python:3.12-alpine").
		WithMountedCache("/records", env.mockRecords).
		WithEnvVariable("RELEASE_VERSION", env.releaseVersion).
		WithExec([]string{"python", "-c", `
import base64
import json
import os
import sys

def fail(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)

def need(condition, msg):
    if not condition:
        fail(msg)

def auth_payload(header):
    if header.startswith("Basic "):
        return base64.b64decode(header.removeprefix("Basic ")).decode("utf-8", "replace")
    return header

events = [json.loads(line) for line in open("/records/events.jsonl", encoding="utf-8") if line.strip()]
version = os.environ["RELEASE_VERSION"]

netlify_lists = [e for e in events if e.get("kind") == "netlify_list_deploys"]
need(len(netlify_lists) == 1, f"expected one Netlify deploy list request, got {len(netlify_lists)}")
need("branch=main" in netlify_lists[0].get("path", ""), f"Netlify deploy list should filter branch=main: {netlify_lists[0].get('path')}")
need(netlify_lists[0].get("auth_header") == "Bearer fake-netlify-token", f"unexpected Netlify list auth header: {netlify_lists[0].get('auth_header')!r}")
netlify_restores = [e for e in events if e.get("kind") == "netlify_restore"]
need(len(netlify_restores) == 1, f"expected one Netlify restore request, got {len(netlify_restores)}")
need(netlify_restores[0].get("path", "").endswith("/deploys/deploy-1/restore"), f"Netlify restore should use listed deploy: {netlify_restores[0].get('path')}")
need(netlify_restores[0].get("auth_header") == "Bearer fake-netlify-token", f"unexpected Netlify restore auth header: {netlify_restores[0].get('auth_header')!r}")

pypi = [e for e in events if e.get("kind") == "pypi_publish"]
need(len(pypi) == 2, f"expected exactly two PyPI uploads, got {len(pypi)}")
pypi_filetypes = {e.get("filetype") for e in pypi}
need(pypi_filetypes == {"sdist", "bdist_wheel"}, f"PyPI should upload exactly sdist and wheel, got {pypi_filetypes}")
expected_pypi_filenames = {
    f"dagger_io-{version}.tar.gz",
    f"dagger_io-{version}-py3-none-any.whl",
}
pypi_filenames = {e.get("filename") for e in pypi}
need(pypi_filenames == expected_pypi_filenames, f"PyPI upload filenames mismatch: expected {expected_pypi_filenames}, got {pypi_filenames}")
for event in pypi:
    need(event.get("package_name") in {"dagger-io", "dagger_io"}, f"unexpected PyPI package name: {event.get('package_name')!r}")
    need(event.get("package_version") == version, f"PyPI upload version mismatch: {event.get('package_version')!r}")
    need("fake-pypi-token" in auth_payload(event.get("auth_header", "")), f"PyPI upload missing fake token auth: {event.get('auth_header')!r}")

hex_publish = [e for e in events if e.get("kind") == "hex_publish"]
need(len(hex_publish) == 1, f"expected one Hex package publish, got {len(hex_publish)}")
need(hex_publish[0].get("package_version") == version, f"Hex publish version mismatch: {hex_publish[0].get('package_version')!r}")
need(hex_publish[0].get("body_sha256"), "Hex publish should record uploaded package bytes")
need("fake-hex-api-key" in hex_publish[0].get("auth_header", ""), f"Hex publish missing API key auth: {hex_publish[0].get('auth_header')!r}")
hex_docs = [e for e in events if e.get("kind") == "hex_docs_publish"]
need(len(hex_docs) == 1, f"expected one Hex docs publish, got {len(hex_docs)}")
need(hex_docs[0].get("package_version") == version, f"Hex docs version mismatch: {hex_docs[0].get('package_version')!r}")
need("fake-hex-api-key" in hex_docs[0].get("auth_header", ""), f"Hex docs publish missing API key auth: {hex_docs[0].get('auth_header')!r}")

cargo = [e for e in events if e.get("kind") == "cargo_publish"]
need(len(cargo) == 1, f"expected one Cargo publish, got {len(cargo)}")
need(cargo[0].get("crate_name") == "dagger-sdk", f"Cargo crate name mismatch: {cargo[0].get('crate_name')!r}")
need(cargo[0].get("crate_version") == version, f"Cargo crate version mismatch: {cargo[0].get('crate_version')!r}")
need(cargo[0].get("deps_count", 0) > 0, "Cargo publish metadata should include dependencies")
need("fake-cargo-token" in cargo[0].get("auth_header", ""), f"Cargo publish missing registry token auth: {cargo[0].get('auth_header')!r}")
need(any(e.get("kind") == "cargo_config" for e in events), "Cargo should fetch registry config")
`}).
		Stdout(ctx)
	if err != nil {
		return fmt.Errorf("check package registry requests: %w", err)
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
	for _, tag := range []string{
		"main",
		env.commit,
		env.releaseTag,
		"latest",
		"main-gpu",
		env.commit + "-gpu",
		env.releaseTag + "-gpu",
		"latest-gpu",
	} {
		if err := requireContains(tags, tag, "registry should contain engine tag"); err != nil {
			return err
		}
	}
	craneCtr := dag.Container(dagger.ContainerOpts{Platform: env.platform}).
		From("gcr.io/go-containerregistry/crane:debug").
		WithServiceBinding("registry", env.registrySvc).
		WithEnvVariable("REGISTRY_USERNAME", publishCheckRegistryUser).
		WithSecretVariable("REGISTRY_PASSWORD", dag.SetSecret("registry-manifest-password-"+randomID(), publishCheckRegistryPass)).
		WithEnvVariable("RELEASE_TAG", env.releaseTag)

	_, err = craneCtr.
		WithExec([]string{"sh", "-ec", `
set -eu
	crane auth login registry:5000 --insecure --username "$REGISTRY_USERNAME" --password "$REGISTRY_PASSWORD"
	for tag in main "$RELEASE_TAG" latest; do
		crane manifest --insecure "registry:5000/dagger/engine:$tag" > "/tmp/default-$tag.json"
		crane config --insecure "registry:5000/dagger/engine:$tag-gpu" > "/tmp/gpu-$tag.json"
	done
check_platform_counts() {
	file="$1"
	expected_amd64="$2"
	expected_arm64="$3"
	expected_linux="$4"
	amd64="$(grep -o '"architecture"[[:space:]]*:[[:space:]]*"amd64"' "$file" | wc -l | tr -d ' ')"
	arm64="$(grep -o '"architecture"[[:space:]]*:[[:space:]]*"arm64"' "$file" | wc -l | tr -d ' ')"
	linux="$(grep -o '"os"[[:space:]]*:[[:space:]]*"linux"' "$file" | wc -l | tr -d ' ')"
	if [ "$amd64" != "$expected_amd64" ] || [ "$arm64" != "$expected_arm64" ] || [ "$linux" != "$expected_linux" ]; then
		echo "unexpected platform counts in $file: amd64=$amd64 arm64=$arm64 linux=$linux" >&2
		cat "$file" >&2
		exit 1
	fi
}
	for tag in main "$RELEASE_TAG" latest; do
		check_platform_counts "/tmp/default-$tag.json" 1 1 2
		architecture="$(grep -o '"architecture"[[:space:]]*:[[:space:]]*"[^"]*"' "/tmp/gpu-$tag.json" | head -1 | sed 's/.*"architecture"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"
		os="$(grep -o '"os"[[:space:]]*:[[:space:]]*"[^"]*"' "/tmp/gpu-$tag.json" | head -1 | sed 's/.*"os"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"
		if [ "$architecture" != "amd64" ] || [ "$os" != "linux" ]; then
		echo "unexpected GPU platform in /tmp/gpu-$tag.json: architecture=$architecture os=$os" >&2
		cat "/tmp/gpu-$tag.json" >&2
			exit 1
		fi
	done
	release_digest="$(crane digest --insecure "registry:5000/dagger/engine:$RELEASE_TAG")"
	latest_digest="$(crane digest --insecure "registry:5000/dagger/engine:latest")"
	if [ "$release_digest" != "$latest_digest" ]; then
		echo "stable release tag and latest should point at the same engine manifest: release=$release_digest latest=$latest_digest" >&2
		exit 1
	fi
	release_gpu_digest="$(crane digest --insecure "registry:5000/dagger/engine:$RELEASE_TAG-gpu")"
	latest_gpu_digest="$(crane digest --insecure "registry:5000/dagger/engine:latest-gpu")"
	if [ "$release_gpu_digest" != "$latest_gpu_digest" ]; then
		echo "stable GPU release tag and latest-gpu should point at the same engine image: release=$release_gpu_digest latest=$latest_gpu_digest" >&2
		exit 1
	fi
	`}).
		Sync(ctx)
	if err != nil {
		return fmt.Errorf("check engine registry manifests: %w", err)
	}

	engineBinaries := craneCtr.
		WithExec([]string{"sh", "-ec", `
set -eu
	crane auth login registry:5000 --insecure --username "$REGISTRY_USERNAME" --password "$REGISTRY_PASSWORD"
	mkdir -p /tmp/engine-binaries
	crane export --insecure --platform linux/amd64 "registry:5000/dagger/engine:$RELEASE_TAG" - | tar -xOf - usr/local/bin/dagger-engine > /tmp/engine-binaries/default-linux-amd64
	crane export --insecure --platform linux/arm64 "registry:5000/dagger/engine:$RELEASE_TAG" - | tar -xOf - usr/local/bin/dagger-engine > /tmp/engine-binaries/default-linux-arm64
	crane export --insecure --platform linux/amd64 "registry:5000/dagger/engine:$RELEASE_TAG-gpu" - | tar -xOf - usr/local/bin/dagger-engine > /tmp/engine-binaries/gpu-linux-amd64
	`}).
		Directory("/tmp/engine-binaries")
	_, err = dag.Container().
		From("python:3.12-alpine").
		WithDirectory("/engine-binaries", engineBinaries).
		WithExec([]string{"python", "-c", `
import os
import struct
import sys

def fail(msg):
    print(msg, file=sys.stderr)
    raise SystemExit(1)

def check_elf(file, expected_machine):
    path = "/engine-binaries/" + file
    data = open(path, "rb").read(64)
    if not data:
        fail(f"{file} should be non-empty")
    if len(data) < 20 or data[:4] != b"\x7fELF":
        fail(f"{file} should be an ELF binary")
    if data[4] != 2:
        fail(f"{file} should be a 64-bit ELF binary, got class {data[4]}")
    if data[5] != 1:
        fail(f"{file} should be little-endian ELF, got data encoding {data[5]}")
    machine = struct.unpack("<H", data[18:20])[0]
    if machine != expected_machine:
        fail(f"{file} ELF machine mismatch: expected {expected_machine}, got {machine}")

check_elf("default-linux-amd64", 62)
check_elf("default-linux-arm64", 183)
check_elf("gpu-linux-amd64", 62)
`}).
		Sync(ctx)
	if err != nil {
		return fmt.Errorf("check engine image binary architectures: %w", err)
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
	_, err := dag.Container().
		From("node:20-alpine").
		WithServiceBinding("verdaccio", env.verdaccio).
		WithEnvVariable("RELEASE_VERSION", env.releaseVersion).
		WithExec([]string{"sh", "-ec", `
	set -eu
	npm view "@dagger.io/dagger@$RELEASE_VERSION" --registry "http://verdaccio:4873" --json > /tmp/npm-view.json
	npm pack "@dagger.io/dagger@$RELEASE_VERSION" --registry "http://verdaccio:4873" --pack-destination /tmp --json > /tmp/npm-pack.json
	node <<'JS'
	const fs = require("fs");
	const childProcess = require("child_process");

	function fail(msg) {
	  console.error(msg);
	  process.exit(1);
	}
	function need(condition, msg) {
	  if (!condition) fail(msg);
	}

	const version = process.env.RELEASE_VERSION;
	const view = JSON.parse(fs.readFileSync("/tmp/npm-view.json", "utf8"));
		need(view.name === "@dagger.io/dagger", "unexpected npm package name: " + view.name);
		need(view.version === version, "unexpected npm package version: " + view.version);
		need(typeof view.dist?.tarball === "string" && view.dist.tarball.endsWith("@dagger.io/dagger/-/dagger-" + version + ".tgz"), "unexpected npm tarball URL: " + view.dist?.tarball);
		need(typeof view.dist?.integrity === "string" && view.dist.integrity.startsWith("sha512-"), "unexpected npm integrity: " + view.dist?.integrity);

	const pack = JSON.parse(fs.readFileSync("/tmp/npm-pack.json", "utf8"))[0];
		need(pack.name === "@dagger.io/dagger", "unexpected packed npm package name: " + pack.name);
		need(pack.version === version, "unexpected packed npm package version: " + pack.version);
		const filename = "/tmp/" + pack.filename;
		need(fs.existsSync(filename), "npm pack did not produce " + filename);
	const listing = childProcess.execFileSync("tar", ["-tzf", filename], {encoding: "utf8"}).trim().split("\n");
	for (const file of [
	  "package/package.json",
	  "package/dist/src/index.js",
	  "package/dist/src/index.d.ts",
	]) {
		  need(listing.includes(file), "packed npm package missing " + file);
		}
		childProcess.execFileSync("tar", ["-xzf", filename, "-C", "/tmp", "package/package.json"]);
		const packageJSON = JSON.parse(fs.readFileSync("/tmp/package/package.json", "utf8"));
		need(packageJSON.name === "@dagger.io/dagger", "packed package.json name mismatch: " + packageJSON.name);
		need(packageJSON.version === version, "packed package.json version mismatch: " + packageJSON.version);
		need(packageJSON.main === "dist/src/index.js", "packed package.json main mismatch: " + packageJSON.main);
		need(packageJSON.types === "./dist/src/index.d.ts", "packed package.json types mismatch: " + packageJSON.types);
JS
	`}).
		Sync(ctx)
	if err != nil {
		return fmt.Errorf("check npm package version: %w", err)
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
	if err := requireContains(tags, env.releaseVersion, "helm registry should contain release tag"); err != nil {
		return err
	}

	_, err = dag.Container(dagger.ContainerOpts{Platform: env.platform}).
		From("alpine:latest").
		WithExec([]string{"apk", "add", "curl", "python3"}).
		WithServiceBinding("registry", env.registrySvc).
		WithEnvVariable("REGISTRY_USERNAME", publishCheckRegistryUser).
		WithSecretVariable("REGISTRY_PASSWORD", dag.SetSecret("registry-helm-password-"+randomID(), publishCheckRegistryPass)).
		WithEnvVariable("RELEASE_VERSION", env.releaseVersion).
		WithExec([]string{"sh", "-ec", `
	set -eu
	curl -fsS -u "$REGISTRY_USERNAME:$REGISTRY_PASSWORD" \
		-H 'Accept: application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json' \
		"http://registry:5000/v2/dagger/dagger-helm/manifests/$RELEASE_VERSION" > /tmp/helm-manifest.json
	chart_digest="$(python3 - <<'PY'
import json
import sys

manifest = json.load(open("/tmp/helm-manifest.json", encoding="utf-8"))
layers = [
    layer for layer in manifest.get("layers", [])
    if layer.get("mediaType") == "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
]
if len(layers) != 1:
    print(f"expected exactly one helm chart content layer, got {len(layers)}", file=sys.stderr)
    print(json.dumps(manifest, indent=2, sort_keys=True), file=sys.stderr)
    raise SystemExit(1)
print(layers[0]["digest"])
PY
	)"
	curl -fsS -u "$REGISTRY_USERNAME:$REGISTRY_PASSWORD" \
		"http://registry:5000/v2/dagger/dagger-helm/blobs/$chart_digest" > /tmp/chart.tgz
	mkdir -p /tmp/chart
	tar -xzf /tmp/chart.tgz -C /tmp/chart
	test -f /tmp/chart/dagger-helm/Chart.yaml
	grep -F "name: dagger-helm" /tmp/chart/dagger-helm/Chart.yaml
	grep -F "version: $RELEASE_VERSION" /tmp/chart/dagger-helm/Chart.yaml
	`}).
		Sync(ctx)
	if err != nil {
		return fmt.Errorf("check helm chart contents: %w", err)
	}
	return nil
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
	if err := requireContains(phpSDKTags, env.releaseTag, "PHP SDK git remote should contain release tag"); err != nil {
		return err
	}

	if _, err := env.gitStdout(ctx, `
set -eu
git clone "$GO_SDK_DEST_REMOTE" go-sdk
git -C go-sdk checkout "$RELEASE_TAG"
test -f go-sdk/go.mod
test -f go-sdk/client.go
test -f go-sdk/engineconn/version.gen.go
test ! -d go-sdk/sdk
grep -F 'const CLIVersion = "`+env.releaseVersion+`"' go-sdk/engineconn/version.gen.go
if grep -F 'replace github.com/dagger/dagger' go-sdk/go.mod; then
	echo "Go SDK mirror should not retain local monorepo replace" >&2
	exit 1
fi

git clone "$PHP_SDK_DEST_REMOTE" php-sdk
git -C php-sdk checkout "$RELEASE_TAG"
test -f php-sdk/composer.json
test -d php-sdk/generated
test -f php-sdk/src/Connection/version.php
test ! -d php-sdk/sdk
grep -F "return '`+env.releaseVersion+`';" php-sdk/src/Connection/version.php
`); err != nil {
		return fmt.Errorf("check SDK mirror contents: %w", err)
	}
	return nil
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
import io
import json
import os
import re
import ssl
import struct
import tarfile
import threading
import time
import urllib.parse

records_path = "/records/events.jsonl"
os.makedirs(os.path.dirname(records_path), exist_ok=True)
github_content_records_dir = "/records/github-content"
github_release_records_dir = "/records/github-releases"
github_asset_records_dir = "/records/github-assets"
os.makedirs(github_content_records_dir, exist_ok=True)
os.makedirs(github_release_records_dir, exist_ok=True)
os.makedirs(github_asset_records_dir, exist_ok=True)
published_crates = {}
github_refs = {}
deleted_github_assets = set()
state_lock = threading.Lock()
existing_content_sha = "9999999999999999999999999999999999999999"

publish_check_tag = os.environ["PUBLISH_CHECK_TAG"]
publish_check_assets = [
    "dagger_" + publish_check_tag + "_darwin_amd64.tar.gz",
    "dagger_" + publish_check_tag + "_darwin_arm64.tar.gz",
    "dagger_" + publish_check_tag + "_linux_amd64.tar.gz",
    "dagger_" + publish_check_tag + "_linux_arm64.tar.gz",
    "dagger_" + publish_check_tag + "_linux_armv7.tar.gz",
    "dagger_" + publish_check_tag + "_windows_amd64.zip",
    "dagger_" + publish_check_tag + "_windows_arm64.zip",
    "checksums.txt",
]
existing_root_release_id = 42
existing_root_release_assets = [
    {"id": 9000 + i, "name": name}
    for i, name in enumerate(publish_check_assets)
]

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

def auth_header(handler):
    return handler.headers.get("authorization", "")

def multipart_field(body, name):
    marker = ('name="' + name + '"').encode("utf-8")
    idx = body.find(marker)
    if idx < 0:
        return ""
    start = body.find(b"\r\n\r\n", idx)
    if start < 0:
        return ""
    start += 4
    end = body.find(b"\r\n--", start)
    if end < 0:
        end = len(body)
    return body[start:end].decode("utf-8", "replace").strip()

def multipart_filename(body, name):
    marker = ('name="' + name + '"').encode("utf-8")
    idx = body.find(marker)
    if idx < 0:
        return ""
    header_end = body.find(b"\r\n\r\n", idx)
    if header_end < 0:
        return ""
    header = body[idx:header_end].decode("utf-8", "replace")
    match = re.search(r'filename="([^"]+)"', header)
    return match.group(1) if match else ""

def hex_package_version(body):
    candidates = [body]
    try:
        with tarfile.open(fileobj=io.BytesIO(body), mode="r:*") as archive:
            for member in archive.getmembers():
                extracted = archive.extractfile(member)
                if extracted is not None:
                    candidates.append(extracted.read())
    except tarfile.TarError:
        pass
    for candidate in candidates:
        text = candidate.decode("utf-8", "ignore")
        match = re.search(r'(?:version|vsn)[^0-9A-Za-z]+([0-9]+\.[0-9]+\.[0-9][0-9A-Za-z.+-]*)', text)
        if match:
            return match.group(1)
    return ""

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
    if (owner, repo, branch) == ("microsoft", "winget-pkgs", "master"):
        return "4444444444444444444444444444444444444444"
    if (owner, repo, branch) == ("dagger", "winget-pkgs", "master"):
        return "5555555555555555555555555555555555555555"
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

def write_github_asset_record(asset_name, body):
    with open(os.path.join(github_asset_records_dir, safe_filename(asset_name)), "wb") as f:
        f.write(body)

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
    record("cargo_publish", handler, body, {
        "crate_name": crate_name,
        "crate_version": crate_version,
        "deps_count": len(meta.get("deps") or []),
        "auth_header": auth_header(handler),
        "checksum": checksum,
    })
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
            record("netlify_list_deploys", self, b"", {"auth_header": auth_header(self)})
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
            tag = urllib.parse.unquote(self.path.split("/releases/tags/", 1)[1].split("?", 1)[0])
            record("github_release_lookup", self, b"", {"tag_name": tag})
            self.send_json(404, {"message": "Not Found"})
            return
        if self.path == "/api/v3/repos/dagger/dagger/releases/" + str(existing_root_release_id) + "/assets":
            record("github_release_asset_list", self, b"")
            with state_lock:
                assets = [asset for asset in existing_root_release_assets if asset["id"] not in deleted_github_assets]
            self.send_json(200, assets)
            return
        if self.path.startswith("/api/v3/repos/dagger/dagger/releases/") and self.path.endswith("/assets"):
            record("github_release_asset_list", self, b"")
            self.send_json(200, [])
            return
        if self.path in ("/api/v3", "/api/v3/"):
            record("github_api_root", self, b"")
            self.send_json(200, {"verifiable_password_authentication": False})
            return
        if self.path.startswith("/api/v3/repos/") and "/contents/" in self.path:
            record("github_content_lookup", self, b"")
            owner, repo, content_path = github_content_target(self.path)
            if (owner, repo, content_path) in {
                ("dagger", "homebrew-tap", "dagger.rb"),
                ("dagger", "nix", "pkgs/dagger/default.nix"),
            }:
                self.send_json(200, {"sha": existing_content_sha})
                return
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
            record("netlify_restore", self, body, {"auth_header": auth_header(self)})
            self.send_json(200, {"id": "deploy-1"})
            return
        if self.path.startswith("/pypi/"):
            record("pypi_publish", self, body, {
                "auth_header": auth_header(self),
                "content_type": self.headers.get("content-type", ""),
                "package_name": multipart_field(body, "name"),
                "package_version": multipart_field(body, "version"),
                "filetype": multipart_field(body, "filetype"),
                "filename": multipart_filename(body, "content"),
            })
            self.send_bytes(200, b"OK", "text/plain")
            return
        if self.path.startswith("/hex/api/packages/dagger/releases/") and self.path.endswith("/docs"):
            version = self.path.split("/releases/", 1)[-1].split("/docs", 1)[0]
            record("hex_docs_publish", self, body, {
                "auth_header": auth_header(self),
                "package_version": version,
            })
            self.send_etf(201, {}, {"location": "http://mock:8080/hexdocs/dagger/" + version})
            return
        if self.path.startswith("/hex/api/packages/dagger/releases"):
            version = hex_package_version(body)
            record("hex_publish", self, body, {
                "auth_header": auth_header(self),
                "package_version": version,
                "body_sha256": hashlib.sha256(body).hexdigest(),
            })
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
                "make_latest": payload.get("make_latest"),
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
            write_github_asset_record(asset_name, body)
            record("github_release_asset_upload", self, body, {
                "asset_name": asset_name,
                "body_sha256": hashlib.sha256(body).hexdigest(),
            })
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
                "sha": payload.get("sha", ""),
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
            if "tag_name" in payload:
                tag = payload.get("tag_name", "")
                write_github_release_record(tag, payload)
                record("github_release_update", self, body, {
                    "tag_name": tag,
                    "target_commitish": payload.get("target_commitish", ""),
                    "name": payload.get("name", ""),
                    "draft": payload.get("draft"),
                    "prerelease": payload.get("prerelease"),
                    "release_body": payload.get("body", ""),
                })
                self.send_json(200, {
                    "id": existing_root_release_id,
                    "tag_name": tag,
                    "html_url": "https://github.test/dagger/dagger/releases/tag/" + tag,
                    "upload_url": "https://github.test/api/uploads/repos/dagger/dagger/releases/" + str(existing_root_release_id) + "/assets{?name,label}",
                })
                return
            record("github_release_publish", self, body, {
                "draft": payload.get("draft"),
                "prerelease": payload.get("prerelease"),
            })
            self.send_json(200, {"id": existing_root_release_id, "tag_name": "mock", "html_url": "https://github.test/dagger/dagger/releases/tag/mock"})
            return
        record("patch", self, body)
        self.send_json(200, {})

    def do_DELETE(self):
        body = self.read_body()
        if self.path.startswith("/api/v3/repos/dagger/dagger/releases/assets/"):
            asset_id = int(self.path.rsplit("/", 1)[-1])
            with state_lock:
                deleted_github_assets.add(asset_id)
            asset_name = next((asset["name"] for asset in existing_root_release_assets if asset["id"] == asset_id), "")
            record("github_release_asset_delete", self, body, {
                "asset_id": asset_id,
                "asset_name": asset_name,
            })
            self.send_bytes(204, b"")
            return
        record("delete", self, body)
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
