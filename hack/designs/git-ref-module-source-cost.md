# Cost of a remote git module source

Line numbers refer to `f4082d763f`, before the changes in section 6.

## 1. Summary

When a module names a git repository as its runtime, every CLI command
contacted that repository again. Three separate causes, none of them related:

| # | Cause | Cost for each command |
|---|---|---|
| 1 | The engine waits for a client that cannot answer. | 10.000 s, only for `dagger generate` |
| 2 | The engine runs `git ls-remote` to select a transport. | 0.7 s to 3.4 s |
| 3 | The engine asks the git host whether the repository is public. | 0.2 s to 9.8 s, once for each client |

Section 6 removes cause 1 and cause 2. It reduces cause 3 to one request for
each command.

The symptom, measured on the `python:generate-all` span of `dagger generate`:

| Runtime source | Duration |
|---|---|
| `"python"`, built into the engine | 0.2 s to 0.3 s |
| A local path to the same runtime | 0.7 s to 0.8 s |
| `github.com/dagger/python-sdk/runtime` | 10.5 s to 11.0 s |

Two facts made this hard to explain. A pinned commit SHA did not help. And the
visible git operations cost about 1.6 s, against 10.4 s for the whole path.

## 2. The workspace lock is not the cause

The lock already prevents live git resolution. When it holds an entry, `ref`
(`core/schema/git.go:1043`) and `latest` (`core/schema/git.go:2065`) return the
pinned value without touching the network. Against a full lock the
`GitRepository.latest` span costs 0.00 s.

