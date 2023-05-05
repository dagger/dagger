# SDK Contribution Guide

This document serves as a guide for creating a new Dagger SDK, but also to help maintain consistency between existing ones.

## Purpose of an SDK

- Best DX
- Technically, any language that can make HTTP requests, can talk to Dagger.
  - However, without concurrency features there’s a bottleneck on performance that makes the chosen language very limiting.
- Provide the best developer experience for working with Dagger, in a way that feels natural to the users of the chosen language (idiomatic).

## Codegen

- Abstraction layers & third-party libraries
  - HTTP request
  - GraphQL request
  - GraphQL query builder
  - Dagger client codegen
    - Type safe if possible for best IDE experience
    - Convenient imports
      - `dagger.Container` (type)
      - `dagger.Client` (i.e., `Query` type)
      - `client.Container()` (instance)
- Introspection query
  - Own language?
  - Dagger’s Go lib
  - [GraphQL Code Generator](https://the-guild.dev/graphql/codegen)
- Use language’s conventions
  - Naming (classes, functions, etc…)
  - Handle default values in field parameters
- Sort for reproducible generation
  - Types and object fields
  - Not field parameters (use API order), unless grouping required vs default arguments
- Handle types (by order)
  - Custom scalars
  - Enums
  - Input objects
  - Object types
- Docstrings
  - Get all documentation from introspection, including field parameters
  - Rename deprecated fields with language’s convention (backticks)
  - Document that `ID` fields are lazily evaluated, no operation is actually run
- Chainable API
  - Immutable query builder
  - Laziness
    - Lazy when building query
    - Execute when a value is needed (leaf)
    - Make sure DX is clear which is which (rule of thumb)
  - Deprecation warnings
  - Objects → ID
  - Extract result from response
  - Error/Exception types
  - `With()` convenience

## Establishing a session

- Convention: `client = dagger.Connect(dagger.Config())` (or similar, whatever’s most idiomatic)
- Config
  - Timeouts
    - For establishing connect (default: 10s)
    - For executing a query (default: None)
  - Stream engine logs to a file (e.g., `log_output=stderr` )
  - Workdir & config path

## Provisioning

- Purpose: Development convenience, easy onboarding
- May be temporary! (i.e., entrypoints)
- Log progress
- Architecture mermaid
  - Check session params env var
  - If not, check CLI_BIN env var
  - If not, check cli in cache
  - If not, download cli to cache
  - Run cli subprocess
- Session start (i.e., `dagger session`)
  - `--workdir` & `--config-path` (only if set)
  - Add labels
    - `--label dagger.io/sdk.name:%s`
    - `--label dagger.io/sdk.version:%s`
  - Handle “text file busy” error
  - Print error message to user during provisioning
    - Duplicated stream from session’s stderr
      - In-memory buffer for user
      - Write back to log_output
  - Return connection params from stdout
