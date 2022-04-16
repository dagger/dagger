---
slug: /1227/contributing
displayed_sidebar: 0.2
---

# Contributing to Dagger

The best way to find a good contribution is to use Dagger for something. Then write down what problems you encounter.
Could be as simple as a question you had, that the docs didn't answer. Or a bug in the tool, or a missing CUE package.
Then pick an item that you're comfortable with in terms of difficulty, and give it a try. 🙂

You can ask questions along the way, we're always happy to help you with your contribution. The bigger the contribution,
the earlier you should talk to maintainers to make sure you're taking the right approach and are not wasting your effort
on something that will not get merged.

## Package Style Guide

If you're contributing CUE packages, be sure to follow the [Package Coding Style](./1226-coding-style.md) guide as well.

## GitHub Workflow

The recommended workflow is to fork the repository and open pull requests from your fork.

### 1. Fork, clone & configure Dagger upstream

- Click on the _Fork_ button on GitHub
- Clone your fork
- Add the upstream repository as a new remote

```shell
# Clone repository
git clone https://github.com/$YOUR_GITHUB_USER/$REPOSITORY.git

# Add upstream origin
git remote add upstream git@github.com:dagger/$REPOSITORY.git
```

### 2. Create a pull request

```shell
# Create a new feature branch
git checkout -b my_feature_branch

# Make changes to your branch
# ...

# Commit changes - remember to sign!
git commit -s

# Push your new feature branch
git push my_feature_branch

# Create a new pull request from https://github.com/dagger/$REPOSITORY
```

### 3. Update your pull request with latest changes

```shell
# Checkout main branch
git checkout main

# Update your fork's main branch from upstream
git pull upstream main

# Checkout your feature branch
git checkout my_feature_branch

# Rebase your feature branch changes on top of the updated main branch
git rebase main

# Update your pull request with latest changes
git push -f my_feature_branch
```

## Scope of pull requests

We prefer small incremental changes that can be reviewed and merged quickly.
It's OK if it takes multiple pull requests to close an issue.

The idea is that each improvement should land in Dagger's main branch within a
few hours.  The sooner we can get multiple people looking at and agreeing on a
specific change, the quicker we will have it out in a release. The quicker we
can get these small improvementes in a Dagger release, the quicker we can get
feedback from our users and find out what doesn't work, or what we have missed.

The added benefit is that this will force everyone to think about handling
partially implemented features & non-breaking changes. Both are great
approaches, and they work really well in the context of Dagger.

["Small incremental changes ftw"](https://github.com/dagger/dagger/pull/1348#issuecomment-1009628531) -> Small pull requests that get merged within hours!

## Commits

### DCO

Contributions to this project must be accompanied by a Developer Certificate of
Origin (DCO).

All commit messages must contain the Signed-off-by line with an email address
that matches the commit author. When commiting, use the `--signoff` flag:

```shell
git commit -s
```

The Signed-off-by line must match the **author's real name**, otherwise the PR will be rejected.

### Commit messages

:::tip
[How to Write a Git Commit Message](https://chris.beams.io/posts/git-commit/)
:::

Guidelines:

- **Group Commits:** Each commit should represent a meaningful change (e.g. implement feature X, fix bug Y, ...).
  - For instance, a PR should not look like _1) Add Feature X 2) Fix Typo 3) Changes to features X 5) Bugfix for feature X 6) Fix Linter 7)_ ...
  - Instead, these commits should be squashed together into a single "Add Feature" commit.
- Each commit should work on its own: it must compile, pass the linter and so on.
  - This makes life much easier when using `git log`, `git blame`, `git bisect`, etc.
  - For instance, when doing a `git blame` on a file to figure out why a change
  was introduced, it's pretty meaningless to see a _Fix linter_ commit message.
  "Add Feature X" is much more meaningful.
- Use `git rebase -i main` to group commits together and rewrite their commit message.
- To add changes to the previous commit, use `git commit --amend -s`. This will
  change the last commit (amend) instead of creating a new commit.
- Format: Use the imperative mood in the subject line: "If applied, this commit
  will _your subject line here_"
- Add the following prefixes to your commit message to help trigger automated processes[^1]:
  - `docs:` for documentation changes only (e.g., `docs: Fix typo in X`);
  - `test:` for changes to tests only (e.g., `test: Check if X does Y`);
  - `chore:` general things that should be excluded (e.g., `chore: Clean up X`);
  - `website:` for the documentation website (i.e., the frontend code; e.g., `website: Add X link to navbar`);
  - `ci:` for internal CI specific changes (e.g., `ci: Enable X for tests`);
  - `infra:` for infrastructure changes (e.g., `infra: Enable cloudfront for X`);

[^1]: See [https://www.conventionalcommits.org](https://www.conventionalcommits.org)

## Docs

### Use relative links to markdown files

Link to markdown files (`[link](../foo.md)`) instead of relative URLs
(`[link](/foo)`).

The docs compiler will replace file links with relative URLs automatically.

This is to avoid broken links. If a file gets renamed, the compiler will
catch broken links and throw an error. Relative URLs get broken unnoticed.

## FAQ

### How to run the linter locally

To run all linters, from the repository root, run:

```shell
dagger do lint
```

### How to re-run all GitHub Actions jobs?

There isn't a button that Dagger contributors can click in their fork that will
re-run all GitHub Actions jobs. See issue
[#1669](https://github.com/dagger/dagger/issues/1169) for more context.

The current workaround is to re-create the last commit:

```shell
git commit --amend -s

# Force push the new commit to re-run all GitHub Actions jobs:
git push -f mybranch
```

### Can I use a remote development environment?

Yes! The Dagger repository has Github Codespaces configuration included to help help you get started contributing directly from GitHub.

The versions of `dagger` and `cue` you are working against will be pre-installed so you can develop your packages, plans and tests with the right tools.

You will also have basic syntax highlighting and formatting for CUE in Visual Studio Code via pre-installed extensions.

Support for other platforms, such as Gitpod, may be added if there is demand. Visit the [developer experience](https://github.com/dagger/dagger/discussions/2052) discussion on GitHub to show your interest.
