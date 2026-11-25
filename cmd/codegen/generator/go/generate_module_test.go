package gogenerator

import (
	"go/version"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/psanford/memfs"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

func TestSyncModReplaceAndTidyPinsDaggerWithoutUpdatingTransitiveDeps(t *testing.T) {
	t.Parallel()

	mod, err := modfile.Parse("go.mod", []byte("module example.com/test\n\ngo 1.25.0\n"), nil)
	require.NoError(t, err)

	genSt := &generator.GeneratedState{}
	g := GoGenerator{
		Config: generator.Config{
			OutputDir: t.TempDir(),
			ModuleConfig: &generator.ModuleGeneratorConfig{
				ModuleName:       "test",
				ModuleSourcePath: ".",
				LibVersion:       "v1.2.3",
			},
		},
	}

	require.NoError(t, g.syncModReplaceAndTidy(mod, genSt, "."))

	var goGetArgs []string
	for _, cmd := range genSt.PostCommands {
		if len(cmd.Args) >= 2 && cmd.Args[0] == "go" && cmd.Args[1] == "get" {
			goGetArgs = cmd.Args
			break
		}
	}

	require.Equal(t, []string{"go", "get", "dagger.io/dagger@v1.2.3"}, goGetArgs)
}

func TestSyncModReplaceAndTidyDropsStaleOTelLogReplaces(t *testing.T) {
	t.Parallel()

	const goMod = `module example.com/test

go 1.25.0

replace go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.16.0

replace go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp => go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.16.0 // pinned

replace (
	go.opentelemetry.io/otel/log => go.opentelemetry.io/otel/log v0.8.0
	example.com/other => example.com/other v0.16.0
)

replace go.opentelemetry.io/otel/sdk/log => ../my-sdk-log

replace go.opentelemetry.io/otel/exporters/otlp/otlptrace => go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.40.0
`
	// Codegen parses go.mod laxly, which leaves replaces out of mod.Replace;
	// strictly parsed, they're there too.
	for name, parse := range map[string]func(string, []byte, modfile.VersionFixer) (*modfile.File, error){
		"lax":    modfile.ParseLax,
		"strict": modfile.Parse,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mod, err := parse("go.mod", []byte(goMod), nil)
			require.NoError(t, err)

			g := GoGenerator{
				Config: generator.Config{
					OutputDir: t.TempDir(),
					ModuleConfig: &generator.ModuleGeneratorConfig{
						ModuleName:       "test",
						ModuleSourcePath: ".",
						LibVersion:       "v1.2.3",
					},
				},
			}
			require.NoError(t, g.syncModReplaceAndTidy(mod, &generator.GeneratedState{}, "."))
			for _, rep := range mod.Replace {
				_, stale := staleSDKReplace([]string{rep.Old.Path, "=>", rep.New.Path, rep.New.Version})
				require.False(t, stale, "a dropped replace is left in mod.Replace: %v", rep.Old)
			}

			body, err := mod.Format()
			require.NoError(t, err)
			formatted, err := modfile.Parse("go.mod", body, nil)
			require.NoError(t, err)
			replaced := map[string]string{}
			for _, rep := range formatted.Replace {
				replaced[rep.Old.Path] = rep.New.Path + "@" + rep.New.Version
			}
			require.Equal(t, map[string]string{
				// replaces the SDK never wrote stay
				"go.opentelemetry.io/otel/sdk/log":                  "../my-sdk-log@",
				"example.com/other":                                 "example.com/other@v0.16.0",
				"go.opentelemetry.io/otel/exporters/otlp/otlptrace": "go.opentelemetry.io/otel/exporters/otlp/otlptrace@v1.40.0",
			}, replaced)
			require.NotContains(t, string(body), "pinned", "a dropped replace's comment goes with it")
		})
	}
}

