# Future User-Level Workspace Environments

author: shykes
created: 2026-05-29
status: design draft before implementation

## Context

Workspace environments are currently repo-owned overlays in
`.dagger/config.toml`. That works for shared presets such as CI, staging, and
prod.

The same mechanism is also useful for user-local choices:

- private account profiles
- local paths
- development clusters
- secret-provider refs
- per-user module settings

Those values should usually not be committed. They should live in the user's
Dagger config and apply only to the intended workspace.

## Goal

Add user-level workspace environments without adding a separate global config
API.

The intended result:

- `dagger --env NAME ...` uses repo config plus matching user config
- `dagger env` is aware of both workspace config and user config
- user config entries are scoped to stable workspace identities
- no-workspace commands still have a `Workspace` receiver
- implementation keeps the existing workspace loading path as much as possible

## Non-Goals

Do not add unscoped global module overlays yet. In particular, do not support
top-level user config like:

```toml
[env.dev.modules.aws.settings]
region = "us-west-2"
```

That is a broader profile/module-loading feature. Start with workspace-scoped
user envs only.

Do not expose a query-level `UserConfig` or `GlobalConfig` API. Global-aware
behavior should sit behind the existing `Workspace` surface.

## Config Shape

Use an env-first shape in `~/.config/dagger/config.toml`:

```toml
[env.dev.workspaces."github.com/acme/api".modules.aws.settings]
profile = "alice-dev"
region = "us-west-2"

[env.dev.workspaces."github.com/acme/web".modules.vercel.settings]
team = "acme-dev"
```

This makes `dev` the user-facing environment name while allowing the same env
to carry workspace-specific settings for multiple workspaces.

Only module settings are in scope for the first version:

```text
env.<name>.workspaces.<workspace-key>.modules.<module-name>.settings.*
```

User envs should not change the module graph, entrypoints, lock behavior, or
workspace ownership.

## Effective Config

When `--env NAME` is selected, load config in this order:

1. base `.dagger/config.toml`
2. repo-owned `[env.NAME]`, if present
3. user-owned `[env.NAME.workspaces.<workspace-key>]`, if present

Conflicts are resolved at the module setting key level. User config shadows
repo config.

The same merge policy must be used everywhere: module loading, config reads,
env lookup, and env management.

## Workspace Key

User env entries need a stable target. Local paths are not stable enough because
moving a checkout breaks the mapping.

Prefer a canonical remote-derived workspace key:

- explicit remote workspace: normalized remote workspace identity
- local checkout: normalized `origin` remote plus workspace/config-owner subdir
- local checkout without a remote: path-based fallback, marked as less stable

Do not include the selected version or ref in the default key. For example,
`github.com/acme/app` should continue to match when a user moves from `@v1` to
`@v2`. Version-specific keys can be added later if there is demand.

The generated scratch path used for no-workspace commands must never become a
workspace key.

## CLI Contract

Environment selection stays session-scoped:

```bash
dagger --env dev call build
```

Environment listing rolls up workspace config and user config:

```bash
dagger env list      # env names selectable for the current workspace
dagger env list -a   # all env names across workspace + user config
```

`dagger env list` answers "what can I select here?" `dagger env list -a`
answers "what env names exist anywhere I can see?"

A user config env namespace is selectable even before it has a workspace entry.
Selecting it applies the repo env overlay if present, then applies a user
workspace overlay only if one exists for the current workspace key.

Environment creation chooses a storage target:

```bash
dagger env create dev     # workspace config when writable; otherwise user config
dagger env create -g dev  # user config only
```

`dagger env create -g dev` creates only the global env namespace. It must not
create a workspace entry. Workspace-scoped user entries are created later when a
command writes actual scoped data, for example:

```bash
dagger config -g --env dev ...
dagger settings -g --env dev ...
```

Those commands use the current real workspace key when one exists.

## Workspace API

Keep the API centered on `Workspace`.

`currentWorkspace` should become total: it should always return a workspace. If
no real project workspace is detected, return a real empty local workspace
created on the caller's host for this session.

Relevant workspace functions can gain optional arguments:

```graphql
type Query {
  currentWorkspace: Workspace!
}

type Workspace {
  envList(all: Boolean = false): [String!]!
  envCreate(name: String!, global: Boolean = false): String!
  envRemove(name: String!, global: Boolean = false): String!
  envConfigKey: String!
}
```

The API should not expose explicit global config access. `global: true` is a
storage-target choice for relevant workspace operations.

## Synthetic Workspace

When no real workspace is detected, create a real empty local workspace root on
the caller's host, then run the normal workspace detection/loading path against
that directory.

Use a host/session attachable for the host-specific part:

```text
CreateScratchDir(kind: "workspace") -> absolute caller-local path
```

The client side owns the platform-correct base location and directory creation.
The engine asks for a scratch workspace dir and continues through the ordinary
workspace path.

The resulting workspace is normal except for one internal flag: local workspace
config is read-only / not user-owned. Local config writes fail unless the
operation targets user config with `global: true`.

Scratch workspace roots need lifecycle management. They should not accumulate
forever, and their paths must not participate in stable workspace identity.

## Things Not To Forget

Workspace key normalization is product behavior, not an implementation detail.
Forks, alternate remotes, URL spelling, subdirectories, and remote refs need
explicit tests.

Deletion needs source-aware semantics. Removing an env from workspace config
must not silently remove a user config env with the same name, and the reverse
is also true.

Listing names is intentionally simple, but implementation should preserve
enough source information internally for future `show`, diagnostics, or debug
output.

Global config writes need locking and formatting preservation. Avoid races
between concurrent CLI invocations, and avoid rewriting unrelated user config
more than necessary.

No-workspace behavior should be boring after detection. The synthetic workspace
exists to keep codepaths shared, not to create a new workspace mode that leaks
into module loading, cache keys, or public identity.

Remote workspaces are readable targets. User config writes are local. Workspace
config writes should only happen when there is a real writable local workspace.

Global config may contain private paths, profile names, and secret refs. Do not
send raw user config through metadata, traces, or diagnostics unless explicitly
requested.

## Implementation Notes

Recommended order:

1. Add user config parsing and pure merge helpers.
2. Add workspace key computation and tests.
3. Make env listing/lookup use the same merge policy.
4. Add global-aware env create/remove behavior.
5. Add total workspace detection with scratch workspace creation.
6. Wire CLI flags to the workspace API.
7. Add integration tests for local, remote, no-workspace, and conflict cases.

Keep the first implementation narrow. The important invariant is that a user
env can change only module settings for a specific workspace.
