# Deprecated features

This page provides an overview of features that are deprecated in BuildKit.

As changes are made to BuildKit there may be times when existing features need
to be removed or replaced with newer features. Before an existing feature is
removed it is labeled as "deprecated" within the documentation and remains in
BuildKit for at least one stable release unless specified explicitly otherwise.
After that time it may be removed.

Users are expected to take note of the list of deprecated features each release
and plan their migration away from those features, and (if applicable) towards
the replacement features as soon as possible.

The table below provides an overview of the current status of deprecated
features:

- **Deprecated**: the feature is marked "deprecated" and should no longer be
  used. The feature may be removed, disabled, or change behavior in a future
  release. The _"Deprecated"_ column contains the release in which the feature
  was marked deprecated, whereas the _"Remove"_ column contains a tentative
  release in which the feature is to be removed. If no release is included in
  the _"Remove"_ column, the release is yet to be decided on.
- **Removed**: the feature was removed, disabled, or hidden. Refer to the linked
  section for details. Some features are "soft" deprecated, which means that
  they remain functional for backward compatibility, and to allow users to
  migrate to alternatives. In such cases, a warning may be printed, and users
  should not rely on this feature.

| Status     | Feature                                 | Deprecated | Remove | Recommendation                                                   |
|------------|-----------------------------------------|------------|--------|------------------------------------------------------------------|
| Deprecated | [Build information](#build-information) | v0.11      | v0.12  | Use [provenance attestations](./attestations/slsa-provenance.md) |

## Build information

[Build information](https://github.com/moby/buildkit/blob/v0.11/docs/buildinfo.md)
structures have been introduced in [BuildKit v0.10.0](https://github.com/moby/buildkit/releases/tag/v0.10.0)
and are generated with build metadata that allows you to see all the sources
(images, git repositories) that were used by the build with their exact
versions and also the configuration that was passed to the build. This
information is also embedded into the image configuration if one is generated.

With the introduction of [provenance attestations](./attestations/slsa-provenance.md)
in [BuildKit v0.11.0](https://github.com/moby/buildkit/releases/tag/v0.11.0),
the build information feature has been deprecated and removed in v0.12.0
release.
