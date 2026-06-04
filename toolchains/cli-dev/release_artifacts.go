package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"dagger/cli-dev/internal/dagger"
)

type cliReleaseMode string

const (
	cliReleaseModeMain       cliReleaseMode = "main"
	cliReleaseModeStable     cliReleaseMode = "stable"
	cliReleaseModePrerelease cliReleaseMode = "prerelease"
)

type cliReleaseTarget struct {
	ID       string `json:"id"`
	Platform string `json:"platform"`
	Binary   string `json:"binary"`
	Windows  bool   `json:"windows"`
}

var cliReleaseTargets = []cliReleaseTarget{
	{ID: "darwin_amd64", Platform: "darwin/amd64", Binary: "dagger"},
	{ID: "darwin_arm64", Platform: "darwin/arm64", Binary: "dagger"},
	{ID: "linux_amd64", Platform: "linux/amd64", Binary: "dagger"},
	{ID: "linux_arm64", Platform: "linux/arm64", Binary: "dagger"},
	{ID: "linux_armv7", Platform: "linux/arm/v7", Binary: "dagger"},
	{ID: "windows_amd64", Platform: "windows/amd64", Binary: "dagger.exe", Windows: true},
	{ID: "windows_arm64", Platform: "windows/arm64", Binary: "dagger.exe", Windows: true},
}

func cliReleaseModeForTag(tag string) cliReleaseMode {
	if !semver.IsValid(tag) {
		return cliReleaseModeMain
	}
	if semver.Prerelease(tag) != "" {
		return cliReleaseModePrerelease
	}
	return cliReleaseModeStable
}

func cliReleaseLabels(tag, commit string) []string {
	if cliReleaseModeForTag(tag) == cliReleaseModeMain {
		return []string{commit, "head"}
	}
	return []string{tag}
}

func cliReleaseArchiveNames(label string) []string {
	names := make([]string, 0, len(cliReleaseTargets))
	for _, target := range cliReleaseTargets {
		ext := ".tar.gz"
		if target.Windows {
			ext = ".zip"
		}
		names = append(names, "dagger_"+label+"_"+target.ID+ext)
	}
	return names
}

func (cli *CliDev) releaseBinaries() *dagger.Directory {
	dir := dag.Directory()
	for _, target := range cliReleaseTargets {
		dir = dir.WithFile(
			target.ID+"/"+target.Binary,
			cli.Binary(dagger.Platform(target.Platform)),
		)
	}
	return dir
}

func (cli *CliDev) releaseDist(ctx context.Context, tag, commit string) (*dagger.Directory, error) {
	if commit == "" {
		return nil, fmt.Errorf("commit must be set")
	}

	targets, err := json.Marshal(cliReleaseTargets)
	if err != nil {
		return nil, err
	}

	ctr := dag.
		Alpine(dagger.AlpineOpts{
			Branch:   "3.22",
			Packages: []string{"python3"},
		}).
		Container().
		WithDirectory("/src", cli.Go.Source()).
		WithDirectory("/binaries", cli.releaseBinaries()).
		WithEnvVariable("RELEASE_LABELS", strings.Join(cliReleaseLabels(tag, commit), ",")).
		WithEnvVariable("RELEASE_TARGETS", string(targets)).
		WithNewFile("/package-release.py", packageReleaseScript).
		WithExec([]string{"python3", "/package-release.py"})

	return ctr.Directory("/dist"), nil
}

const packageReleaseScript = `import gzip
import hashlib
import json
import os
import shutil
import stat
import tarfile
import zipfile

labels = [label for label in os.environ["RELEASE_LABELS"].split(",") if label]
targets = json.loads(os.environ["RELEASE_TARGETS"])
dist = "/dist"
work = "/work"
license_path = "/src/LICENSE"

shutil.rmtree(dist, ignore_errors=True)
shutil.rmtree(work, ignore_errors=True)
os.makedirs(dist, exist_ok=True)
os.makedirs(work, exist_ok=True)

def add_zip_file(zf, src, arcname, mode):
    info = zipfile.ZipInfo(arcname)
    info.compress_type = zipfile.ZIP_DEFLATED
    info.external_attr = (stat.S_IFREG | mode) << 16
    with open(src, "rb") as f:
        zf.writestr(info, f.read())

def add_tar_file(tf, src, arcname, mode):
    info = tf.gettarinfo(src, arcname)
    info.mode = mode
    with open(src, "rb") as f:
        tf.addfile(info, f)

for label in labels:
    for target in targets:
        ext = ".zip" if target.get("windows") else ".tar.gz"
        archive = f"dagger_{label}_{target['id']}{ext}"
        binary = os.path.join("/binaries", target["id"], target["binary"])
        out = os.path.join(dist, archive)

        if target.get("windows"):
            with zipfile.ZipFile(out, "w") as zf:
                add_zip_file(zf, license_path, "LICENSE", 0o644)
                add_zip_file(zf, binary, target["binary"], 0o755)
        else:
            with gzip.GzipFile(out, "wb", mtime=0) as gz:
                with tarfile.open(fileobj=gz, mode="w") as tf:
                    add_tar_file(tf, license_path, "LICENSE", 0o644)
                    add_tar_file(tf, binary, target["binary"], 0o755)

checksums = []
for name in sorted(os.listdir(dist)):
    if name == "checksums.txt":
        continue
    with open(os.path.join(dist, name), "rb") as f:
        checksums.append((name, hashlib.sha256(f.read()).hexdigest()))

with open(os.path.join(dist, "checksums.txt"), "w", encoding="utf-8") as f:
    for name, checksum in checksums:
        f.write(f"{checksum}  {name}\n")
`
