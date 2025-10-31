The best way to find a good contribution is to use Dagger for something. Then
write down what problems you encounter. Could be as simple as a question you
had, that the docs didn't answer. Or a bug in the tool, or a missing feature.
Then pick an item that you're comfortable with in terms of difficulty, and give
it a try. 🙂

You can ask questions along the way, we're always happy to help you with your
contribution. The bigger the contribution, the earlier you should talk to
maintainers to make sure you're taking the right approach and are not wasting
your effort on something that will not get merged.

For more information on SDK contributions, see the SDK-specific [CONTRIBUTING.md](https://github.com/dagger/dagger/tree/main/sdk/CONTRIBUTING.md).

## Initial setup

### 1. Join our Discord

Dagger's community is one of its killer features. If you haven't already, we strongly recommend [joining our Discord server](https://discord.gg/UhXqKz7SRM). This will save you tons of time, and the constant feedback and encouragement will help you stay motivated.

### 2. Install Dagger

To develop Dagger, we use - surprise! - Dagger. Start by installing the [latest stable release](http://dagger.io/install). If you're using an old version, make sure to upgrade it.

### 3. Install Changie

Changie is a simple tool for managing release notes. You will need it to submit user-facing changes. Make sure to [install the latest version](https://changie.dev/guide/installation/).

### 4. Git setup

- Navigate to [github.com/dagger/dagger](https://github.com/dagger/dagger)
- Click on the *Fork* button
- Clone your fork

  ```shell
  git clone git@github.com:$YOUR_GITHUB_USER/dagger.git
  ```

- Add the upstream repository as a new remote

  ```shell
  git remote add upstream git@github.com:dagger/dagger.git
  ```

## Contribution workflow

The overall workflow looks like this:

### 1. Claim an issue

[Find](https://github.com/dagger/dagger/issues) or [report](https://github.com/dagger/dagger/issues/new/choose) an issue on Github. This could be a bug, missing feature, missing documentation, or "rough edge". Write a comment to declare your intention to contribute a solution. If others are already working on it, make sure to coordinate with them.

### 2. Communicate

For bigger contributions, communicate upfront your plan and design, and ask maintainers for feedback. You can do this in the Github issue, or on Discord. Communicate early and often! This will save your time as well as the maintainer's.

### 3. Develop

Use your local dev environment to iterate until you're ready for review.

#### Manual testing

To run an interactive playground with all Dagger components built and integrated:

```shell
dagger call playground terminal
```

This will:

1. Build the Dagger engine, with core SDKs bundled inside
2. Run the dev engine as a Dagger service (dagger-in-dagger)
3. Build the Dagger CLI
4. Run an ephemeral container with the CLI installed, and the engine available as a sidecar
5. Open an interactive terminal

#### Integration testing

- Run all core tests: `dagger call test all`
- Run available core tests: `dagger call test list`
- Run a specific core test (eg.  `TestNamespacing` in the `TestModule` suite): `dagger call test specific --pkg="./core/integration" --run="^TestModule/TestNamespacing$"`
- Run SDK tests: `dagger call test-sdks`

#### Linting

To run all linters: `dagger call lint`

#### Local docs server

To run a local docs server: `dagger -m docs call server up`


### 4. Prepare your pull request

Before submitting your pull request, follow this checklist.

- Generate API docs, client bindings, and other generated files with `dagger call generate`, and include the output in your git commit.
- Call all linters: `dagger call lint`
- If your change is user-facing, add a release note: run `changie new` then follow instructions. Add produced files to your commit.
- Understand the license. All contributions are made under the Apache License 2.0 (Apache-2.0). Make sure you are willing and able to honor the terms of the license.
- Make sure all your commits are accompanied by a [Developer Certificate of Origin (DCO)](https://developercertificate.org). You can do this with `git commit -s`. The `Signed-off-by` line must match the author's real name.
- Make sure your git commit messages are useful, accurate and concise. See [How to Write a Git Commit Message](https://chris.beams.io/posts/git-commit/)

### 5. Submit your pull request

- Push your feature branch to your github fork
- Create a new pull request from https://github.com/dagger/dagger

### 6. Review process

- A maintainer will review your pull request and may suggest changes, etc.
- If needed, make any changes and push them to your branch. If there are
  conflicts with `main`, rebase your branch on top of the latest `main` and
  force-push it (find any guide on how to do this online if you're not
  familiar).
- Once everything is good, a maintainer will merge your changes.

Thank you for considering a contribution! We appreciate it.