`726ddb673f` (#13897), `e50018a6e2` (#13754) and `f6f662ca6d` (#13088) made
that the default. All three are already in `v1.0.0-beta.11`, the version that
showed the numbers above. No code at the three cause sites changed between that
tag and `main`.

All three causes below happen **before** the engine reads the lock.

## 3. Cause 1 — a 10 s wait for a client that cannot answer

The largest cost is not a git operation.

`ParseRefString` (`core/modulerefs.go:52`) must decide whether a ref string
names a git repository or a local directory. `FastKindCheck`
(`core/gitref/gitref.go:80`) decides from the text alone for `./runtime` and
for `https://github.com/org/repo`. It cannot decide for `github.com/org/repo`,
which is the form users write.

For that ambiguous form, `ParseRefString` asks the calling client's file system
whether such a directory exists (`core/modulerefs.go:82` →
`core/modulesource.go:2179` → `engine/engineutil/filesync.go:119`).

During `dagger generate` the code generator runs in a synthetic nested client,
created for in-engine dang evaluation (`core/sdk/dang/v2/sdk.go:124`). That
client never registers session attachables, because it has no file system to
offer. `getClientCaller` (`engine/server/session.go:836`) waited for them
anyway, up to 10 seconds. The wait could never succeed, so it always expired:

```text
parseRefString stat error error="failed to stat path: failed to get requester session: context deadline exceeded"
```

`ParseRefString` treats the request as best effort. It logs the failure and
reads the string as a git ref. The answer was always correct, 10 seconds late.

Two measurements confirm the cause. The span lasted 10.001478 s and
10.000348 s in two runs; a network delay does not repeat to four decimal
places. And the same ref string with an `https://` prefix cost 0.000045 s,
because the prefix lets `FastKindCheck` answer from the text.

This is also why only `generate` paid. `dagger call` resolves the module source
in the CLI client, which does have a file system. There the request cost
0.000253 s.

`resolveHostServiceCaller` (`engine/server/session.go:1055`) already knows this
class of client and routes host-backed services to the parent instead. The file
system path did not.

## 4. Cause 2 — an `ls-remote` to select a transport

A scheme-less ref string does not say whether to use HTTPS or SSH. The `git`
resolver finds this at `core/schema/git.go:369` and builds one candidate for
each entry in `cloneURLFallbackProtocols` (`util/gitutil/url.go:139`). To find
which candidate answers, it calls `LoadRemote` on each one
(`core/schema/git.go:475`), which runs `ls-remote`.

That loop never reads the workspace lock. It runs on every command, for every
scheme-less ref string. This is why a pinned commit SHA did not help: the
resolver selects the transport before it resolves any ref.

Measured `ls-remote` span: 0.72 s, 2.96 s and 3.39 s in different runs.

## 5. Cause 3 — a visibility request for each client

Before attaching the user's git credentials, the engine asks whether the
repository is public (`core/schema/git.go:764`). That is an unauthenticated
HTTP request for the ref advertisement. It had no cache and no span, so it was
invisible in traces.

The engine caches the `git` field per client. One command can reach the same
repository from the CLI client and again from a module's dependency resolution,
and each client paid a separate request.

One run used an `https://` ref string and a full lock, so it made no
`ls-remote` call at all. `Query.git` still cost 1.32 s, 1.55 s and 9.75 s over
three such runs. That is this request.

## 6. The changes

### 6.1 Fail fast for a client that serves no attachables

`engine/server/session.go`. `getClientCaller` now returns an error at once for
a synthetic nested client that registered no attachables.

Nothing that used to succeed changes. Such a client has no file system, so
these requests already failed — 10 seconds later.

The change deliberately does **not** route the request to the parent client the
way `resolveHostServiceCaller` does for host-backed services. That would give
module code implicit read access to the user's files, which is a wider grant
than a performance problem justifies.

### 6.2 Take the transport from the lock

`core/schema/git.go`. Before probing, the resolver checks whether the lock
already holds `git-*` entries for one of the candidates. If it does, an earlier
session already reached the repository over that transport, so the resolver
keeps only that candidate and skips `LoadRemote`. Remote metadata stays lazy
(`core/git.go:225`), so whoever needs it still loads it.

The check reuses `gitRemoteHasWorkspacePin` (`core/schema/git.go:1019`), which
the failure branch of the visibility request already consults for the same
reason.

This adds no new data to `dagger.lock` and no new risk of a stale answer. The
inputs of `git-sha` and `git-latest` entries already carry the full URL with
its scheme. A user on a different transport therefore finds no entry, and
probes as before.

### 6.3 Cache the visibility answer for each session

`core/schema/git.go`. The answer now comes from the engine cache, keyed by
session and repository URL, and the request gets a `git remote visibility`
span. It sends no credentials, so the URL alone identifies the answer. A
repository behind a service binding is still asked directly, because its
visibility depends on the service.

The first request of each command remains. The engine cache holds the answer in
memory only and releases it with the last owning session, so nothing carries
over. Removing that request means recording repository visibility in
`dagger.lock`, which changes what a shared lock file states about a repository.
That needs its own decision.

## 7. Measurements

Engine built from this branch against the released `v1.0.0-beta.11`. Same
workspaces, same host, warm caches. Module runtime is
`github.com/dagger/python-sdk/runtime`.

| Command | Measurement | Before | After |
|---|---|---|---|
| `generate` | `generators` span | 12.75 s, 12.80 s | 2.01 s, 2.26 s, 3.30 s |
| `generate` | `parseRefString` for the runtime ref | 10.001478 s, 10.000348 s | 0.00064 s, 0.00071 s |
| `generate` | `git ls-remote` spans | 2 | 0 |
| `generate` | `git remote visibility` | untraced, once per client | 0.37 s, then 0.00 s from cache |
| `call` | `load workspace` span | 2.46 s, 2.77 s | 1.56 s, 1.80 s |
| `call` | `git ls-remote` spans | 1 | 0 |

The workspace SDK can be the remote git ref instead of the module runtime. In
that case the `generate` `generators` span drops from 4.81 s to 2.81 s, and its
3.39 s `ls-remote` disappears.

A fully pinned scheme-less ref string that points at a host which does not
exist now resolves with no network at all:

```graphql
{ git(url: "git.example.invalid/dagger.git") { latest { ref commit } } }
```

| Version | Result |
|---|---|
| `v1.0.0-beta.11` | `git error: exit status 128`, after an `ls-remote` |
| This branch | `{"ref":"refs/tags/v1.2.3","commit":"0123…4567"}` in 0.00 s |

## 8. Tests and reproduction

Two tests in `core/integration/lockfile_test.go` use that unreachable host and
differ only in the lock:

- `TestGitLatestPinnedSchemelessRemoteSkipsTransportProbe` — the lock holds
  entries for the HTTPS candidate, so the command must succeed with no network.
  Fails on `v1.0.0-beta.11`, passes here.
- `TestUnpinnedSchemelessRemoteStillProbesTransport` — the lock is empty, so
  the command must still probe and still fail. Passes on both.

`TestNeverServesAttachables` in `engine/server/session_test.go` covers the four
cases of the fail-fast condition.

To reproduce the numbers, point a module's `[runtime] source` at
`github.com/dagger/python-sdk/runtime`, capture with `hack/otlpdump`, and run
the command twice. Read the `parseRefString`, `git ls-remote` and
`git remote visibility` spans from the second run.

Two failures on a development host are **not** regressions; each repeats on an
unmodified `v1.0.0-beta.11` engine.
`TestLockfile/TestUpdateRefreshesExistingGitEntry` compares exact output and
the host adds a telemetry warning.
`TestGit/TestGitSchemeless/private_no_auth_fails` needs a host whose git
credential helper holds no GitHub credentials.
