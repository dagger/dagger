# Session Resources: Secrets And Sockets

This document describes the current session-resource model used for secrets and
sockets, and how it integrates with the dagql cache.

The source of truth is the code, mainly:

- `dagql/cache.go`
- `core/secret.go`
- `core/socket.go`
- `core/ssh_auth_socket.go`
- `core/client_resource.go`
- `core/schema/secret.go`
- `core/schema/host.go`
- `core/schema/git.go`
- `core/git_remote.go`
- `core/container_exec.go`

This doc focuses on:

- why session resources exist
- how the handle model works
- how cache hits become conditional on resource availability
- how equivalence works for secrets and sockets
- what concrete resource categories exist today

## The Core Problem

Some values are not ordinary content-addressed data.

Examples:

- a secret from an external provider
- a secret created from plaintext in a session
- an SSH agent socket
- a host unix socket path

These values have two conflicting requirements:

1. we want cache reuse when the resource is semantically equivalent
2. we do **not** want a cache hit to silently hand one client's resource-backed
   result to another client that never loaded that resource

The current session-resource system is the compromise that gives us both.

## The Core Rule

This is the key rule to remember:

If a cache hit has a direct or transitive dependency on a session resource, the
hit is only valid if the caller's session has already loaded that session
resource handle.

In practice that means:

- the cache may find an equivalent cached result
- but before returning it, it checks whether the session has the required
  resource handles
- if not, the hit is rejected

So session resources make some cache hits **conditional** rather than
unconditional.

## Why This Exists

Without this rule, cache reuse would be wrong in both directions:

- too strict: equivalent secrets/sockets could never reuse cache
- too loose: one session could get a hit that really depended on another
  session's resource

The handle model lets us say:

- "these two resources are equivalent enough for caching"
- while also saying
- "you still only get the hit if your session has loaded that resource handle"

That is the whole point of the system.

## High-Level Model

Each session resource has two forms:

### 1. Concrete session-local backing value

This is the real thing:

- actual secret plaintext or provider URI
- actual socket URL / SSH agent source

This concrete value is stored in the cache's per-session resource tables via
`BindSessionResource`.

### 2. Handle-form dagql object

This is what flows through the dagql graph and cache:

- a `Secret` or `Socket` object carrying a `SessionResourceHandle`
- plus a content digest set to that handle
- plus `requiredSessionResources` including that handle

The handle-form object is safe to cache and compare. The concrete value stays
session-bound.

## Cache Integration

The dagql cache owns the session-resource binding tables:

- `sessionResourcesBySession`
- `sessionHandlesBySession`

The important APIs are:

- `BindSessionResource`
- `ResolveSessionResource`

`BindSessionResource` stores:

- session ID
- client ID
- handle
- concrete value

and records that the session currently has that handle available.

`ResolveSessionResource` later looks up the concrete value from:

- session ID
- client ID
- handle

with a fallback to the latest binding for that handle in the session if the
exact client did not bind it.

## How A Result Becomes Resource-Conditional

When a handle-form resource object is created, the result is stamped with:

- `WithContentDigest(handle)`
- `WithSessionResourceHandle(handle)`

`WithSessionResourceHandle` is what makes the result resource-conditional:

- it records `sessionResourceHandle`
- it inserts that handle into `requiredSessionResources`

Later, as results depend on other results, the cache recomputes transitive
`requiredSessionResources` by unioning dependency requirements.

So if a container depends on a secret, and an exec depends on that container,
the required resource handle propagates transitively.

## How Hits Are Filtered

When the cache finds candidate results, it does not immediately return the first
equivalent one.

Instead, `selectLookupCandidateForSessionLocked` filters candidates by checking:

- the result's `requiredSessionResources`
- the session's available handles in `sessionHandlesBySession`

Only a candidate whose required handle set is a subset of the session's loaded
handles is eligible.

This is the actual enforcement point of the core rule.

## Concrete Resource Categories Today

