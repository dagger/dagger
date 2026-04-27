package core

// Workspace alignment: this file is the user-facing design spec for env
// management and env-scoped config semantics.
// Scope: User-facing workspace environment lifecycle plus `dagger config` read/write behavior when `--env` is selected.
// Intent: Keep config storage, effective reads, runtime behavior, and CLI management aligned on one env contract.
//
// This file covers generic config behavior in env scope. Typed module-setting
// discovery belongs to `dagger settings`; here, module-specific examples use
// the underlying `[modules.<alias>.settings]` storage model.

import (
	"context"

	"github.com/dagger/testctx"
)

// TestWorkspaceEnvLifecycleCommands owns the explicit lifecycle commands for
// named workspace environments. It should not cover runtime application of an
// env; that belongs with config semantics and runtime consistency below.
func (WorkspaceSuite) TestWorkspaceEnvLifecycleCommands(ctx context.Context, t *testctx.T) {
	t.Run("env list prints names in deterministic order", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace env list coverage.

Given a workspace config containing:

[env.dev]

[env.ci]

[env.prod]

Running:

  dagger env list

should print:

  ci
  dev
  prod

one environment name per line, sorted lexicographically, with no extra prose.
If no envs are defined, the command should print nothing and succeed.`)
	})

	t.Run("env create initializes an empty env and is idempotent", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace env create coverage.

Starting from a workspace with no [env.*] sections, running:

  dagger env create ci

should create:

[env.ci]

with no module overlay keys yet.

Running the same command again should succeed without changing any existing
env.ci contents. This command is "ensure exists", not "fail if already exists".`)
	})

	t.Run("env rm deletes only the selected env and fails for missing env", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement workspace env rm coverage.

Given:

[env.dev.modules.aws.settings]
region = "us-west-2"

[env.ci.modules.aws.settings]
region = "us-east-1"

Running:

  dagger env rm ci

should remove only the env.ci subtree and leave env.dev untouched.

Running:

  dagger env rm missing

should fail clearly rather than silently succeeding, so typos do not look like
successful deletes.`)
	})
}

// TestWorkspaceEnvConfigReadSemantics defines what users should see from
// `dagger config` when they select an environment. The command is a config UX,
// not a raw TOML browser, so env-scoped reads should default to effective
// merged values.
func (WorkspaceSuite) TestWorkspaceEnvConfigReadSemantics(ctx context.Context, t *testctx.T) {
	t.Run("whole-file read with env shows the effective active config", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped config full-read coverage.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
format = "json"
region = "us-west-2"

[modules.vitest]
source = "github.com/dagger/vitest"

[modules.vitest.settings]
reporter = "dot"

[env.ci.modules.aws.settings]
region = "us-east-1"

Running:

  dagger --env=ci config

should print the effective active config for ci, shaped like the base config:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
format = "json"
region = "us-east-1"

[modules.vitest]
source = "github.com/dagger/vitest"

[modules.vitest.settings]
reporter = "dot"

It must not print [env.ci] tables in this mode, because the user asked for the
config as ci sees it, not for the raw storage subtree.`)
	})

	t.Run("scalar reads in env scope return effective values with base fallback", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped config scalar-read coverage.

Using the fixture above:

  dagger --env=ci config modules.aws.settings.region

should print:

  us-east-1

because ci overrides that key.

And:

  dagger --env=ci config modules.aws.settings.format

should print:

  json

because the key is not overridden in ci and must fall back to the base config.`)
	})

	t.Run("table reads in env scope merge base entry fields with env settings overrides", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped config table-read coverage.

Using the same fixture:

  dagger --env=ci config modules.aws

should print a merged table view that includes:

  source = "github.com/dagger/aws"
  settings.format = "json"
  settings.region = "us-east-1"

And:

  dagger --env=ci config modules.aws.settings

should print:

  format = "json"
  region = "us-east-1"

This locks in that env reads merge module.settings keys only; module source and
other non-overridable fields still come from the base config.`)
	})

	t.Run("missing env fails clearly instead of silently falling back to base", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement missing-env config read coverage.

Given a workspace with no env.ci, running:

  dagger --env=ci config

or:

  dagger --env=ci config modules.aws.settings.region

should fail clearly. It must not silently behave like base-scope config, or a
typo in the env name becomes invisible.`)
	})
}

// TestWorkspaceEnvConfigWriteSemantics defines where writes land when an env is
// selected. Reads are effective in the selected scope; writes mutate that same
// scope's underlying storage.
func (WorkspaceSuite) TestWorkspaceEnvConfigWriteSemantics(ctx context.Context, t *testctx.T) {
	t.Run("write with env stores the override under env scope and leaves base unchanged", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped config write coverage.

Given:

[modules.aws]
source = "github.com/dagger/aws"

[modules.aws.settings]
region = "us-west-2"

[env.ci]

Running:

  dagger --env=ci config modules.aws.settings.region us-east-1

should write:

[env.ci.modules.aws.settings]
region = "us-east-1"

and must leave the base value under modules.aws.settings.region unchanged.

After the write:

  dagger config modules.aws.settings.region

should still print:

  us-west-2

while:

  dagger --env=ci config modules.aws.settings.region

should print:

  us-east-1`)
	})

	t.Run("env-scoped writes use the same scalar typing rules as base writes", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped typed write coverage.

Running:

  dagger --env=ci config modules.vitest.settings.failFast true
  dagger --env=ci config modules.vitest.settings.retries 3
  dagger --env=ci config modules.vitest.settings.tags smoke, nightly

should write bool, integer, and array values under env.ci with the same typing
rules used by base-scope dagger config writes.`)
	})

	t.Run("env-scoped writes reject keys outside the allowed overlay surface", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped write validation coverage.

The following should fail clearly in env scope:

  dagger --env=ci config modules.aws.source github.com/acme/aws
  dagger --env=ci config modules.aws.entrypoint true
  dagger --env=ci config defaults_from_dotenv true

because v1 env overlays may only write:

  modules.<alias>.settings.*

The error should explain that the key is not writable in env scope, not just
that some TOML field is unknown.`)
	})

	t.Run("env-scoped writes reject missing envs and unknown module aliases", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped missing-target write coverage.

Running:

  dagger --env=missing config modules.aws.settings.region us-east-1

should fail because envs are created explicitly through dagger env create;
env-scoped config writes must not auto-create a new env for a mistyped name.

And:

  dagger --env=ci config modules.missing.settings.region us-east-1

should fail because env overlays may only target already-installed workspace
module aliases.`)
	})
}

// TestWorkspaceEnvRawAccessEscapeHatches locks in the low-level escape hatch
// for users who need to inspect or edit the raw env subtree rather than the
// effective active config.
func (WorkspaceSuite) TestWorkspaceEnvRawAccessEscapeHatches(ctx context.Context, t *testctx.T) {
	t.Run("explicit env-prefixed keys address raw stored overlays", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement raw env subtree read coverage.

Given:

[modules.aws.settings]
region = "us-west-2"

[env.ci.modules.aws.settings]
region = "us-east-1"

Running:

  dagger config env.ci.modules.aws.settings.region

should print:

  us-east-1

This is the raw stored override, not the effective merged value logic used by:

  dagger --env=ci config modules.aws.settings.region`)
	})

	t.Run("explicit env-prefixed writes edit raw stored overlays directly", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement raw env subtree write coverage.

Running:

  dagger config env.ci.modules.aws.settings.region us-east-1

should write the raw env subtree directly, without requiring --env=ci.

This keeps a stable low-level escape hatch for debugging and scripting even
though ordinary env-scoped reads and writes are effective/scope-aware.`)
	})

	t.Run("explicit env-prefixed keys remain raw even when a current env is selected", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement raw-key precedence coverage.

If a user runs:

  dagger --env=prod config env.ci.modules.aws.settings.region

the explicit env.ci key path should still address the raw env.ci storage, not
the current effective prod scope. Explicit raw paths should win over implicit
scope selection so the escape hatch stays predictable.`)
	})
}

// TestWorkspaceEnvConfigRuntimeConsistency keeps the user-facing promise that
// `dagger config` reflects what runtime commands will actually use under the
// same env selection.
func (WorkspaceSuite) TestWorkspaceEnvConfigRuntimeConsistency(ctx context.Context, t *testctx.T) {
	t.Run("effective config reads match the defaults used by runtime commands", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env config/runtime equivalence coverage.

Use a workspace module whose constructor defaults are visible through both:

  dagger --env=ci config modules.<alias>.settings.<key>

and:

  dagger --env=ci call --help
  dagger --env=ci call ...

Verify the effective config value shown by dagger config is the same value used
to populate constructor defaults and runtime behavior under that env.`)
	})

	t.Run("env-scoped writes affect only that envs runtime behavior", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement env-scoped runtime isolation coverage.

Given one workspace and two envs:

  dagger --env=ci config modules.aws.settings.region us-east-1
  dagger --env=dev config modules.aws.settings.region us-west-2

Verify:

  dagger --env=ci call ...

uses us-east-1,

  dagger --env=dev call ...

uses us-west-2,

and:

  dagger call ...

still uses the base config. This locks in that env-scoped config writes are
runtime-visible only within the selected env.`)
	})
}
