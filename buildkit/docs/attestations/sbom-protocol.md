# SBOM Scanning Protocol

BuildKit supports automatic creation of [SBOMs](https://en.wikipedia.org/wiki/Software_supply_chain)
for builds, attaching them as [image attestations](./attestation-storage.md).

To scan the filesystem contents, a user can specify an SBOM generator image.
When run, this image is passed the rootfs of the build stage as a read-only
mount, writes its SBOM scan data to a specified directory.

The SBOM generator image is expected to follow the rules of the BuildKit SBOM
generator protocol, defined in this document.

> **Note**
>
> Currently, only SBOMs in the [SPDX](https://spdx.dev) JSON format are
> supported.
>
> These SBOMs will be attached to the final image as an in-toto attestation
> with the `https://spdx.dev/Document` predicate type.

## Implementations

The following SBOM generator images are available:

- [docker/buildkit-syft-scanner](https://github.com/docker/buildkit-syft-scanner)

## Parameters

A single run of a generator may specify multiple target filesystems to scan by
passing multiple paths - the scanner should scan all of them. Each filesystem
target has a **name**, specified by the final component of the path for that
target. A generator may produce any number of scans for the available targets -
though ideally it should aim to produce a single scan per target.

These parameters will be passed to the generator image as environment variables
by BuildKit:

- `BUILDKIT_SCAN_DESTINATION` (required)

  This variable specifies the directory where the scanner should write its
  SBOM data. Scanners should write their SBOMs to `$BUILDKIT_SCAN_DESTINATION/<scan>.spdx.json`
  where `<scan>` is the name of an arbitrary scan. A scanner may produce
  multiple scans for a single target - scan names must be unique within a
  target, but should not be considered significant by producers or consumers.

- `BUILDKIT_SCAN_SOURCE` (required)

  This variable specifies the main target, passing the path to the root
  filesystem of the final build result.

  The scanner should scan this filesystem, and write its SBOM result to
  `$BUILDKIT_SCAN_DESTINATION/$(basename $BUILDKIT_SCAN_SOURCE).spdx.json`.

- `BUILDKIT_SCAN_SOURCE_EXTRAS` (optional)

  This variable specifies additional targets, passing the path to a directory
  of other root filesystems. If the variable is not set, is empty, or contains
  a directory that does not exist, then no extras should be scanned.

  The scanner should iterate through this directory, and write its SBOM scans
  to `$BUILDKIT_SCAN_DESTINATION/<scan>.spdx.json`, similar to above.

A scanner must not error if optional parameters are not set.

The scanner should produce SBOM results for all filesystems specified in
`BUILDKIT_SCAN_SOURCE` or `BUILDKIT_SCAN_SOURCE_EXTRAS` but must not produce
SBOM results for any other filesystems.

## Further reading

See [frontend/attestations/sbom/sbom.go](https://github.com/moby/buildkit/blob/master/frontend/attestations/sbom/sbom.go)
for the code that invokes the user-specified generator.
