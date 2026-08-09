# Dagger Rust engine integration

`dagger-sdk-engine` is the private Rust operation tool packaged with the Dagger
engine. It owns canonical operation models and, in later implementation slices,
will own Cargo project adoption, generated-file ownership, confined publication,
and runtime verification.

This crate is repository tooling rather than an application dependency. It is not
published, and generated user projects depend only on the public `dagger-sdk` crate.

Licensed under Apache-2.0 as part of the Dagger repository.
