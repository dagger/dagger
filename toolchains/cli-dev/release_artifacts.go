package main

import (
	"fmt"

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
		names = append(names, cliReleaseArchiveName(label, target))
	}
	return names
}

func cliReleaseArchiveName(label string, target cliReleaseTarget) string {
	ext := ".tar.gz"
	if target.Windows {
		ext = ".zip"
	}
	return "dagger_" + label + "_" + target.ID + ext
}

func (cli *CliDev) releaseBinary(target cliReleaseTarget) *dagger.File {
	return cli.Binary(dagger.Platform(target.Platform))
}

func (cli *CliDev) releaseBinaries() *dagger.Directory {
	dir := dag.Directory()
	for _, target := range cliReleaseTargets {
		dir = dir.WithFile(
			target.ID+"/"+target.Binary,
			cli.releaseBinary(target),
		)
	}
	return dir
}

func (cli *CliDev) releaseDist(tag, commit string) (*dagger.Directory, error) {
	if commit == "" {
		return nil, fmt.Errorf("commit must be set")
	}

	dist := dag.Directory()
	for _, label := range cliReleaseLabels(tag, commit) {
		for _, target := range cliReleaseTargets {
			name := cliReleaseArchiveName(label, target)
			dist = dist.WithFile(name, cli.releaseArchive(label, target))
		}
	}

	return dist.WithFile("checksums.txt", cli.releaseChecksums(dist)), nil
}

func (cli *CliDev) releaseArchive(label string, target cliReleaseTarget) *dagger.File {
	name := cliReleaseArchiveName(label, target)
	input := dag.Directory().
		WithFile("LICENSE", cli.Go.Source().File("LICENSE"), dagger.DirectoryWithFileOpts{
			Permissions: 0o644,
		}).
		WithFile(target.Binary, cli.releaseBinary(target), dagger.DirectoryWithFileOpts{
			Permissions: 0o755,
		})

	cmd := `mkdir -p /out
if [ "$WINDOWS" = "true" ]; then
	cd /input
	touch -t 198001010000 LICENSE "$BINARY"
	zip -X -q "/out/$ARCHIVE" LICENSE "$BINARY"
else
	tar --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner -czf "/out/$ARCHIVE" -C /input LICENSE "$BINARY"
fi`

	return releaseArchiveBase().
		WithDirectory("/input", input).
		WithEnvVariable("ARCHIVE", name).
		WithEnvVariable("BINARY", target.Binary).
		WithEnvVariable("WINDOWS", fmt.Sprintf("%t", target.Windows)).
		WithExec([]string{"sh", "-ec", cmd}).
		File("/out/" + name)
}

func (cli *CliDev) releaseChecksums(dist *dagger.Directory) *dagger.File {
	return dag.
		Alpine(dagger.AlpineOpts{
			Branch: "3.22",
		}).
		Container().
		WithDirectory("/dist", dist).
		WithExec([]string{"sh", "-ec", `mkdir -p /out
cd /dist
for file in $(ls | sort); do
	sha256sum "$file"
done > /out/checksums.txt`}).
		File("/out/checksums.txt")
}

func releaseArchiveBase() *dagger.Container {
	return dag.
		Alpine(dagger.AlpineOpts{
			Branch:   "3.22",
			Packages: []string{"tar", "gzip", "zip"},
		}).
		Container()
}
