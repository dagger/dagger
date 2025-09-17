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

## Development environment

For more detailed instructions on building, running and testing dagger locally,
see the dagger [dev module](https://github.com/dagger/dagger/tree/main/.dagger/README.md).

Working on dagger requires dagger to bootstrap it - you can install dagger
using the instructions at [https://docs.dagger.io/install](https://docs.dagger.io/install).
Because we dogfood all of our tooling ourselves, we recommend using the *most
recent* version of dagger to build (you can find the exact version used in our
ci by looking in `go.mod`).

## GitHub Workflow

The recommended workflow is to fork the repository and open pull requests from your fork.

### 1. Fork, clone & configure Dagger upstream

- Click on the *Fork* button on GitHub
- Clone your fork

  ```shell
  git clone git@github.com:$YOUR_GITHUB_USER/dagger.git
  ```

- Add the upstream repository as a new remote

  ```shell
  git remote add upstream git@github.com:dagger/dagger.git
  ```

### 2. Create a pull request

- Create a new feature branch for your changes:

  ```shell
  git checkout -b my_feature_branch
  ```

- Make whatever changes you need

- Commit your changes

  ```shell
  git commit -s
  ```

- Push your changes to your fork

  ```shell
  git push
  ```

- Create a new pull request from https://github.com/dagger/dagger

### 3. Review process

- A maintainer will review your pull request and may suggest changes, etc.
- If needed, make any changes and push them to your branch. If there are
  conflicts with `main`, rebase your branch on top of the latest `main` and
  force-push it (find any guide on how to do this online if you're not
  familiar).
- Once everything is good, a maintainer will merge your changes.

## Release notes

If this is a user-facing change, a maintainer will ask you to add a line for
the release notes. You will need to have [`changie` installed](https://changie.dev/guide/installation/).

Here is an example of what that looks like:

```shell
changie new
✔ Kind … Added
✔ Body … engine: add `Directory.Sync`
✔ GitHub PR … 5414
✔ GitHub Author … helderco
```

If there are code changes in the SDKs, run `changie new` in the corresponding directory, e.g. `sdk/go`, `sdk/typescript`, etc.

Remember to add & commit the release notes fragment. This will be used at
release time, in the changelog.

## Commits

### License

Contributions to this project are made under the Apache License 2.0 (Apache-2.0).

### DCO

Contributions to this project must be accompanied by a Developer Certificate of
Origin (DCO).

All commit messages must contain the Signed-off-by line with an email address
that matches the commit author. When committing, use the `--signoff` flag:

```shell
git commit -s
```

The Signed-off-by line must match the **author's real name**, otherwise the PR will be rejected.

### Commit messages

See:

- [How to Write a Git Commit Message](https://chris.beams.io/posts/git-commit/)
- [Conventional Commits](https://www.conventionalcommits.org)


## Github Actions

The workflows titled `*.gen.yml` under `.github/workflows` are generated using the [gha module](./modules/gha/main.go).

Workflows are defined in [.github/main.go](./.github/main.go). To modify an existing workflow, you should modify its configuration in [.github/main.go](./.github/main.go) and then regenerate the workflow yaml.

To regenerate the github actions yaml, run:

```
dagger -m .github <<.
 generate |
 export .
.
```

## Docs

### Running the docs locally

The `docs` submodule handles everything needed for running the documentation site locally.

It can be executed with:

```
dagger -m docs call server up
```
