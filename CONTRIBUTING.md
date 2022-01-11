# Contributing to Dagger

## GitHub Workflow

The recommended workflow is to clone the repository from `dagger/dagger` and
open pull requests from your own fork.

### 1) Cloning the repository

```sh
git clone https://github.com/dagger/dagger.git
```

**NOTE**: If you cloned your fork, either switch back to `dagger/dagger` using
`git remote` or start over.

### 2) Forking

- Click on the _Fork_ button on GitHub
- Add your fork as a remote

```sh
git remote add fork git@github.com:MYFORK/dagger.git
```

### 3) Creating a Pull Request

```sh
# create a branch
git checkout -b mybranch

# make changes to your branch, use `git commit -s`, ...
# ...

# push the branch to your own fork
git push -u fork mybranch

# create a pull request from https://github.com/dagger/dagger
```

### 4) Rebasing

```sh
git checkout main
git pull  # <-- this will pull from `dagger/dagger`
git checkout mybranch
git rebase main  # <-- this will rebase `dagger/dagger` into your `FORK/dagger`
git push -f -u fork mybranch  # <-- update the pull request
```

## Scope of Pull Requests

We prefer small incremental changes that can be reviewed and merged quickly.
It's OK if it takes multiple pull requests to close an issue.

The idea is that each improvement should land in Dagger's main branch within a
few hours.  The sooner we can get multiple people looking at and agreeing on a
specific change, the quicker we will have it out in a release.  The quicker we
can get these small improvementes in a Dagger release, the quicker we can get
feedback from our users and find out what doesn't work, or what we have missed.

The added benefit is that this will force everyone to think about handling
partially implemented features & non-breaking changes. Both are great
approached, and they work really well in the context of Dagger.

["Small incremental changes FTW!"](https://github.com/dagger/dagger/pull/1348#issuecomment-1009628531) -> "Small pull requests that get merged within hours!"

## Commits

### DCO

Contributions to this project must be accompanied by a Developer Certificate of
Origin (DCO).

All commit messages must contain the Signed-off-by line with an email address that matches the commit author. When commiting, use the `--signoff` flag:

```sh
git commit -s
```

The Signed-off-by line must match the **author's real name**, otherwise the PR will be rejected.

### Commit Messages

[How to Write a Git Commit Message](https://chris.beams.io/posts/git-commit/)

Guidelines:

- Group Commits: Each commit should represent a meaningful change (e.g. implement
  feature X, fix bug Y, ...).
- For instance, a PR should not look like _1) Add Feature X 2) Fix Typo 3) Changes to features X 5) Bugfix for feature X 6) Fix Linter 7) ..._<br>
  Instead, these commits should be squashed together into a single "Add Feature" commit.
- Each commit should work on its own: it must compile, pass the linter and so on.
- This makes life much easier when using `git log`, `git blame`, `git bisect`, etc.
- For instance, when doing a `git blame` on a file to figure out why a change
  was introduced, it's pretty meaningless to see a _Fix linter_ commit message.
  "Add Feature X" is much more meaningful.
- Use `git rebase -i main` to group commits together and rewrite their commit message
- To add changes to the previous commit, use `git commit --amend -s`. This will
  change the last commit (amend) instead of creating a new commit.
- Format: Use the imperative mood in the subject line: "If applied, this commit
  will _your subject line here_"

## Docs

### Use relative links to markdown files

Link to markdown files (`[link](../foo.md)`) instead of relative URLs
(`[link](/foo)`).

The docs compiler will replace file links with relative URLs automatically.

This is to avoid broken links. If a file gets renamed, the compiler will
catch broken links and throw an error. Relative URLs get broken unnoticed.

## FAQ

### How to run the markdown linter locally

First install `markdownlint-cli`:

- On Mac OS: `brew install markdownlint-cli`
- On other systems, with yarn installed: `yarn global add markdownlint-cli`

Then from the repository root:

```console
markdownlint -c .markdownlint.yaml docs/**/*.md
```
