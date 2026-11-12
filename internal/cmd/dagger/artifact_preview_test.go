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
	const input = `[{"Module":"app","Path":["app","verify"],"LocalPath":["verify"],"Type":"Check","Collections":[],"Items":[{"Keys":[]}]}]`
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
	require.Regexp(t, `(?m)^modules\.generate\.stale +dag\+check://`, table.String())
	require.NotContains(t, table.String(), "GOLANG-MODULE")
	require.NotContains(t, table.String(), "MODULE")
	require.Contains(t, cli.String(), "dagger check --golang --golang-module=. --name=modules.generate.stale")
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
	const input = `[{"Module":"golang","Path":["golang","modules"],"LocalPath":[],"Type":"GolangModule","Collections":[{"Fields":["modules"],"Type":"GolangModules","ItemType":"GolangModule"}],"Items":[{"Keys":["."]},{"Keys":["sdk"]}]}]`
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
				require.Contains(t, output.String(), "modules.packages.generate.stale")
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
	coordinates := []flattening.Coordinate{{Dimension: "module", Key: "app"}, {Dimension: "AppItems", Key: "a"}, {Dimension: "name", Key: "verify"}}
	target := artifact.AddressRow{Type: "Check", Coordinates: coordinates}
	for _, index := range []*artifact.AddressIndex{
		artifact.NewAddressIndex([]artifact.AddressRow{target}, false),
		artifact.NewAddressIndex([]artifact.AddressRow{target, target}, true),
	} {
		require.Equal(t, "dag+check://app/items/verify?item=a", artifactPreviewLink(entry, coordinates, index, map[string]string{"AppItems": "item"}))
	}
}

func TestArtifactPreviewRequiresLocalPath(t *testing.T) {
	const input = `[{"Module":"app","Path":["app","verify"],"Type":"Check","Items":[{"Keys":[]}]}]`
	var output, diagnostics bytes.Buffer
	err := PreviewArtifacts(strings.NewReader(input), &output, &diagnostics, []string{"check", "-la"})
	require.ErrorContains(t, err, "needs an explicit LocalPath")
	require.Empty(t, output.String())
}

func TestArtifactPreviewPreservesLiteralFieldNames(t *testing.T) {
	const input = `[
 {"Module":"app","Path":["app","health-check","run"],"LocalPath":["healthCheck","run"],"Type":"Check","Items":[{"Keys":[]}]},
 {"Module":"app","Path":["app","check"],"LocalPath":["check"],"Type":"Check","Items":[{"Keys":[]}]}
 ]`
	var output, diagnostics bytes.Buffer
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &output, &diagnostics, []string{"check", "-la", "--name=healthCheck.run", "-f=link"}))
	require.Equal(t, "dag+check://?name=healthCheck.run\n", output.String())
	require.Empty(t, diagnostics.String())
}

func TestArtifactPreviewExactCollectionTypeSelector(t *testing.T) {
	const input = `[{"Module":"golang","Path":["golang","modules","test"],"LocalPath":["test"],"Type":"Check","Collections":[{"Fields":["modules"],"Type":"GolangModules","ItemType":"GolangModule"}],"Items":[{"Keys":["."]},{"Keys":["sdk"]}]}]`
	var output, diagnostics bytes.Buffer
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &output, &diagnostics, []string{"check", "-l", "-f=link", "dag://?GolangModules=sdk"}))
	require.Equal(t, "dag+check://?golang-module=sdk\n", output.String())
	require.Empty(t, diagnostics.String())
}

func TestArtifactPreviewMatchesCanonicalCollectionKeys(t *testing.T) {
	row := listedArtifact{Name: "test"}
	row.Coordinates = append(row.Coordinates, struct{ Dimension, Key string }{"GolangModules", "sdk"})
	row.DimensionKeys = append(row.DimensionKeys,
		struct{ Dimension, Key string }{"/golang/modules", "old"},
		struct{ Dimension, Key string }{"type:Check", "golang/modules/test"})
	require.True(t, artifactPreviewMatches(row, []dagaddress.Pair{{Dimension: "GolangModules", Key: "sdk", HasKey: true}}, nil))
	require.False(t, artifactPreviewMatches(row, []dagaddress.Pair{{Dimension: "/golang/modules", Key: "old", HasKey: true}}, nil))
	require.True(t, artifactPreviewMatches(row, []dagaddress.Pair{{Dimension: "type:Check", Key: "golang/modules/test", HasKey: true}}, nil))
	row.Presence = []string{"GolangModules"}
	require.True(t, artifactPreviewMatches(row, []dagaddress.Pair{{Dimension: "GolangModules"}}, nil))
}

