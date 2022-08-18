# Writing an API extension

## Overview

This document explains how to write an API extension for Dagger/cloak.

## SDK-specific guides

* [Writing an extension in Go](writing_extensions_go.md)
* [Writing an extension in Typescript](writing_extensions_typescript.md)

## Concepts

### Extension

A schema + associated resolvers that can be loaded into Cloak at runtime to add new functionality beyond the core API.

### Extension Runtime

The part of a resolver that serves as an interface between the Cloak server and the code or other artifacts that
implement the actual functionality of the extension. Runtimes implement the "runtime protocol", which defines how inputs
are provided to a resolver and how outputs are provided back to the Cloak server.

It is up to each runtime implementation to determine how the inputs are converted to outputs.

[Extension Runtime Protocol reference](extension_runtime_protocol.md)


## Dependency

Resolvers can declare a list of extensions they depend on; that list determines the schema presented to the resolver by Cloak at the time the resolver
is executed.

### Host Client

The initiator of queries to Cloak (the root of a DAG). These can currently be split into a few subtypes:

- Direct graphql queries (i.e. with `curl`, a `GraphiQL` web console, etc.)
- The `cloak` CLI
- Embedded SDKs, where the host client is imported as a library and enables submission of queries in a way
  that looks very similar to implementing a resolver.

- TODO: I hate this name, but "client" by itself is too ambiguous with the concept of a graphql client. Need to come up with something better

### Resolver

An implementation of one field of a schema. When Cloak is evaluating a query, it needs to run code to calculate the values
being requested; we call this "invoking a resolver". When an extension is loaded, each field in the extension's schema
that takes args must be associated with a resolver.

Resolvers are provided to Cloak in the form of a Filesystem+ImageConfig pair. The entrypoint of the image config is expected
to be an executable that implements the "runtime protocol".

Resolvers have access to the Cloak API during execution, which enables them to invoke other resolvers as needed.

- TODO: mention complications of cases where fields w/ no args can be resolvers; what a "trivial resolver" is in graphql parlence, etc.
- TODO: this is the closest thing to what we used to call an "action". Should we still call it an action instead of resolver?

### SDK

A collection of tools and libraries to help you develop an extension in a given language.

SDKs are distributed as extensions: you can think of them as extensions used to create other extensions.
For example, if the Go SDK Extension is loaded, a schema like this may become available:

```graphql
extend type Stubber {
  go(schema: String, dependencies: [Dependency!], source: Filesystem!) Filesystem!
}
extend type Runtime {
  go(source: Filesystem!, goVersion: String!) Extension!
}
```

NOTE: `Extension` is a type defined as part of core, will look something **_vaguely along the lines of_**:

```graphql
type Extension {
  schema: String!
  resolvers: Filesystem!
  dependencies: [Dependency!]
}
type Dependency {
  schema: String!
  resolvers: Filesystem!
}
```

Say you are implementing the Alpine extension in Go. The idea is:

1. You write the schema of the Alpine extension and declare that you are using the Go SDK.
2. The Go SDK extension is loaded (e.g. by `cloak generate` or similar tools)
3. The `go` stubber is invoked, which outputs an implementation skeleton and codegen'd clients to your local directory.
4. You fill in your implementation of the resolvers needed for Alpine
5. When you want to invoke your action, the implemented source code is provided to the `go` runtime resolver, which returns an `Extension`.
   - That `Extension` can then be loaded into your schema, which will make the Alpine schema+resolvers available for querying.

Note that the `Stubber` implementation is optional here. For example, a `Makefile` SDK may just consist of a runtime like:

```graphql
extend type Runtime {
  makefile(source: Filesystem!) Extension!
}
```

Where the `source` arg is just a filesytem containing a `Makefile` and the returned `Extension` has the derived schema of the Makefile, the filesystem containing the resolvers that will be invoked for each field of the schema, etc.

- TODO: It's not yet incredibly clear how to model extensions that make use of multiple runtimes (i.e. an extension that has resolvers that use Go and resolvers that use Bash). Should be possible with some tweaks, but need to figure those out.
- TODO: the use of `extend` doesn't make sense here by itself, need to explore where SDK tools like the stubber and runtimes get stitched into the overall schema. It may make sense to use graphql interfaces here?

## Stubber
A tool used when developing resolvers that generates:

1. Implementation skeletons for the runtime being used by the resolver
1. Native clients for invoking other actions

The input to a stubber is:

1. The schema of the resolver being developed
1. The dependencies of the resolver being developed
1. The current implementation (if any) of the resolver. This supports two optional features:
   - Updating existing resolver implementations with modified schemas (of the resolver or of a dependency)
   - Code-first schemas

The output of a stubber is a filesystem that will be exported to the developer's local machine.

- TODO: should this be split up into two subtypes of stubber: one for impl, one for native clients?

