package daggercmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/stretchr/testify/require"
)

func TestArtifactPreviewFlagsAreIndependent(t *testing.T) {
	const input = `[{"Module":"app","Path":["app","verify"],"Type":"Check","Collections":[],"Items":[{"Keys":[]}]}]`
	var links, table, diagnostics bytes.Buffer
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &links, &diagnostics, []string{"check", "-la", "-f=link"}))
	require.Equal(t, "dag+check://\n", links.String())
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &table, &diagnostics, []string{"check", "-l"}))
	require.Equal(t, "LINK\ndag+check://app/verify\n", table.String())
}

func TestArtifactPreviewCanonicalTable(t *testing.T) {
	const input = `[
 {"Module":"golang","Path":["golang","modules","generate","stale"],"LocalPath":["generate","stale"],"Type":"Check","Collections":[{"Fields":["modules"],"Type":"GolangModules","ItemType":"GolangModule"}],"Items":[{"Keys":["."]},{"Keys":["sdk"]}]},
 {"Module":"golang","Path":["golang","modules","test"],"LocalPath":["test"],"Type":"Check","Collections":[{"Fields":["modules"],"Type":"GolangModules","ItemType":"GolangModule"}],"Items":[{"Keys":["."]},{"Keys":["sdk"]}]}
 ]`
	var table, links, cli, diagnostics bytes.Buffer
	for _, pair := range []struct {
		format string
		output *bytes.Buffer
	}{{"table", &table}, {"link", &links}, {"cli", &cli}} {
		require.NoError(t, PreviewArtifacts(strings.NewReader(input), pair.output, &diagnostics, []string{"check", "-l", "--golang-module=.", "-f=" + pair.format}))
	}
	require.Regexp(t, `(?m)^NAME +LINK$`, table.String())
	require.Regexp(t, `(?m)^generate\.stale +dag\+check://`, table.String())
	require.NotContains(t, table.String(), "GOLANG-MODULE")
	require.NotContains(t, table.String(), "MODULE")
	require.Contains(t, cli.String(), "dagger check --golang --golang-module=. --name=generate.stale")
	selected := strings.Split(strings.TrimSpace(links.String()), "\n")
	require.Len(t, selected, 2)
	for _, link := range selected {
		require.Contains(t, table.String(), link)
		var replay, replayDiagnostics bytes.Buffer
		require.NoError(t, PreviewArtifacts(strings.NewReader(input), &replay, &replayDiagnostics, []string{"check", "-l", "-f=link", link}))
		require.Equal(t, link+"\n", replay.String())
		require.Empty(t, replayDiagnostics.String())
	}
	require.Len(t, strings.Split(strings.TrimSpace(cli.String()), "\n"), 2)
	require.Empty(t, diagnostics.String())
}

func TestArtifactPreviewCollectionListsItems(t *testing.T) {
	const input = `[{"Module":"golang","Path":["golang","modules"],"Type":"GolangModule","Collections":[{"Fields":["modules"],"Type":"GolangModules","ItemType":"GolangModule"}],"Items":[{"Keys":["."]},{"Keys":["sdk"]}]}]`
	var table, links, cli, diagnostics bytes.Buffer
	for _, pair := range []struct {
		format string
		output *bytes.Buffer
	}{{"table", &table}, {"link", &links}, {"cli", &cli}} {
		require.NoError(t, PreviewArtifacts(strings.NewReader(input), pair.output, &diagnostics, []string{"list", "golang-modules", "-f=" + pair.format}))
	}
	require.Regexp(t, `(?m)^GOLANG-MODULE +LINK$`, table.String())
	require.Regexp(t, `(?m)^\. +dag\+golang-module://`, table.String())
	require.Regexp(t, `(?m)^sdk +dag\+golang-module://`, table.String())
	require.NotContains(t, table.String(), "*")
	require.Len(t, strings.Split(strings.TrimSpace(links.String()), "\n"), 2)
	require.Equal(t, "dagger list golang-modules --golang --golang-module=.\ndagger list golang-modules --golang --golang-module=sdk\n", cli.String())
	require.Empty(t, diagnostics.String())
}