There are four handle-backed categories worth thinking about today.

## 1. Secret provider secrets: `secret(...)`

This is the preferred secret path.

The GraphQL entry point is `Query.secret`.

The user supplies:

- a provider URI
- optionally a custom `cacheKey`

The concrete value is a `Secret` with:

- `URIVal`
- `SourceClientID`

The handle is derived in one of two ways:

### Default behavior

If no custom cache key is supplied, the secret plaintext is loaded up front and
the handle is derived from the plaintext using `SecretHandleFromPlaintext`.

That means two provider secrets with the same plaintext get the same handle by
default, even if their URIs differ.

This is what enables the important equivalence behavior:

- one client loads `env://FOO`
- another loads `env://BAR`
- the plaintext is the same
- therefore the handles match
- therefore downstream cache reuse is possible
- but only if both sessions have loaded that handle

### Custom cache key

If `cacheKey` is provided, the handle is derived from that string instead of the
plaintext.

That is an explicit override:

- different secrets with the same custom cache key are considered equivalent for
  cache purposes
- even if their URIs or plaintext differ

This is intentionally powerful and intentionally user-directed.

### Failure fallback

If the provider secret cannot be resolved up front for plaintext-based handle
derivation, the implementation falls back to a random cache key.

That disables cross-call equivalence instead of accidentally making unrelated
provider failures collide.

## 2. `setSecret(name, plaintext)`

This is the in-session plaintext secret path.

The GraphQL entry point is `Query.setSecret`.

Unlike provider secrets, the handle here is **not** derived from plaintext.

It is derived by `SetSecretHandle(name, accessor)`, where `accessor` comes from
`GetClientResourceAccessor`.

So `setSecret` equivalence is based on:

- the user-visible secret name
- the scoped accessor

not directly on plaintext.

This is the backwards-compatible behavior the current system preserves.

That means `setSecret` does **not** have the same equivalence semantics as the
provider-secret path.

The plaintext itself stays only in the concrete bound value. The handle-form
secret object carries:

- `Handle`
- content digest set to `Handle`
- session-resource requirement set to `Handle`

One other important detail: `setSecret` sanitizes the call frame so the handle
result's `ResultCall` records `"***"` for plaintext rather than the actual
secret value.

## 3. SSH auth sockets: `_sshAuthSocket(...)`

This is the SSH-specific socket path.

It is currently mostly used by:

- git SSH operations
- some SDK / nesting flows
- Dockerfile `--mount=type=ssh` paths through `dockerBuild`

The GraphQL entry point is the internal host field `Host._sshAuthSocket`.

The concrete socket is a real socket source, but the handle is derived from SSH
agent fingerprints, not from the local socket path.

That is crucial.

The handle is produced by:

- `ScopedSSHAuthSocketHandle(secretSalt, fingerprints)` for the main client
- `ScopedNestedSSHAuthSocketHandle(secretSalt, fingerprints, clientID)` for
  nested clients

Important consequences:

- two different SSH agent socket paths with the same key fingerprints can be
  equivalent
- nested clients deliberately get a more narrowly scoped handle than the main
  client

So SSH socket equivalence is semantic "same agent identity set", not "same file
path on the host".

## 4. Host unix sockets: `host.unixSocket(path: ...)`

This is the generic opaque unix socket path.

The GraphQL entry point is `Host.unixSocket`.

The concrete socket is stored as:

- `Kind = unix_opaque`
- `URLVal = unix://...`
- `SourceClientID`

The handle is derived by `HostUnixSocketHandle`, which uses
`GetClientResourceAccessor(...)` for the given path.

So host unix socket equivalence is accessor-based, not content-based.

Unlike SSH sockets, we do not derive equivalence from the socket's behavior or a
remote identity. We just treat the scoped accessor as the handle.

This is intentionally weaker and more opaque than the SSH case.

## A Note On Socket Kinds

There is also `SocketKindHostIP`.

That exists in the type system, but it is not part of the handle-based session
resource model discussed here in the same way the four categories above are.

