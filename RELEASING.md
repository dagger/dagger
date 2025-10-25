# Releasing ![shields.io](https://img.shields.io/badge/Last%20updated%20on-October%223,%202025-success?style=flat-square)

This document describes the process for releasing Dagger.

## Let the team know

Before you go ahead and produce a new release, remember that it's a team
effort. The first step is to let the team know what is going to happen,
preferably a few days in advance so that they can react. We do our release
threads in public wherever possible, to give the community more visibility, and
to help external contributors to more easily keep in sync with the release
cadence.

To let the team know:

- [ ] Create a new milestone in [GitHub](https://github.com/dagger/dagger/milestones),
      e.g. [`v0.19.2 Milestone`](https://github.com/dagger/dagger/milestone/47)
- [ ] Create a new post in [Discord #team](https://discord.com/channels/707636530424053791/783828171153866782),
      e.g. [`v0.19.2 release`](https://discord.com/channels/707636530424053791/1430934913984499876/1430934919403671583)

This allows others to weigh in whether:

- we should go for a patch / minor bump
- there are any PRs that people are waiting to get merged
- any big features which need to remain experimental?
- etc.

Maybe there are breaking changes which we should be aware of and message
accordingly. Giving other team members a day or two to react - because
timezones! - will make this entire process smoother.

- [ ] If doing a minor release, determine if there are any deprecations that
      can be removed.

### Backwards compatibility

Where possible, we try to ensure backwards compatibility between mismatched cli
and engine versions. However, for technical reasons, this isn't always possible:
sometime the communication protocol changes, or a bug fix or new feature
requires changes on both the CLI and the engine.

Before releasing, make sure to sanity check the backwards compatibility of a
release. If you encounter issues, then:

- [ ] Add a release note using `changie new` (or add it later manually).
- [ ] Bump the minimum version numbers in [engine/version.go](https://github.com/dagger/dagger/blob/main/engine/version.go).

If unsure, bump both the client and engine minimum version numbers, but if
the backwards compatibility is only an issue in one direction, you only need
to bump that one.

### Improve this doc while releasing 改善

In order to keep this relevant & accurate, we improve this doc during the
release process. It's the best time to pause, observe how it all fits together,
and improve it. We want small, constant improvements which compound. Therefore:

- [ ] Save a copy of this doc outside of this repository (e.g.
      `~/Downloads/RELEASING.md`). Now open that copy in your editor and start
      ticking items off it as you make progress. Remember to add / remove / edit
      any parts which could be improved.
- [ ] Update the date in the shields.io badge, first line in this file.

## Workflow for releases off non-main branch

> [!WARNING]
>
> There are no additional instructions for this, since this hasn't been done in
> a while.
>
> Exercise caution!

## Required tooling

Before starting the releasing process, make sure you have your local
environment setup and ready to go. You'll need (at least) the following tools -
though you probably already have most of them if you're regularly contributing
to dagger.

- A UNIX environment and shell
- [git](https://git-scm.com/)
- [dagger](https://github.com/dagger/dagger) (the most recent release)
- [changie](https://changie.dev)
- [gh](https://cli.github.com/) github cli tool
- [golang](https://go.dev)

## 📝 Preparation

- [ ] Setup some variables used throughout the release process:

  ```console
  export RELEASE_BRANCH=main
  export DAGGER_REPO_REMOTE=$(git remote -v | grep -E "(github.com.dagger/dagger)" | head -n 1 | awk '{print $1}')
  ```

> [!NOTE]
>
> This process involves direct pushes to the github.com/dagger/dagger repository. Recently there have been permission issues here that result in errors like
>
> `refusing to allow an OAuth App to create or update workflow` when pushing.
>
> If you encounter this, you may need to change your git remote from https to ssh, e.g.
>
> `git remote set-url $DAGGER_REPO_REMOTE git@github.com:dagger/dagger.git`
>
> It's advisable to go back to https after the release is complete to not retain excessive permissions longer than necessary, e.g. to go back run
>
> `git remote set-url $DAGGER_REPO_REMOTE https://github.com/dagger/dagger.git`

- [ ] Setup the local branch to align with the remote branch being released

  ```console
  git checkout "$RELEASE_BRANCH"

  git pull $DAGGER_REPO_REMOTE "$RELEASE_BRANCH"
  ```

- [ ] Ensure that `.changes/.next` contains `$ENGINE_VERSION` - if it doesn't,
      update it now!

- [ ] Determine the next release version (use `patch`/`minor`/`major` to set the release type):

  ```console
  export ENGINE_VERSION="$(dagger call -m version next-release-version)"

  # this is required to interpolate $ENGINE_VERSION to the SDK release notes
  export CHANGIE_ENGINE_VERSION="$ENGINE_VERSION"
  ```

- [ ] Create the target release notes branch for a PR - e.g. `prep-v0.12.4`.

  ```console
  git checkout -b prep-$ENGINE_VERSION
  ```

- [ ] Bump internal versions (sdks + docs + helm chart) to the target version

  ```console
  dagger call -m releaser bump --version="$ENGINE_VERSION"
  git add docs sdk helm
  git commit -s -m "chore: bump dependencies to $ENGINE_VERSION"
  ```

- [ ] Push to `dagger/dagger` - we need access to secrets that PRs coming from forks will not have. Open the PR as a draft and capture the PR number:

  ```console
  git push $DAGGER_REPO_REMOTE prep-$ENGINE_VERSION
  gh pr create --draft --title "chore: prep for $ENGINE_VERSION" --body "" | tee /tmp/prep-pr.txt
  export RELEASE_PREP_PR=$(cat /tmp/prep-pr.txt | sed -r 's/^[^0-9]*([0-9]+).*/\1/')
  ```

- [ ] Generate bump changes for each SDK + the helm charts

  ```console
  export GITHUB_USERNAME=$(gh api /user --jq .login)
  find sdk/go sdk/python sdk/typescript sdk/elixir sdk/php sdk/rust helm/dagger -maxdepth 1 -name .changie.yaml -execdir \
        changie new --kind "Dependencies" --body "Bump Engine to $ENGINE_VERSION" --custom PR="$RELEASE_PREP_PR" --custom Author="$GITHUB_USERNAME" \;
  ```

- [ ] Generate release notes `.changes/**/v0.12.4.md` for all releases:

  ```console
  find . sdk/go sdk/python sdk/typescript sdk/elixir sdk/php sdk/rust helm/dagger -maxdepth 1 -name .changie.yaml -execdir changie batch $ENGINE_VERSION \;
  ```

- [ ] Make any necessary edits to the newly generated file, e.g. `.changes/v0.12.4.md`
- [ ] Update `CHANGELOG.md` by running `changie merge`:

  ```console
  find . sdk/go sdk/python sdk/typescript sdk/elixir sdk/php sdk/rust helm/dagger -maxdepth 1 -name .changie.yaml -execdir changie merge \;
  find . -name .changes -type d -exec git add {} \;
  find . -name CHANGELOG.md -type f -exec git add {} \;
  git commit -s -m "chore: add release notes for $ENGINE_VERSION"
  ```

- [ ] Update `.changes/.next` with the next release number if known and commit it -
      otherwise, make the file empty (but don't remove it).

- [ ] Push changes, and bring the prep PR out of draft:

  ```console
  git push $DAGGER_REPO_REMOTE prep-$ENGINE_VERSION
  gh pr ready
  ```

- [ ] Ensure that all GitHub Actions checks pass for the dagger.io PR which
      gets automatically created part of this PR. The PR is configured to deploy a
      Daggerverse preview environment with a `main` Dagger Engine (the one that is
      just about to be released). If all checks pass, close that PR & delete the
      branch (this will clean up the infra that gets provisioned). If checks fail, cc
      @marcosnils @matipan in the release thread and wait for a
      response before continuing with the release (this might be a blocker).

- [ ] If everything above is green, review and merge the prep PR. The merged commit is what gets tagged in the next step.

- [ ] Confirm that all checks on `$RELEASE_BRANCH` are green, for main you're basically [checking](https://github.com/dagger/dagger/commits/main/) that the merged prep commit is has a green check.
      Do not push tags until this is finished.

## 🚀 Release

- [ ] When you have confirmed that all checks on `$RELEASE_BRANCH` are green, pull the latest changes, and push the tag to trigger the release workflow by running:

  ```console
  git checkout "$RELEASE_BRANCH"
  git pull "$DAGGER_REPO_REMOTE" "$RELEASE_BRANCH"

  export ENGINE_GIT_SHA="$(git rev-parse --verify HEAD)"
  git tag "$ENGINE_VERSION" "$ENGINE_GIT_SHA"
  git push "$DAGGER_REPO_REMOTE" "$ENGINE_VERSION"
  ```

  This will kick off [`.github/workflows/publish.yml`](https://github.com/dagger/dagger/actions/workflows/publish.yml) which publishes:
  - A new image to [ghcr.io/dagger/engine](https://github.com/dagger/dagger/pkgs/container/engine) (mirrored to registry.dagger.io/engine using https://github.com/dagger/registry-redirect).
  - Go packages to [🐙 dagger.io/dagger](https://pkg.go.dev/dagger.io/dagger) via [github.com/dagger/dagger-go-sdk](https://github.com/dagger/dagger-go-sdk/tags).
  - Python packages to [🐍 dagger-io](https://pypi.org/project/dagger-io).
  - Typescript packages to [⬢ npmjs.com/package/@dagger.io/dagger](https://www.npmjs.com/package/@dagger.io/dagger).
  - Elixir packages to [🧪 hex.pm/packages/dagger](https://hex.pm/packages/dagger).
  - Rust crates to [⚙️ crates.io/crate/dagger-sdk](https://crates.io/crates/dagger-sdk).
  - PHP packages to [🐘 packagist.org/packages/dagger/dagger](https://packagist.org/packages/dagger/dagger) via [github.com/dagger/dagger-php-sdk](https://github.com/dagger/dagger-php-sdk/tags).
  - Helm charts to [☸️ registry.dagger.io/dagger-helm](https://github.com/dagger/dagger/pkgs/container/dagger-helm).

This will also kick off [`.github/workflows/evals.yml`], which is currently broken at the moment.

- [ ] Double-check the engine+cli release:

  ```console
  # install the cli to dagger-<version>, and symlink dagger to it
  export BIN_DIR=$HOME/.local/bin
  curl -fsSL https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=$ENGINE_VERSION sh
  mv $BIN_DIR/dagger{,-$ENGINE_VERSION}
  ln -s $BIN_DIR/dagger{-$ENGINE_VERSION,}

  dagger version
  dagger core version
  ```

- [ ] Double-check that all the above packages have been correctly published
      and updated to their latest versions.
      _Note_: the [Go package](https://pkg.go.dev/dagger.io/dagger) may not be instantly updated due to caching.

- [ ] Double-check that git tags + github releases have been made for each component.

## 🌌 Daggerverse && Dagger Cloud

- [ ] Ask @marcosnils @matipan @sipsma on the release thread to review and merge the newly opened dagger.io PR (this is created by the publish workflow). This PR updates both the Daggerverse and Dagger Cloud. If anything fails, cc @kpenfound @matipan.

## Improve releasing 改善 (Post release steps)

- [ ] Start an release improvements branch:

  ```console
  git checkout -b improve-releasing-during-$ENGINE_VERSION
  ```

- [ ] Download and install the latest release, and continue the rest of the
      release process using the just-released CLI. This is needed now so the
      `.dagger` module updated below will get `dagger.json`'s engine version bumped.

- [ ] Update the dagger version in `.github/main.go`, then regenerate
      `.github/` configs:

  ```console
  dagger call -m .github generate
  ```

- [ ] Update non-autogenerated `.github` configs to `$ENGINE_VERSION`
  - The version numbers (of the form `<major>.<minor>.<patch>`) should be updated to the new version
  - The worker runner versions (of the form `dagger-v<major>-<minor>-<patch>-<worker>`)
  - e.g. if bumping 0.19.1->0.19.2, can run:

```console
find .github/ -type f -exec sed -i 's/0-19-1/0-19-2/g; s/0\.19\.1/0\.19\.2/g' {} +
```

- [ ] Bump the Go SDK version in our internal CI targets (these aren't actually
      used anywhere since we use the modularized go SDK - but it's good
      practice regardless).

  ```console
  # update deps
  go mod edit -require dagger.io/dagger@$ENGINE_VERSION
  go mod edit -require github.com/dagger/dagger/engine/distconsts@$ENGINE_VERSION
  go mod tidy
  cd .dagger
  go mod edit -require github.com/dagger/dagger/engine/distconsts@$ENGINE_VERSION
  go mod tidy
  cd ..

  dagger develop --recursive -m .
  dagger develop --recursive -m ./releaser

  # add, commit and push the changes to the branch
  git add .
  git commit -s -m "chore: bump internal tooling to $ENGINE_VERSION"
  git push
  ```

- [ ] Open a PR with the title `Improve Releasing during $ENGINE_VERSION`

  ```console
  git add .  # or any other files changed during the last few steps
  git commit -s -m "Improve releasing during $ENGINE_VERSION"
  git push "$DAGGER_REPO_REMOTE" "improve-releasing-during-$ENGINE_VERSION"
  gh pr create --draft --title "Improve releasing during $ENGINE_VERSION" --body ""
  ```

- [ ] When all the above done, remember to add the `RELEASING.md` changes to
      the `improve-releasing-during-v...` PR that you have opened earlier (remember
      to toggle all the checkboxes back to `[ ]`). Here is an example:
      https://github.com/dagger/dagger/pull/5658

## Last step

- [ ] Close the Discord release thread you created in [Let the team know](#let-the-team-know)
- [ ] Close the GitHub milestone you created in [Let the team know](#let-the-team-know)
  - If there are remaining PRs/issues that were not resolved, then move
    them into the next milestone (or remove it from a milestone entirely)
