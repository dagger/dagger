# Architecture

- `crates/dagger-bootstrap` Root project mainly used for generating the CLI,
  which in turn is used to bootstrap the code generation from `dagger`
- `crates/dagger-core` Contains all base types used during actual usage. This is
  where the primary logic lives in which the user interacts (\*disclaimer: most
  stuff haven't moved in here yet.)
- `crates/dagger-sdk` Contains the actual sdk in which the user interacts,
  `dagger-core` is reexported through this API as well.
- `crates/dagger-codegen` This is the bulk of the work, it takes the input
  graphql and spits out the API in which the user interacts, this is heavily
  inspired by other `dagger-sdk's`. It primarily turns graphql into rust code.