The interesting handle-backed categories are:

- provider secret
- `setSecret`
- SSH auth socket
- opaque host unix socket

## `GetClientResourceAccessor`

`GetClientResourceAccessor` is an important helper for `setSecret` and host
unix sockets.

It computes an HMAC-based accessor over:

- the external resource name/path
- the caller module implementation scope digest, if any

That gives two important properties:

1. the external name is not directly recoverable from the handle input
2. accessors are scoped by caller module context rather than being totally
   global

This is why `setSecret("FOO", ...)` and `host.unixSocket("/tmp/x")` are not
just globally keyed by those raw strings.

## Concrete Resolution Happens Late

The handle-form objects are what flow through the graph.

Concrete resolution happens only when somebody actually needs the underlying
resource.

For secrets:

- `resolveSessionSecret`
- `Secret.Name`
- `Secret.URI`
- `Secret.Plaintext`

For sockets:

- `ResolveSessionSocket`
- `Socket.URL`
- `Socket.PortForward`
- `Socket.ForwardAgentClient`
- `Socket.MountSSHAgent`
- `Socket.AgentFingerprints`

So the cached graph carries the handle, but actual execution-time operations
resolve back to concrete bound resources through the session tables.

## Execution-Time Use

The two most important execution-time consumers are:

### Secrets

Container exec paths eventually call `Secret.Plaintext(ctx)` to materialize
secret mount data or secret env data.

That means exec only succeeds if the current session can resolve the secret
handle to a concrete secret.

### Sockets

Container exec SSH mounts and git SSH operations eventually call
`Socket.MountSSHAgent(ctx)` / `ForwardAgentClient`.

That again depends on resolving the handle to a concrete session-bound socket.

So the handle is not just an identity trick. It is also the gate that connects
cacheable graph state back to the right live resource.

## Why This Enables Safe Equivalent Hits

The provider-secret example is the clearest one.

Suppose two sessions do the same operation except:

- session A loads a secret from `env://FOO`
- session B loads a secret from `env://BAR`
- both plaintext values are the same

With the current system:

1. both provider secrets derive the same handle from plaintext
2. downstream operations depending on that secret can become cache-equivalent
3. but the hit is only usable if each session has loaded that handle

So we get the desired behavior:

- semantic equivalence enables reuse
- per-session binding prevents cross-session resource theft

The same general pattern applies to SSH sockets by fingerprint identity.

## Git Is A Good Example Of The Nuance

Git paths show a few important subtleties.

### SSH sockets

For Git SSH URLs, the schema tries hard to route SSH auth through
`Host._sshAuthSocket`, not raw `host.unixSocket`, so the cache key can be scoped
by SSH key fingerprints instead of host path.

If the caller provides an unscoped socket, the schema reinvokes itself with a
scoped `_sshAuthSocket` result so that the scoped handle appears explicitly in
the DAG.

### HTTP auth secrets

For HTTP auth:

- the remote metadata cache key includes the secret handles for token/header
- this intentionally scopes remote metadata caching by auth configuration

The code comment is explicit about the reason: it is safer to scope by auth
configuration than risk cache poisoning across different auth methods.

### Content digests vs session-resource conditions

Git also mixes these resources into some content digest calculations or cache
scope strings.

That is related to cache identity, but it is not the whole story. The
session-resource gating is still separately important because equivalence alone
does not authorize a hit.

## Containers And Transitive Requirements

Containers do not need bespoke session-resource logic to participate.

They just depend on secrets and sockets as ordinary dagql results:

- `withSecretVariable`
- secret mounts in exec
- `withUnixSocket`
- SSH mounts in exec / dockerBuild

Because dependency attachment is exact and
`recomputeRequiredSessionResourcesLocked` unions requirements transitively,
container results automatically inherit the required handles of the secrets and
sockets they depend on.

That is what makes later cache hits on container-derived results conditional on
resource availability.

## Session Lifetime And Cleanup

