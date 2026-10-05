package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (WorkspaceSuite) TestDoctorCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithEnvVariable("DAGGER_NO_UPDATE_CHECK", "1").
		WithEnvVariable("DAGGER_CLOUD_TOKEN", "").
		WithWorkdir("/work").WithDirectory("/work", c.Directory().WithNewDirectory(".git"))
	for _, tc := range []struct {
		name    string
		config  string
		lock    string
		command []string
		expect  dagger.ReturnType
		want    []string
	}{
		{"missing files", "", "", []string{"workspace", "doctor"}, dagger.ReturnTypeSuccess, []string{"WARN Workspace config", "WARN Lockfile", "PASS Engine", "WARN Cloud login"}},
		{"unloadable module fails", "[modules.broken]\nsource = \"does-not-exist\"\n", "[[\"version\",\"2\"]]\n", []string{"workspace", "doctor"}, dagger.ReturnTypeFailure, []string{"PASS Workspace config", "PASS Lockfile", "PASS Engine", `FAIL Module loading "broken"`}},
		{"hidden alias", "", "", []string{"doctor"}, dagger.ReturnTypeSuccess, []string{"WARN Workspace config", "PASS Engine"}},
		{"invalid files", "[broken", "broken", []string{"workspace", "doctor"}, dagger.ReturnTypeFailure, []string{"FAIL Workspace config", "FAIL Lockfile", "PASS Engine", "WARN Cloud login"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			ctr := base
			if tc.config != "" {
				ctr = ctr.WithNewFile("/work/dagger.toml", tc.config)
			}
			if tc.lock != "" {
				ctr = ctr.WithNewFile("/work/dagger.lock", tc.lock)
			}
			out, err := ctr.WithExec(append([]string{"dagger", "--progress=plain"}, tc.command...), dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true, Expect: tc.expect,
			}).Stderr(ctx)
			require.NoError(t, err)
			out = ansi.Strip(out)
			for _, want := range tc.want {
				require.Contains(t, out, doctorLine(want))
			}
		})
	}
	t.Run("malformed credentials remain a warning", func(ctx context.Context, t *testctx.T) {
		for _, command := range [][]string{{"workspace", "doctor"}, {"doctor"}} {
			out, err := base.WithEnvVariable("XDG_CONFIG_HOME", "/doctor-config").
				WithNewFile("/doctor-config/dagger/credentials.json", "broken").
				With(workspaceSelectionDaggerExec(append([]string{"--progress=plain"}, command...)...)).Stderr(ctx)
			require.NoError(t, err)
			out = ansi.Strip(out)
			require.Contains(t, out, doctorLine("WARN Cloud login"))
			require.Contains(t, out, doctorLine("PASS Engine"))
		}
	})
	t.Run("remote workspace", func(ctx context.Context, t *testctx.T) {
		source := c.Directory().WithNewFile("dagger.toml", "")
		ref := workspaceSelectionRemoteRef(ctx, t, c, source)
		out, err := base.With(workspaceSelectionDaggerExec("--progress=plain", "-W", ref, "workspace", "doctor")).Stderr(ctx)
		require.NoError(t, err)
		out = ansi.Strip(out)
		require.Contains(t, out, doctorLine("PASS Workspace config"))
		require.Contains(t, out, doctorLine("WARN Lockfile"))
		require.Contains(t, out, doctorLine("PASS Engine"))
	})
}

func (WorkspaceSuite) TestDoctorModuleSettings(ctx context.Context, t *testctx.T) {
	fixture := workspaceSettingsModuleFixture{relDir: "probe", name: "probe", main: `package main
import "dagger/probe/internal/dagger"
type Probe struct{}
type Flavor string
const Vanilla Flavor = "VANILLA"
func New(
 // +optional
 flavor Flavor,
 // +optional
 count int,
 // +optional
 enabled bool,
 // +optional
 tags []string,
 // +optional
 dir *dagger.Directory,
) *Probe { panic("doctor must not invoke the constructor") }
func (p *Probe) Hello() string { return "hello" }
`}
	provider := workspaceSettingsModuleFixture{relDir: "provider", name: "provider", main: `package main
import "dagger/provider/internal/dagger"
type Provider struct{}
func (p *Provider) Directory() *dagger.Directory { return dag.Directory() }
`}
	for _, tc := range []struct {
		name, settings, extra string
		args                  []string
		fail                  bool
		want                  []string
	}{
		{name: "valid settings", settings: "COUNT = 3\nenabled = true\ntags = [\"a\", \"b\"]\ndir = \".\"\n", want: []string{`PASS Module loading "probe"`, `PASS Module settings "probe"`}},
		{name: "module wiring", settings: "dir = \"provider:directory\"\n", extra: "[modules.provider]\nsource = \"provider\"\n", want: []string{`PASS Module settings "probe"`}},
		{name: "invalid module wiring", settings: "dir = \"provider:missing\"\n", extra: "[modules.provider]\nsource = \"provider\"\n", fail: true, want: []string{`FAIL Module settings "probe"`}},
		{name: "enum setting", settings: "flavor = \"VANILLA\"\n", want: []string{`PASS Module settings "probe"`}},
		{name: "invalid enum setting", settings: "flavor = \"INVALID\"\n", fail: true, want: []string{"ERROR", "flavor"}},
		{name: "unknown settings", settings: "typo = true\n", fail: true, want: []string{`FAIL Module settings "probe"`, `unknown setting "typo"`}},
		{name: "invalid primitive", settings: "count = \"not-an-int\"\n", fail: true, want: []string{"ERROR", "count"}},
		{name: "invalid bool", settings: "enabled = 42\n", fail: true, want: []string{"ERROR", "enabled"}},
		{name: "invalid address", settings: "dir = \"/does-not-exist\"\n", fail: true, want: []string{`FAIL Module settings "probe"`, "dir"}},
		{name: "selected environment", settings: "count = 3\n", extra: "[env.bad.modules.probe.settings]\ntyppo = true\n", args: []string{"--env=bad"}, fail: true, want: []string{`FAIL Module settings "probe"`, "typpo"}},
		{name: "continue after load failure", extra: "[modules.broken]\nsource = \"does-not-exist\"\n", fail: true, want: []string{`FAIL Module loading "broken"`, `PASS Module settings "probe"`}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			dir := newWorkspaceSettingsWorkdir(ctx, t, "[modules.probe]\nsource = \"probe\"\n[modules.probe.settings]\n"+tc.settings+tc.extra, fixture, provider)
			args := append([]string{"--progress=plain", "workspace", "doctor"}, tc.args...)
			out, err := hostDaggerExec(ctx, t, dir, args...)
			if tc.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			for _, want := range tc.want {
				require.Contains(t, ansi.Strip(string(out)), doctorLine(want))
			}
		})
	}
}

// doctorLine maps "STATUS name" to how plain progress shows that diagnostic:
// a passing span is DONE, a failing span is ERROR, and a warning names its
// reason after the diagnostic. Other strings are matched as given.
func doctorLine(want string) string {
	status, name, _ := strings.Cut(want, " ")
	switch status {
	case "PASS":
		return name + " DONE"
	case "FAIL":
		return name + " ERROR"
	case "WARN":
		return name + ": "
	}
	return want
}
