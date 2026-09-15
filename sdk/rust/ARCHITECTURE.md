**Warning** This SDK is **experimental**. Please do not use it for anything
mission-critical. Possible issues include:

- Missing features
- Stability issues
- Performance issues
- Lack of polish
- Upcoming breaking changes
- Incomplete or out-of-date documentation Please report any issues you
  encounter. We appreciate and encourage contributions. If you are a Rust
  developer interested in contributing to this SDK, we welcome you!

# Architecture

- `crates/dagger-bootstrap` Root project mainly used for generating the CLI,
  which in turn is used to bootstrap the code generation from `dagger`
- `crates/dagger-sdk` Contains the actual sdk in which the user interacts,
  `dagger-core` is reexported through this API as well.
- `crates/dagger-codegen` This is the bulk of the work, it takes the input
  graphql and spits out the API in which the user interacts, this is heavily
  inspired by other `dagger-sdk's`. It primarily turns graphql into rust code.