// Earlier SDKs wrote their replaces into a go.work too: they go, whatever the
// module's own go.mod holds. While another module of the workspace still has
// them, the go.work overrides its pins with the SDK's versions instead.
func TestSyncModReplaceAndTidyFixesStaleOTelLogGoWorkReplaces(t *testing.T) {
	t.Parallel()

	sdkMod, err := modfile.Parse("go.mod", dagger.GoMod, nil)
	require.NoError(t, err)
	sdkRequires := map[string]string{}
	for _, req := range sdkMod.Require {
		sdkRequires[req.Mod.Path] = req.Mod.Version
	}
	const (
		logPath    = "go.opentelemetry.io/otel/log"
		sdkLogPath = "go.opentelemetry.io/otel/sdk/log"
	)
	require.NotEmpty(t, sdkRequires[logPath])

	const goWork = `go 1.25.0

use (
	./test
	./other
)

replace go.opentelemetry.io/otel/log => go.opentelemetry.io/otel/log v0.16.0

replace go.opentelemetry.io/otel/sdk/log v0.16.0 => go.opentelemetry.io/otel/sdk/log v0.16.0

replace go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp => ../otlploghttp
`
	for name, tc := range map[string]struct {
		otherGoMod string
		want       []string
	}{
		"no other module pins": {
			otherGoMod: "module example.com/other\n\ngo 1.25.0\n",
			want:       []string{"-dropreplace", logPath, "-dropreplace", sdkLogPath + "@v0.16.0"},
		},
		"another module pins": {
			otherGoMod: "module example.com/other\n\ngo 1.25.0\n\nreplace " + logPath + " => " + logPath + " v0.16.0\n",
			want: []string{
				"-replace", logPath + "=" + logPath + "@" + sdkRequires[logPath],
				"-dropreplace", sdkLogPath + "@v0.16.0",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for _, mod := range []struct{ dir, goMod string }{
				{"test", "module example.com/test\n\ngo 1.25.0\n"},
				{"other", tc.otherGoMod},
			} {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, mod.dir), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(dir, mod.dir, "go.mod"), []byte(mod.goMod), 0o600))
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "go.work"), []byte(goWork), 0o600))
			mod, err := modfile.ParseLax("go.mod", []byte("module example.com/test\n\ngo 1.25.0\n"), nil)
			require.NoError(t, err)

			g := GoGenerator{
				Config: generator.Config{
					OutputDir: dir,
					ModuleConfig: &generator.ModuleGeneratorConfig{
						ModuleName:       "test",
						ModuleSourcePath: "test",
						LibVersion:       "v1.2.3",
					},
				},
			}
			genSt := &generator.GeneratedState{}
			require.NoError(t, g.syncModReplaceAndTidy(mod, genSt, "test"))

			var edits []string
			for _, cmd := range genSt.PostCommands {
				if len(cmd.Args) > 3 && cmd.Args[1] == "work" && cmd.Args[2] == "edit" {
					edits = append(edits, cmd.Args[3:]...)
				}
			}
			require.Equal(t, tc.want, edits)
		})
	}
}

func TestNewModuleGoModNamesNoPatchRelease(t *testing.T) {
	t.Parallel()

	g := GoGenerator{
		Config: generator.Config{
			OutputDir: t.TempDir(),
			ModuleConfig: &generator.ModuleGeneratorConfig{
				ModuleName:       "test",
				ModuleSourcePath: ".",
			},
		},
	}
	mfs := memfs.New()
	_, _, err := g.bootstrapMod(mfs, &generator.GeneratedState{}, true)
	require.NoError(t, err)

	content, err := fs.ReadFile(mfs, "go.mod")
	require.NoError(t, err)
	mod, err := modfile.Parse("go.mod", content, nil)
	require.NoError(t, err)
	// Any patch release of the Go codegen is built with must build the
	// module, not only that patch or later ones.
	require.Equal(t, strings.TrimPrefix(version.Lang(runtime.Version()), "go")+".0", mod.Go.Version)
}

func TestGoModVersion(t *testing.T) {
	t.Parallel()

	for release, want := range map[string]string{
		"go1.27.4":  "1.27.0",
		"go1.27.0":  "1.27.0",
		"go1.28rc1": "1.28.0",
	} {
		require.Equal(t, want, goModVersion(release), release)
	}
}
