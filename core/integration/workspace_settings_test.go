package core

// Workspace alignment: this is a user-facing design spec for `dagger settings`; implementation and storage migration are still pending.
// Scope: Command grammar, discovery, read/write semantics, env scoping, and the relationship between `dagger settings` and `dagger config`.
// Intent: Make module settings a first-class UX distinct from workspace config while keeping one underlying source of truth.
//
// Storage examples in this file intentionally use `[modules.<alias>.settings]`
// and `[env.<name>.modules.<alias>.settings]`. That terminology is part of the
// design being specified here. Any compatibility with legacy
// `[modules.<alias>.config]` storage belongs in migration or compat coverage,
// not in this command spec.

import (
	"context"

	"github.com/dagger/testctx"
)

// TestWorkspaceSettingsCommandGrammar fixes the command shape before any
// implementation details. The command is intentionally positional and requires
// an explicit module alias at every depth so it stays unambiguous.
func (WorkspaceSuite) TestWorkspaceSettingsCommandGrammar(ctx context.Context, t *testctx.T) {
	t.Run("settings supports exactly zero, one, two, or three positional args", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement command-grammar coverage for dagger settings.

The supported forms are:

  dagger settings
  dagger settings MODULE
  dagger settings MODULE KEY
  dagger settings MODULE KEY VALUE

Each form has distinct behavior:

  dagger settings
    shows the discoverable settings surface for all installed modules

  dagger settings MODULE
    shows the discoverable settings surface for one installed module

  dagger settings MODULE KEY
    prints the current effective value for one setting

  dagger settings MODULE KEY VALUE
    writes one setting in the current scope

Any invocation with four or more positional args after "settings" should fail
with usage guidance rather than guessing how to interpret extra words.`)
	})

	t.Run("module omission is never supported, even for a single entrypoint module", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement no-module-omission coverage for dagger settings.

Given a workspace with exactly one installed module:

[modules.greeter]
source = "modules/greeter"
entrypoint = true

[modules.greeter.settings]
greeting = "hello"

Running:

  dagger settings greeting

must not be interpreted as "read the greeting setting from the entrypoint
module". It should fail as an unknown module selection, because the grammar
always requires:

  dagger settings MODULE KEY

This keeps the command unambiguous and avoids special entrypoint-only rules.`)
	})

	t.Run("module selection always uses the workspace alias, not the module's intrinsic name", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement alias-selection coverage for dagger settings.

Given:

[modules.prod-aws]
source = "github.com/dagger/aws"

[modules.prod-aws.settings]
region = "us-west-2"

Running:

  dagger settings prod-aws region

should succeed.

Running:

  dagger settings aws region

should fail clearly, even if the module loaded from github.com/dagger/aws has an
intrinsic module name of "aws". The command must target the workspace-installed
module alias, because that is the stable identity in config storage and env
overlays.`)
	})
}

// TestWorkspaceSettingsDiscovery defines the high-level, typed, discoverable UX
// that `dagger settings` should provide on top of constructor introspection.
func (WorkspaceSuite) TestWorkspaceSettingsDiscovery(ctx context.Context, t *testctx.T) {
	t.Run("settings with no module arg lists installed modules in deterministic order", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement all-modules discovery coverage for dagger settings.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.vitest]
source = "github.com/dagger/vitest"

Running:

  dagger settings

should list the installed modules in lexicographic alias order:

  aws
  vitest

and, for each module, should expose that it has a discoverable settings surface.
The exact formatting can evolve, but the output must be easy to scan and
deterministic so users and tests can rely on it.`)
	})

	t.Run("settings MODULE shows typed setting discovery with current effective values", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement per-module discovery coverage for dagger settings.

Given a module whose constructor accepts settings like:

  region string
  secretKey string
  failFast bool

with descriptions on those constructor args, and given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"
secretKey = "op://vault/aws"

Running:

  dagger settings aws

should show the discoverable settings surface for that module. At minimum, for
each setting it should include:

  the setting name
  the type
  the current effective value, if one is set
  the description/help text derived from constructor introspection

It should not expose workspace-owned fields like source or entrypoint in this
view, because those are workspace config, not module settings.`)
	})

	t.Run("settings MODULE in env scope shows effective values after overlay", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped discovery coverage for dagger settings.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"
format = "json"

[env.ci.modules.aws.settings]
region = "us-east-1"

Running:

  dagger --env=ci settings aws

should show:

  region = us-east-1
  format = json

because discovery in env scope should reflect the effective active settings for
that env, not just the raw override subtree. Values not overridden in the env
must still appear via base fallback.`)
	})

	t.Run("unknown module fails clearly instead of printing an empty settings surface", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement unknown-module discovery coverage for dagger settings.

Given a workspace with no module alias named "missing", running:

  dagger settings missing

should fail clearly. It must not print an empty block or silently succeed, or
typos become indistinguishable from modules that simply have no settings.`)
	})
}

// TestWorkspaceSettingsReadSemantics covers scalar reads for one setting at a
// time. These reads should behave like other scope-aware commands: the selected
// env changes what value is considered active.
func (WorkspaceSuite) TestWorkspaceSettingsReadSemantics(ctx context.Context, t *testctx.T) {
	t.Run("settings MODULE KEY reads the base-scope effective value", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement base-scope settings read coverage.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"

Running:

  dagger settings aws region

should print:

  us-west-2

with no surrounding prose.`)
	})

	t.Run("settings MODULE KEY with env reads the effective env value with base fallback", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped settings read coverage.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"
format = "json"

[env.ci.modules.aws.settings]
region = "us-east-1"

Running:

  dagger --env=ci settings aws region

should print:

  us-east-1

because that key is overridden in ci.

Running:

  dagger --env=ci settings aws format

should print:

  json

because env reads are effective reads, not raw-override-only reads.`)
	})

	t.Run("missing env fails clearly instead of silently falling back to base", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement missing-env settings read coverage.

Given a workspace with no env.ci, running:

  dagger --env=ci settings aws region

should fail clearly. It must not silently behave like base-scope settings, or a
typo in the env name becomes invisible.`)
	})

	t.Run("unknown setting fails clearly and does not expose non-setting metadata", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement unknown-setting read coverage for dagger settings.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"

Running:

  dagger settings aws missing

should fail clearly as an unknown setting.

Running:

  dagger settings aws source

should also fail, because source is workspace config metadata, not a module
setting exposed by constructor introspection.`)
	})
}

// TestWorkspaceSettingsWriteSemantics defines where writes land. Reads are
// effective in the selected scope; writes mutate that scope's stored settings.
func (WorkspaceSuite) TestWorkspaceSettingsWriteSemantics(ctx context.Context, t *testctx.T) {
	t.Run("base-scope writes update modules.<alias>.settings and affect later effective reads", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement base-scope settings write coverage.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"

Running:

  dagger settings aws region eu-central-1

should update the underlying workspace config to:

[modules.aws.settings]
region = "eu-central-1"

Subsequent reads through either:

  dagger settings aws region

or:

  dagger config modules.aws.settings.region

should return:

  eu-central-1`)
	})

	t.Run("env-scoped writes update env.<name>.modules.<alias>.settings and leave base unchanged", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped settings write coverage.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"

[env.ci]

Running:

  dagger --env=ci settings aws region us-east-1

should write:

[env.ci.modules.aws.settings]
region = "us-east-1"

and must leave the base value under [modules.aws.settings] unchanged.

After that:

  dagger settings aws region

should still print:

  us-west-2

while:

  dagger --env=ci settings aws region

should print:

  us-east-1`)
	})

	t.Run("typed writes use the same coercion rules as config writes", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement typed settings write coverage.

Given constructor-backed settings like:

  failFast bool
  retries int
  tags []string

Running:

  dagger settings vitest failFast true
  dagger settings vitest retries 3
  dagger settings vitest tags smoke, regression

should persist typed values under [modules.vitest.settings] using the same
parsing/coercion rules that dagger config uses for scalar and list writes.`)
	})

	t.Run("writes reject unknown modules, unknown settings, and workspace-owned fields", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement write-validation coverage for dagger settings.

These should all fail clearly:

  dagger settings missing region us-west-2
  dagger settings aws missing value
  dagger settings aws source github.com/acme/aws
  dagger settings aws entrypoint true

The command must only allow writes to constructor-backed module settings. It
must not become a second interface for mutating arbitrary workspace config
metadata.`)
	})
}

// TestWorkspaceSettingsConfigProjection locks in that `dagger settings` is an
// ergonomic, typed projection over workspace config rather than a second
// storage system with independent semantics.
func (WorkspaceSuite) TestWorkspaceSettingsConfigProjection(ctx context.Context, t *testctx.T) {
	t.Run("settings reads agree with config reads for the same effective value", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement config-projection read equivalence coverage.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"

[env.ci.modules.aws.settings]
region = "us-east-1"

These two commands should agree in base scope:

  dagger settings aws region
  dagger config modules.aws.settings.region

and these two commands should agree in ci scope:

  dagger --env=ci settings aws region
  dagger --env=ci config modules.aws.settings.region

The point of dagger settings is better discovery and ergonomics, not a separate
value model.`)
	})

	t.Run("writes through settings are visible immediately through config and runtime behavior", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement config-projection write/runtime coverage.

After:

  dagger settings aws region eu-central-1

the new value should be visible through:

  dagger config modules.aws.settings.region

and should also affect any runtime surfaces that already derive defaults from
module settings, such as constructor default values in help output or calls that
use those settings implicitly.

This locks in that dagger settings is the preferred typed UX for module
settings, while dagger config remains the universal workspace-config escape
hatch.`)
	})
}
