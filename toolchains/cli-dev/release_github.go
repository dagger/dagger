package main

import (
	"context"
	"encoding/json"
	"strings"

	"dagger/cli-dev/internal/dagger"
)

func (cli *CliDev) githubHelper(
	githubToken *dagger.Secret,
	githubHost string,
	githubCaCert *dagger.File,
	packages []string,
) *dagger.Container {
	ctr := dag.
		Alpine(dagger.AlpineOpts{
			Branch:   "3.22",
			Packages: packages,
		}).
		Container().
		With(withGithubCaCert(githubCaCert)).
		With(optSecretVariable("GITHUB_TOKEN", githubToken)).
		WithEnvVariable("GITHUB_API_URL", githubAPIURL(githubHost))
	return ctr
}

func (cli *CliDev) publishRootGitHubRelease(
	ctx context.Context,
	dist *dagger.Directory,
	tag string,
	commit string,
	notes string,
	githubOrgName string,
	githubToken *dagger.Secret,
	githubHost string,
	githubCaCert *dagger.File,
) error {
	assets, err := json.Marshal(append(cliReleaseArchiveNames(tag), "checksums.txt"))
	if err != nil {
		return err
	}

	ctr := cli.githubHelper(githubToken, githubHost, githubCaCert, []string{"ca-certificates", "python3"}).
		WithMountedDirectory("/dist", dist).
		WithNewFile("/release-body.md", notes).
		WithNewFile("/publish-root-release.py", publishRootReleaseScript).
		WithEnvVariable("GITHUB_ORG_NAME", githubOrgName).
		WithEnvVariable("RELEASE_TAG", tag).
		WithEnvVariable("RELEASE_COMMIT", commit).
		WithEnvVariable("RELEASE_ASSETS", string(assets)).
		WithExec([]string{"python3", "/publish-root-release.py"})

	_, err = ctr.Sync(ctx)
	return err
}

func (cli *CliDev) publishPackageManagers(
	ctx context.Context,
	dist *dagger.Directory,
	tag string,
	githubOrgName string,
	githubToken *dagger.Secret,
	githubHost string,
	githubCaCert *dagger.File,
	artefactsFQDN string,
) error {
	ctr := cli.githubHelper(githubToken, githubHost, githubCaCert, []string{"ca-certificates", "coreutils", "curl", "python3", "xz"}).
		WithDirectory("/nix", dag.Directory()).
		WithNewFile("/etc/nix/nix.conf", `build-users-group =`).
		WithExec([]string{"sh", "-ec", "curl -fsSL https://nixos.org/nix/install | sh -s -- --no-daemon"})
	path, err := ctr.EnvVariable(ctx, "PATH")
	if err != nil {
		return err
	}

	ctr = ctr.
		WithEnvVariable("PATH", path+":/nix/var/nix/profiles/default/bin").
		WithExec([]string{"nix-hash", "--version"}).
		WithMountedDirectory("/dist", dist).
		WithNewFile("/publish-package-managers.py", publishPackageManagersScript).
		WithEnvVariable("GITHUB_ORG_NAME", githubOrgName).
		WithEnvVariable("RELEASE_TAG", tag).
		WithEnvVariable("RELEASE_VERSION", strings.TrimPrefix(tag, "v")).
		WithEnvVariable("ARTEFACTS_FQDN", artefactsFQDN).
		WithExec([]string{"python3", "/publish-package-managers.py"})

	_, err = ctr.Sync(ctx)
	return err
}