func TestArtifactPreviewSharedPathNeedsAbsentDimension(t *testing.T) {
	const input = `[
 {"Module":"grid","Path":["grid","rows","check"],"LocalPath":["check"],"Type":"Check","Collections":[{"Fields":["rows"],"Type":"Rows","ItemType":"Row"}],"Items":[{"Keys":["top"]}]},
 {"Module":"grid","Path":["grid","rows","check"],"LocalPath":["check"],"Type":"Check","Collections":[{"Fields":["rows"],"Type":"Rows","ItemType":"Row"},{"Fields":[],"Type":"Row","ItemType":"Cell"}],"Items":[{"Keys":["top","left"]}]}
 ]`
	t.Run("unsafe parent fails before output", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		err := PreviewArtifacts(strings.NewReader(input), &output, &diagnostics, []string{"check", "-la", "-f=link"})
		require.ErrorContains(t, err, "absent-dimension")
		require.Empty(t, output.String())
	})
	t.Run("child filter excludes unsafe parent", func(t *testing.T) {
		var output, diagnostics bytes.Buffer
		err := PreviewArtifacts(strings.NewReader(input), &output, &diagnostics, []string{"check", "-la", "-f=link", "--cell=left"})
		require.NoError(t, err)
		require.Equal(t, "dag+check://?cell=left\n", output.String())
		require.Empty(t, diagnostics.String())
	})
	t.Run("empty child permits a proven type-only parent link", func(t *testing.T) {
		capture := strings.Replace(input, `"Items":[{"Keys":["top","left"]}]`, `"Items":[]`, 1)
		var output, diagnostics bytes.Buffer
		err := PreviewArtifacts(strings.NewReader(capture), &output, &diagnostics, []string{"check", "-la", "-f=link"})
		require.NoError(t, err)
		require.Equal(t, "dag+check://\n", output.String())
		require.Empty(t, diagnostics.String())
	})
}

func TestArtifactPreviewNamesPreserveDistinctSourceFields(t *testing.T) {
	const input = `[
 {"Module":"app","Path":["app","before","check"],"LocalPath":["check"],"Type":"Check","Collections":[{"Fields":["before"],"Type":"Tests","ItemType":"Test"}],"Items":[{"Keys":["unit"]}]},
 {"Module":"app","Path":["app","after","check"],"LocalPath":["check"],"Type":"Check","Collections":[{"Fields":["after"],"Type":"Tests","ItemType":"Test"}],"Items":[{"Keys":["unit"]}]}
 ]`
	var links, table, diagnostics bytes.Buffer
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &links, &diagnostics, []string{"check", "-la", "-f=link"}))
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &table, &diagnostics, []string{"check", "-la"}))
	require.Contains(t, table.String(), "before.check")
	require.Contains(t, table.String(), "after.check")
	rows := strings.Split(strings.TrimSpace(links.String()), "\n")
	require.Len(t, rows, 2)
	for _, link := range rows {
		var replay, replayDiagnostics bytes.Buffer
		require.NoError(t, PreviewArtifacts(strings.NewReader(input), &replay, &replayDiagnostics, []string{"check", "-la", "-f=link", link}))
		require.Equal(t, link+"\n", replay.String())
		require.Empty(t, replayDiagnostics.String())
	}
	for _, name := range []string{"before.check", "after.check"} {
		var selected bytes.Buffer
		require.NoError(t, PreviewArtifacts(strings.NewReader(input), &selected, &diagnostics, []string{"check", "-la", "-f=link", "--name=" + name}))
		require.Equal(t, "dag+check://?name="+name+"\n", selected.String())
	}
	require.Empty(t, diagnostics.String())
}

func TestArtifactPreviewCollectionLabelsUseCompleteSchema(t *testing.T) {
	const input = `[
 {"Module":"app","Path":["app","packages","check"],"LocalPath":["check"],"Type":"Check","Collections":[{"Fields":["packages"],"Type":"Packages","ItemType":"Package"}],"Items":[{"Keys":["one"]},{"Keys":["two"]}]},
 {"Module":"app","Path":["app","dependencies","check"],"LocalPath":["check"],"Type":"Check","Collections":[{"Fields":["dependencies"],"Type":"Dependencies","ItemType":"Package"}],"Items":[{"Keys":["three"]},{"Keys":["four"]}]}
 ]`
	var all, selected, diagnostics bytes.Buffer
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &all, &diagnostics, []string{"check", "-la"}))
	require.Regexp(t, `(?m)^DEPENDENCIES +PACKAGES +NAME +LINK$`, all.String())
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &selected, &diagnostics, []string{"check", "-la", "--name=packages.check"}))
	require.Regexp(t, `(?m)^PACKAGES +LINK$`, selected.String())
	require.NotContains(t, selected.String(), "PACKAGE ")
	var links bytes.Buffer
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &links, &diagnostics, []string{"check", "-l", "--packages=one", "-f=link"}))
	require.Equal(t, "dag+check://?packages=one\n", links.String())
	require.Empty(t, diagnostics.String())
}

func TestArtifactPreviewReservedFlagDoesNotRenameColumn(t *testing.T) {
	field := flattening.Field{Module: "app", Type: "Check", LocalPath: []string{"check"}, Collections: []flattening.Collection{{Fields: []string{"items"}, Type: "Items", ItemType: "All"}}}
	schema, err := flattening.NewSchema([]flattening.Field{field})
	require.NoError(t, err)
	for _, dimension := range schema.Dimensions() {
		if dimension.Identifier == "Items" {
			require.Equal(t, "all", dimension.Name)
		}
	}
	const input = `[{"Module":"app","Path":["app","items","check"],"LocalPath":["check"],"Type":"Check","Collections":[{"Fields":["items"],"Type":"Items","ItemType":"All"}],"Items":[{"Keys":["one"]},{"Keys":["two"]}]}]`
	var table, cli, diagnostics bytes.Buffer
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &table, &diagnostics, []string{"check", "-la"}))
	require.Regexp(t, `(?m)^ALL +LINK$`, table.String())
	require.NotContains(t, table.String(), "ITEMS")
	require.Contains(t, table.String(), "?all=one")
	require.NoError(t, PreviewArtifacts(strings.NewReader(input), &cli, &diagnostics, []string{"check", "-la", "-f=cli"}))
	require.Contains(t, cli.String(), "--items=one")
	require.Contains(t, cli.String(), "--items=two")
	require.NotContains(t, cli.String(), "--all=")
	require.Empty(t, diagnostics.String())
}
