---
title: Build checks
description: |
  BuildKit has built-in support for analyzing your build configuration based on
  a set of pre-defined rules for enforcing Dockerfile and building best
  practices.
keywords: buildkit, linting, dockerfile, frontend, rules
---

BuildKit has built-in support for analyzing your build configuration based on a
set of pre-defined rules for enforcing Dockerfile and building best practices.
Adhering to these rules helps avoid errors and ensures good readability of your
Dockerfile.

Checks run as a build invocation, but instead of producing a build output, it
performs a series of checks to validate that your build doesn't violate any of
the rules. To run a check, use the `--check` flag:

```console
$ docker build --check .
```

To learn more about how to use build checks, see
[Checking your build configuration](https://docs.docker.com/build/checks/).

<table>
  <thead>
    <tr>
      <th>Name</th>
      <th>Description</th>
    </tr>
  </thead>
  <tbody>
    <tr>
      <td><a href="./stage-name-casing/">StageNameCasing</a></td>
      <td>Stage names should be lowercase</td>
    </tr>
    <tr>
      <td><a href="./from-as-casing/">FromAsCasing</a></td>
      <td>The 'as' keyword should match the case of the 'from' keyword</td>
    </tr>
    <tr>
      <td><a href="./no-empty-continuation/">NoEmptyContinuation</a></td>
      <td>Empty continuation lines will become errors in a future release</td>
    </tr>
    <tr>
      <td><a href="./consistent-instruction-casing/">ConsistentInstructionCasing</a></td>
      <td>All commands within the Dockerfile should use the same casing (either upper or lower)</td>
    </tr>
    <tr>
      <td><a href="./duplicate-stage-name/">DuplicateStageName</a></td>
      <td>Stage names should be unique</td>
    </tr>
    <tr>
      <td><a href="./reserved-stage-name/">ReservedStageName</a></td>
      <td>Reserved words should not be used as stage names</td>
    </tr>
    <tr>
      <td><a href="./json-args-recommended/">JSONArgsRecommended</a></td>
      <td>JSON arguments recommended for ENTRYPOINT/CMD to prevent unintended behavior related to OS signals</td>
    </tr>
    <tr>
      <td><a href="./maintainer-deprecated/">MaintainerDeprecated</a></td>
      <td>The MAINTAINER instruction is deprecated, use a label instead to define an image author</td>
    </tr>
    <tr>
      <td><a href="./undefined-arg-in-from/">UndefinedArgInFrom</a></td>
      <td>FROM command must use declared ARGs</td>
    </tr>
    <tr>
      <td><a href="./workdir-relative-path/">WorkdirRelativePath</a></td>
      <td>Relative workdir without an absolute workdir declared within the build can have unexpected results if the base image changes</td>
    </tr>
    <tr>
      <td><a href="./undefined-var/">UndefinedVar</a></td>
      <td>Variables should be defined before their use</td>
    </tr>
    <tr>
      <td><a href="./multiple-instructions-disallowed/">MultipleInstructionsDisallowed</a></td>
      <td>Multiple instructions of the same type should not be used in the same stage</td>
    </tr>
    <tr>
      <td><a href="./legacy-key-value-format/">LegacyKeyValueFormat</a></td>
      <td>Legacy key/value format with whitespace separator should not be used</td>
    </tr>
    <tr>
      <td><a href="./redundant-target-platform/">RedundantTargetPlatform</a></td>
      <td>Setting platform to predefined $TARGETPLATFORM in FROM is redundant as this is the default behavior</td>
    </tr>
    <tr>
      <td><a href="./secrets-used-in-arg-or-env/">SecretsUsedInArgOrEnv</a></td>
      <td>Sensitive data should not be used in the ARG or ENV commands</td>
    </tr>
    <tr>
      <td><a href="./invalid-default-arg-in-from/">InvalidDefaultArgInFrom</a></td>
      <td>Default value for global ARG results in an empty or invalid base image name</td>
    </tr>
    <tr>
      <td><a href="./from-platform-flag-const-disallowed/">FromPlatformFlagConstDisallowed</a></td>
      <td>FROM --platform flag should not use a constant value</td>
    </tr>
    <tr>
      <td><a href="./copy-ignored-file/">CopyIgnoredFile (experimental)</a></td>
      <td>Attempting to Copy file that is excluded by .dockerignore</td>
    </tr>
  </tbody>
</table>