const publishRootReleaseScript = `import json
import os
import urllib.error
import urllib.parse
import urllib.request

api_base = os.environ["GITHUB_API_URL"].rstrip("/")
org = os.environ["GITHUB_ORG_NAME"]
tag = os.environ["RELEASE_TAG"]
commit = os.environ["RELEASE_COMMIT"]
assets = json.loads(os.environ["RELEASE_ASSETS"])
token = os.environ.get("GITHUB_TOKEN", "")

with open("/release-body.md", encoding="utf-8") as f:
    release_body = f.read()

def request(method, url, payload=None, content_type="application/json"):
    data = None
    headers = {
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    if token:
        headers["Authorization"] = "Bearer " + token
    if payload is not None:
        if isinstance(payload, bytes):
            data = payload
        else:
            data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = content_type
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            body = resp.read()
            return resp.status, json.loads(body.decode("utf-8") or "{}") if body else {}
    except urllib.error.HTTPError as err:
        body = err.read().decode("utf-8", errors="replace")
        if err.code == 404:
            return err.code, {}
        raise RuntimeError(f"{method} {url} failed: {err.code} {body}") from err

status, release = request("GET", f"{api_base}/repos/{org}/dagger/releases/tags/{urllib.parse.quote(tag, safe='')}")
payload = {
    "tag_name": tag,
    "name": tag,
    "target_commitish": commit,
    "body": release_body,
    "draft": True,
    "prerelease": False,
}
if status == 404:
    _, release = request("POST", f"{api_base}/repos/{org}/dagger/releases", payload)
else:
    release_id = release["id"]
    payload["draft"] = release.get("draft", True)
    _, release = request("PATCH", f"{api_base}/repos/{org}/dagger/releases/{release_id}", payload)

release_id = release["id"]
upload_url = release["upload_url"].split("{", 1)[0]
for asset in assets:
    with open(os.path.join("/dist", asset), "rb") as f:
        data = f.read()
    url = upload_url + "?name=" + urllib.parse.quote(asset)
    request("POST", url, data, "application/octet-stream")

request("PATCH", f"{api_base}/repos/{org}/dagger/releases/{release_id}", {"draft": False})
`

