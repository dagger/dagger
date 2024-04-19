# BuildKit project process guide

* [Issue categorization guidelines](#issue-categorization-guidelines)
  + [P0](#p0)
  + [P1](#p1)
  + [P2](#p2)
* [Release milestones](#release-milestones)
  + [Feature releases](#feature-releases)
  + [Patch releases](#patch-releases)
  + [Updating dependencies](#updating-dependencies)
* [Project scope](#project-scope)
* [Security boundary](#security-boundary)
  + [Host](#host)
  + [Client](#client)
  + [Untrusted sources](#untrusted-sources)
  + [Stored data](#stored-data)
  + [Examples of issues not (currently) considered security](#examples-of-issues-not--currently--considered-security)


## Issue categorization guidelines

Following categorization describes how priorities are assigned to bug reports, and how the priority is used to determine whether a fix will be included in a patch release. Note that not all issues need to have priority assigned if they don't need any special tracking for an upcoming patch release of a feature milestone.

### P0

Fixing this issue is the highest priority. As soon as a patch is available and verified a patch release will follow.

Examples:

- Regression in a critical code path
- Panic in a critical code path
- Corruption in critical code path or rest of the system
- Leaked zero-day critical security

### P1

Issues should be fixed with high priority and almost always included in a patch release. Unless waiting for another issue, patch releases should happen within a week.

Examples:

- Any regression, panic
- Measurable performance regression
- A major bug in a new feature in the latest release
- Incompatibility with upgraded external dependency

### P2

PRs opened for P2 bugs should be included in the next feature release milestone. A P2 fix may be included in a patch release, depending on the patch size and how likely the patch may break something some other functionality. Patch release is usually only done if there are P1 fixes, and in that case some of the P2 fixes may be included as well.

Examples:

- Confirmed bug
- Bugs in non-default configurations

## Release milestones

BuildKit is released with feature releases, patch releases, and security releases.

### Feature releases

Feature releases happen from the development branch, after which a release branch is cut for future patch releases (this can also happen in the code freeze time).

Users can expect 2-3 release candidate test releases before a feature release. First of these usually happens around 2 weeks before the actual release.

BuildKit maintains backward compatibility in the gRPC API with previous releases, so it is possible to use old clients with new daemon and vice versa. If a feature needs to be removed, then it is first marked deprecated in a feature release. We do not plan to ever make backward incompatible changes in certain areas like the LLB API. BuildKit APIs internally use feature detection to understand what features the other side of the API supports.

Go modules in the BuildKit repository aren't guaranteed to be backward compatible with previous release branches.

Once a new feature release is cut, no support is offered for the previous feature release. An exception might be if a security release suddenly appears very soon after a new feature release. There are no LTS releases. If you need a different support cycle, consider using a [product that includes BuildKit](https://github.com/moby/buildkit#used-by), (eg. Docker) instead.

Anyone can ask for an issue or PR to be included in the next feature- or patch release milestone, assuming it passes the requirements.

### Patch releases

Patch releases should only include the most critical patches. Everyone should always use the latest patch release, so stability is very important.

If a fix is needed but it does not qualify for patch release because of the code size or other criteria that make it too unpredictable, we will prioritize cutting a new feature release sooner, rather than making an exception for backporting.

Following PRs are included in patch releases

- P0 fixes
- P1 fixes, assuming maintainers don’t object because of the patch size
- P2 fixes, only if (both required)
    - proposed by maintainer
    - the patch is trivial and self-contained
- Documentation-only patches
- Runtime dependency updates (e.g. Alpine packages, runc)
    - may be updated to the latest patch release of the dependency
- Vendored dependency updates, only if
    - Fixing (qualifying) bug or security issue in BuildKit
    - Patch is small, otherwise a forked version of dependency with only the patches required

New features do not qualify for patch release.

### Updating dependencies

Runtime dependencies should usually use the latest stable release available when the first RC of the feature release was cut. Patch releases may update such dependencies to their own latest matching patch release.

The core vendored dependencies (eg. `x/sys` , OTEL, grpc, protobuf) should use the same dependency version that is used by the vendored `containerd` library. At feature release time containerd library should use a version from a containerd release branch. If you need to update such dependency, update it in containerd repository first and then update containerd in BuildKit. During development, using containerd development branch is allowed if the release timeline shows that containerd release (and matching update in BuildKit) will happen before next BuildKit feature release.

Docker dependencies from `moby/moby` and `docker/cli` may use versions from the development branch.

For other dependencies, updating to the latest patch release is always allowed in the development branch. Updating to a new feature release should have a reason unless the dependency is very stale. Dependencies should use a tagged version if one is available and there isn’t a need for a specific patch from an untagged commit. Go modules should define the lowest compatible version for their dependencies so there is no goal that all dependencies need to be in their latest versions before a new BuildKit feature release is cut.

Vendored dependency updates are considered for patch releases, except for rare cases defined in the earlier section.

A security scanner report for a dependency that isn't exploitable via BuildKit is not considered a valid reason for backports.

## Project scope

The following characteristics define the scope and purpose of the BuildKit project:

- BuildKit provides the best solution for defining a build graph, executing and caching it as efficiently as possible, and exporting the result to a place where it can be used by other tools.
- BuildKit is a place for fast collaboration around modern containerized build tooling. 
- BuildKit uses containers as an execution sandbox and distribution platform.
- BuildKit provides an API that is flexible enough to be used in many tools and use cases.
- BuildKit is secure by default and can be used with untrusted sources.
- The purpose of BuildKit's command line tool `buildctl` is to expose API features as directly as possible.
- BuildKit isn't limited to only supporting features used by Docker build.

Things that **do not** define BuildKit:

- Running processes on the host.
- Solving the following issues that should be left for external projects, such as:
  - Combining multiple build requests together
  - Managing and deploying BuildKit instances
  - Inventing new frontends
  - Running containers from mutable state
- Opinionated client-side UX features. An exception here is a `buildctl debug` command where we can experiment with ways to extract more debugging data out of BuildKit.

If you are an end user, you should probably consider a tool built with BuildKit, rather than using BuildKit directly.

## Security boundary

This section is for some guidelines about what BuildKit considers a security issue and what kind of guarantees all future BuildKit features should provide. If you are unsure if the case you have found is a security issue, it is always better to ask privately first.

### Host

- The BuildKit API with default daemon configuration does not allow changes to the host filesystem or reading the host filesystem outside of the BuildKit state directory.
- Application and frontend containers are not allowed to read or write to the host system, run privileged system calls, or access external devices directly. Monitoring the load of the system is allowed.

### Client

- Buildctl does not allow access to any directories or file paths that are not explicitly set by the user with command line arguments. The untrusted BuildKit daemon does not have any way to access files that were not listed.
- When extracting build results to a directory specified with `--output` or `--cache-to`, no subfile can escape to the outside directory (e.g. via symlinks)

### Untrusted sources

- Although discouraged, you can use untrusted resources in your build, like images, frontend, URLs. These resources, or containers created from the files of these resources, should not have a way to read/write/execute in the host or crash the BuildKit daemon.
  
  Exceptions:

  - Containers can use system resources (CPU, memory, disk) without specific limits.
  - Untrusted remote cache imports may not be used.

- An untrusted frontend may not export build results to a location (client-side directory, registry) without user permission with a specific build request. If the frontend initializes a pull with credentials from the client, this needs to be logged on the client-side progress stream.
- Frontends can not access registry credentials or tokens that a build is using, the SSH private keys used in SSH forwarding, nor keys that may be used to sign build results or attestations. Frontends can provide SBOM attestation for the builds it has performed but it can not alter the contents of provenance attestations generated by BuildKit daemon.
- If a build was started with a policy file, the untrusted frontend has no way to use resources that are denied by that policy.

### Stored data

- Credentials should not be logged or written to OpenTelemetry trace or progress stream. Note that this applies to registry credentials and URL sources, if user writes credentials into the arguments of their application containers, there is nothing BuildKit can do about it.
- Values of the build secrets should never be stored anywhere on the disk or included in the cache checksums.

### Examples of issues not (currently) considered security

- Multiple concurrent builds from separate client share their build resources without namespacing. For example, if both builds require pulling the same image, the pull only happens once and is authenticated only once. The same behavior happens with containers that use the same build secret or if local cache or cache mounts of a previous build are reused. If different behavior is needed, consider running multiple instances of buildkitd for each of the namespaces.
- Remote cache resources provided with `--cache-to` need to be trusted by the user. If they have been manipulated by an attacker, this can result in an incorrect cache match by BuildKit solver.
- Application containers may cause the system to run out of resources (e.g. memory). In that case BuildKit should be configured with a cgroup parent.
- By default, registry credentials are not shared with BuildKit daemon, and short-lived token is generated on client side instead. For backward compatibility this can be bypassed and daemon can choose to ask for credentials instead (this is always required for basic auth). In such cases, the sending of credentials should be logged by the client, but no special confirmation from the user is needed.
- Untrusted frontends are free to run any builds, for example, they can run a container with a secret mounted and then read out the secret value. They are not allowed to see your registry credentials/tokens or signing keys.