Concrete bindings live under `sessionResourcesBySession` and
`sessionHandlesBySession`.

When the session is released:

- those maps are deleted for the session
- the session no longer advertises those handles as available
- any later cache hit requiring those handles will fail session compatibility

This is why the cache has to be involved in session resource lifecycle at all.
It is the place that:

- knows which results require which handles
- knows which sessions currently have which handles
- tears down the availability side when a session disconnects

## Persistence Behavior

Secrets and sockets do implement persisted object encoding, but what is persisted
is only the handle-form metadata, not the concrete session-bound value.

That is a very important part of the design.

We cannot safely persist the concrete resource itself:

- persisting actual secret plaintext would be a security problem
- persisting a live socket backing would not make sense in the first place

What *is* safe to persist is the handle form. Persisting the handle form means
persisting the fact that some result depends on "any secret or socket that
matches handle `H`".

That is exactly the right logical model:

- the cache can remember that a result depends on handle `H`
- a future session can load a matching secret or socket and bind handle `H`
- only then does the persisted hit become usable

This is what makes it coherent for other persistent objects to depend on session
resources. For example, a persistent container can still be persisted normally
even if it depends on a secret or socket, because what persistence keeps is the
handle-form dependency graph, not the concrete secret plaintext or live socket
instance.

So persistence preserves the conditional dependency, not the concrete backing
resource.

## Important Differences Between Categories

### Provider secrets

- default equivalence: plaintext-based
- optional override: custom cache key
- concrete value may be fetched from a provider

### `setSecret`

- equivalence: accessor + name
- not plaintext-based by default
- concrete value already lives in memory

### SSH auth sockets

- equivalence: SSH key fingerprints
- nested clients get narrower scoping

### Host unix sockets

- equivalence: accessor/path-derived
- opaque, not behavior/fingerprint-derived

## Current Limitations / Sharp Edges

### 1. The model is a little roundabout

It is intentionally indirect:

- graph carries handles
- cache binds concrete values
- execution resolves concrete values later

That is more moving parts than a direct "just store the secret/socket" model,
but it is what enables safe conditional reuse.

### 2. Category semantics are not uniform

Provider secrets and `setSecret` do not have identical equivalence semantics.
SSH sockets and host unix sockets do not either.

That is real and intentional, but it is worth keeping in mind when reasoning
about cache hits.

### 3. Session compatibility is necessary even when cache identity matches

A matching content digest or structural lookup is not enough by itself. The
session still must have the required handles loaded.

## Suggested Reading Order

If you want to load this model into your head quickly, this order works well:

1. `dagql/cache.go`
   - `BindSessionResource`
   - `ResolveSessionResource`
   - `WithSessionResourceHandle`
   - `recomputeRequiredSessionResourcesLocked`

2. `dagql/cache_egraph.go`
   - `selectLookupCandidateForSessionLocked`

3. `core/secret.go`
   - `SecretHandleFromCacheKey`
   - `SetSecretHandle`
   - `SecretHandleFromPlaintext`
   - `resolveSessionSecret`

4. `core/socket.go`
   - `HostUnixSocketHandle`
   - `ResolveSessionSocket`
   - `MountSSHAgent`
   - `AgentFingerprints`

5. `core/ssh_auth_socket.go`
   - `ScopedSSHAuthSocketHandle`
   - `ScopedNestedSSHAuthSocketHandle`

6. `core/schema/secret.go`
   - `secret`
   - `setSecret`

7. `core/schema/host.go`
   - `unixSocket`
   - `_sshAuthSocket`

8. `core/schema/git.go` and `core/git_remote.go`
   - how scoped auth resources get reflected into cacheable Git operations

## Short Summary

Session resources let the cache reuse work across equivalent secrets and sockets
without letting one session silently consume another session's backing resource:
the graph carries stable handles, the cache tracks which sessions have loaded
which handles, and a cache hit is only valid if the caller's session satisfies
the required handle set.