func TestArtifactPreviewLinksUseCompleteCapturedItems(t *testing.T) {
	const schema = `[{"Module":"docker","Path":["docker","services","containers"],"LocalPath":[],"Type":"Container","Collections":[{"Fields":["services"],"Type":"Services","ItemType":"Service"},{"Fields":["containers"],"Type":"Containers","ItemType":"Container"}],"Items":%s}]`
	links := func(input string, selector ...string) []string {
		t.Helper()
		var output, diagnostics bytes.Buffer
		args := append([]string{"list", "containers", "-f=link"}, selector...)
		require.NoError(t, PreviewArtifacts(strings.NewReader(input), &output, &diagnostics, args))
		require.Empty(t, diagnostics.String())
		return strings.Split(strings.TrimSpace(output.String()), "\n")
	}
	t.Run("globally unique child omits parent", func(t *testing.T) {
		input := fmt.Sprintf(schema, `[{"Keys":["api","unit"]},{"Keys":["cli","lint"]}]`)
		output := links(input)
		require.Len(t, output, 2)
		for _, link := range output {
			address, err := dagaddress.Parse(link)
			require.NoError(t, err)
			require.Len(t, address.Query, 1)
			require.Equal(t, "container", address.Query[0].Dimension)
			require.Contains(t, []string{"unit", "lint"}, address.Query[0].Key)
		}
	})
	t.Run("duplicate children retain parent after selection", func(t *testing.T) {
		input := fmt.Sprintf(schema, `[{"Keys":["api","unit"]},{"Keys":["api","lint"]},{"Keys":["cli","unit"]},{"Keys":["cli","lint"]}]`)
		all := links(input)
		selected := links(input, "dag://?service=api")
		require.Len(t, all, 4)
		require.Len(t, selected, 2)
		for _, link := range selected {
			require.Contains(t, all, link)
			address, err := dagaddress.Parse(link)
			require.NoError(t, err)
			require.Len(t, address.Query, 2)
			require.Equal(t, []dagaddress.Pair{{Dimension: "service", Key: "api", HasKey: true}, {Dimension: "container", Key: address.Query[1].Key, HasKey: true}}, address.Query)
		}
	})
}

func TestArtifactPreviewNameFlag(t *testing.T) {
	const input = `[
 {"Module":"app","Path":["app","generate","stale"],"LocalPath":["generate","stale"],"Type":"Check","Items":[{"Keys":[]}]},
 {"Module":"app","Path":["app","test"],"LocalPath":["test"],"Type":"Check","Items":[{"Keys":[]}]}
 ]`
	var output, diagnostics bytes.Buffer
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &output, &diagnostics, []string{"check", "-la", "--name=generate.stale", "-f=link"}))
	require.Equal(t, "dag+check://?name=generate.stale\n", output.String())
	require.Empty(t, diagnostics.String())
}

func TestArtifactPreviewFilteredChecksKeepEachItem(t *testing.T) {
	const input = `[
 {"Module":"golang","Path":["golang","modules","packages","generate","stale"],"LocalPath":["generate","stale"],"Type":"Check","Collections":[{"Fields":["modules"],"Type":"GolangModules","ItemType":"GolangModule"},{"Fields":["packages"],"Type":"GolangPackages","ItemType":"GolangPackage"}],"Items":[{"Keys":[".","pkg"]}]},
 {"Module":"golang","Path":["golang","modules","packages","tests","test"],"LocalPath":["test"],"Type":"Check","Collections":[{"Fields":["modules"],"Type":"GolangModules","ItemType":"GolangModule"},{"Fields":["packages"],"Type":"GolangPackages","ItemType":"GolangPackage"},{"Fields":["tests"],"Type":"GolangTests","ItemType":"GolangTest"}],"Items":[{"Keys":[".","pkg","One"]},{"Keys":[".","pkg","Two"]},{"Keys":[".","pkg","Three"]},{"Keys":[".","pkg","Four"]},{"Keys":[".","pkg","Five"]},{"Keys":[".","pkg","Six"]},{"Keys":[".","pkg","Seven"]}]}
 ]`
	for _, format := range []string{"table", "link", "cli"} {
		t.Run(format, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			require.NoError(t, PreviewArtifacts(strings.NewReader(input), &output, &diagnostics, []string{"check", "-l", "--golang-module=.", "-f=" + format}))
			expectedLines := 8
			if format == "table" {
				expectedLines++
				require.Regexp(t, `(?m)^GOLANG-TEST +NAME +LINK$`, output.String())
				require.Contains(t, output.String(), "generate.stale")
				require.NotContains(t, output.String(), "GOLANG-MODULE")
				require.NotContains(t, output.String(), "GOLANG-PACKAGE")
			}
			require.Len(t, strings.Split(strings.TrimSpace(output.String()), "\n"), expectedLines)
			require.NotContains(t, output.String(), ", ")
			require.NotContains(t, output.String(), "type:Check=")
			require.Empty(t, diagnostics.String())
		})
	}
}

func TestArtifactPreviewLinkFallback(t *testing.T) {
	entry := artifactPreviewEntry{Artifact: flattening.Artifact{Module: "app", Path: []string{"app", "items", "verify"}, Type: "Check"}, LocalPath: []string{"verify"}}
	row := flattening.Row{Coordinates: []flattening.Coordinate{{Dimension: "/app/items", Key: "a"}}}
	target := artifact.AddressRow{Type: "Check", Coordinates: artifactPreviewCoordinates(entry, row.Coordinates)}
	for _, index := range []*artifact.AddressIndex{
		artifact.NewAddressIndex([]artifact.AddressRow{target}, false),
		artifact.NewAddressIndex([]artifact.AddressRow{target, target}, true),
	} {
		require.Equal(t, "dag+check://app/items/verify?item=a", artifactPreviewLink(entry, row, index, map[string]string{"/app/items": "item"}))
	}
}