const publishPackageManagersScript = `import base64
import datetime
import json
import os
import subprocess
import urllib.error
import urllib.parse
import urllib.request

api_base = os.environ["GITHUB_API_URL"].rstrip("/")
org = os.environ["GITHUB_ORG_NAME"]
tag = os.environ["RELEASE_TAG"]
version = os.environ["RELEASE_VERSION"]
artefacts_fqdn = os.environ["ARTEFACTS_FQDN"]
token = os.environ.get("GITHUB_TOKEN", "")
base_url = f"https://{artefacts_fqdn}/dagger/releases/{version}"
generated = "This file was generated by Dagger release tooling. DO NOT EDIT."

def request(method, path, payload=None):
    url = api_base + path
    data = None
    headers = {
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    if token:
        headers["Authorization"] = "Bearer " + token
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            body = resp.read()
            return resp.status, json.loads(body.decode("utf-8") or "{}") if body else {}
    except urllib.error.HTTPError as err:
        body = err.read().decode("utf-8", errors="replace")
        if err.code == 404:
            return err.code, {}
        raise RuntimeError(f"{method} {url} failed: {err.code} {body}") from err

def q(value):
    return urllib.parse.quote(value, safe="")

def read_checksums():
    checksums = {}
    with open("/dist/checksums.txt", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            checksum, name = line.split(None, 1)
            checksums[name] = checksum
    return checksums

checksums = read_checksums()

def archive(suffix):
    name = f"dagger_{tag}_{suffix}"
    if name not in checksums:
        raise RuntimeError(f"missing checksum for {name}")
    return name

def nix_hash(name):
    return subprocess.check_output([
        "nix-hash", "--type", "sha256", "--flat", "--base32", os.path.join("/dist", name),
    ], text=True).strip()

def default_branch(owner, repo):
    _, data = request("GET", f"/repos/{q(owner)}/{q(repo)}")
    return data.get("default_branch") or ("master" if repo == "winget-pkgs" else "main")

def ensure_branch(owner, repo, branch):
    status, _ = request("GET", f"/repos/{q(owner)}/{q(repo)}/branches/{q(branch)}")
    if status != 404:
        return
    base = default_branch(owner, repo)
    _, ref = request("GET", f"/repos/{q(owner)}/{q(repo)}/git/ref/heads/{q(base)}")
    sha = ref["object"]["sha"]
    request("POST", f"/repos/{q(owner)}/{q(repo)}/git/refs", {
        "ref": "refs/heads/" + branch,
        "sha": sha,
    })

def write_content(owner, repo, path, content, branch, message):
    status, existing = request("GET", f"/repos/{q(owner)}/{q(repo)}/contents/{q(path)}?ref={q(branch)}")
    payload = {
        "message": message,
        "content": base64.b64encode(content.encode("utf-8")).decode("ascii"),
        "branch": branch,
        "committer": {
            "name": "dagger-bot",
            "email": "noreply@dagger.io",
        },
    }
    if status != 404 and existing.get("sha"):
        payload["sha"] = existing["sha"]
    request("PUT", f"/repos/{q(owner)}/{q(repo)}/contents/{q(path)}", payload)

def homebrew_formula():
    archives = {
        "darwin_amd64": archive("darwin_amd64.tar.gz"),
        "darwin_arm64": archive("darwin_arm64.tar.gz"),
        "linux_amd64": archive("linux_amd64.tar.gz"),
        "linux_arm64": archive("linux_arm64.tar.gz"),
    }
    return f'''# typed: false
# frozen_string_literal: true

# {generated}
class Dagger < Formula
  desc "Dagger is an integrated platform to orchestrate the delivery of applications"
  homepage "https://dagger.io"
  version "{version}"

  on_macos do
    if Hardware::CPU.intel?
      url "{base_url}/{archives["darwin_amd64"]}"
      sha256 "{checksums[archives["darwin_amd64"]]}"

      def install
        bin.install "dagger"
      end
    end

    if Hardware::CPU.arm?
      url "{base_url}/{archives["darwin_arm64"]}"
      sha256 "{checksums[archives["darwin_arm64"]]}"

      def install
        bin.install "dagger"
      end
    end
  end

  on_linux do
    if Hardware::CPU.intel? and Hardware::CPU.is_64_bit?
      url "{base_url}/{archives["linux_amd64"]}"
      sha256 "{checksums[archives["linux_amd64"]]}"

      def install
        bin.install "dagger"
      end
    end

    if Hardware::CPU.arm? and Hardware::CPU.is_64_bit?
      url "{base_url}/{archives["linux_arm64"]}"
      sha256 "{checksums[archives["linux_arm64"]]}"

      def install
        bin.install "dagger"
      end
    end
  end

  test do
    system "#{{bin}}/dagger version"
  end
end
'''

def nix_package():
    archives = {
        "x86_64-linux": archive("linux_amd64.tar.gz"),
        "armv7l-linux": archive("linux_armv7.tar.gz"),
        "aarch64-linux": archive("linux_arm64.tar.gz"),
        "x86_64-darwin": archive("darwin_amd64.tar.gz"),
        "aarch64-darwin": archive("darwin_arm64.tar.gz"),
    }
    sha_lines = "\n".join(f'    {platform} = "{nix_hash(name)}";' for platform, name in archives.items())
    url_lines = "\n".join(f'    {platform} = "{base_url}/{name}";' for platform, name in archives.items())
    platform_lines = "\n".join(f'      "{platform}"' for platform in archives)
    return f'''# {generated}
# vim: set ft=nix ts=2 sw=2 sts=2 et sta
{{
system ? builtins.currentSystem
, lib
, fetchurl
, installShellFiles
, stdenvNoCC
}}:
let
  shaMap = {{
{sha_lines}
  }};

  urlMap = {{
{url_lines}
  }};
in
stdenvNoCC.mkDerivation {{
  pname = "dagger";
  version = "{version}";
  src = fetchurl {{
    url = urlMap.${{system}};
    sha256 = shaMap.${{system}};
  }};

  sourceRoot = ".";

  nativeBuildInputs = [ installShellFiles ];

  installPhase = ''
    install -Dm755 dagger $out/bin/dagger
  '';

  postInstall = ''
    installShellCompletion --cmd dagger \\
      --bash <($out/bin/dagger completion bash) \\
      --fish <($out/bin/dagger completion fish) \\
      --zsh <($out/bin/dagger completion zsh)
  '';

  system = system;

  meta = {{
    description = "Dagger is an integrated platform to orchestrate the delivery of applications";
    homepage = "https://dagger.io";
    license = lib.licenses.asl20;

    sourceProvenance = [ lib.sourceTypes.binaryNativeCode ];

    platforms = [
{platform_lines}
    ];
  }};
}}
'''

def winget_manifests():
    win_amd64 = archive("windows_amd64.zip")
    win_arm64 = archive("windows_arm64.zip")
    release_date = datetime.datetime.utcnow().strftime("%Y-%m-%d")
    root = f"manifests/d/Dagger/Cli/{version}"
    version_manifest = f'''# {generated}
# yaml-language-server: $schema=https://aka.ms/winget-manifest.version.1.10.0.schema.json
PackageIdentifier: Dagger.Cli
PackageVersion: {version}
DefaultLocale: en-US
ManifestType: version
ManifestVersion: 1.10.0
'''
    installer_manifest = f'''# {generated}
# yaml-language-server: $schema=https://aka.ms/winget-manifest.installer.1.10.0.schema.json
PackageIdentifier: Dagger.Cli
PackageVersion: {version}
InstallerLocale: en-US
InstallerType: zip
ReleaseDate: "{release_date}"
Installers:
  - Architecture: arm64
    NestedInstallerType: portable
    NestedInstallerFiles:
      - RelativeFilePath: dagger.exe
        PortableCommandAlias: dagger
    InstallerUrl: {base_url}/{win_arm64}
    InstallerSha256: {checksums[win_arm64]}
    UpgradeBehavior: uninstallPrevious
  - Architecture: x64
    NestedInstallerType: portable
    NestedInstallerFiles:
      - RelativeFilePath: dagger.exe
        PortableCommandAlias: dagger
    InstallerUrl: {base_url}/{win_amd64}
    InstallerSha256: {checksums[win_amd64]}
    UpgradeBehavior: uninstallPrevious
ManifestType: installer
ManifestVersion: 1.10.0
'''
    locale_manifest = f'''# {generated}
# yaml-language-server: $schema=https://aka.ms/winget-manifest.defaultLocale.1.10.0.schema.json
PackageIdentifier: Dagger.Cli
PackageVersion: {version}
PackageLocale: en-US
Publisher: Dagger
PublisherUrl: https://dagger.io
PublisherSupportUrl: https://github.com/dagger/dagger/issues/new/choose
PackageName: dagger
PackageUrl: https://dagger.io
License: asl20
ShortDescription: Dagger is an integrated platform to orchestrate the delivery of applications
Tags:
  - dagger
  - cli
  - cicd
  - workflows
  - sandbox
  - containers
  - devops
  - llm
ManifestType: defaultLocale
ManifestVersion: 1.10.0
'''
    return {
        f"{root}/Dagger.Cli.yaml": version_manifest,
        f"{root}/Dagger.Cli.installer.yaml": installer_manifest,
        f"{root}/Dagger.Cli.locale.en-US.yaml": locale_manifest,
    }

write_content(org, "homebrew-tap", "dagger.rb", homebrew_formula(), "main", f"Brew formula update for dagger version {tag}")
write_content(org, "nix", "pkgs/dagger/default.nix", nix_package(), "main", f"dagger:  -> {tag}")

winget_branch = f"dagger-{version}"
ensure_branch(org, "winget-pkgs", winget_branch)
request("POST", f"/repos/{q(org)}/winget-pkgs/merge-upstream", {"branch": "master"})
for path, content in winget_manifests().items():
    if path.endswith(".installer.yaml"):
        suffix = "installer"
    elif path.endswith(".locale.en-US.yaml"):
        suffix = "locale"
    else:
        suffix = "version"
    write_content(org, "winget-pkgs", path, content, winget_branch, f"New version: Dagger.Cli {version}: add {suffix}")

request("POST", "/repos/microsoft/winget-pkgs/pulls", {
    "title": f"New version: Dagger.Cli {version}",
    "base": "master",
    "head": f"{org}:winget-pkgs:{winget_branch}",
    "body": "Automated with Dagger release tooling.",
})
`
