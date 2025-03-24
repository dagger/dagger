# Releasing ![shields.io](https://img.shields.io/badge/Last%20updated%20on-October%2031,%202024-success?style=flat-square)

This describes how to release Dagger:

- [📝 Preparation ⏱ `30mins`](#-preparation--30mins)
- [🚀 Release ⏱ `10mins`](#-release--10mins)
- [🌌 Daggerverse ⏱ `2mins`](#-daggerverse--2mins)
- [🌥️ Dagger Cloud ⏱ `2mins`](#-dagger-cloud--2mins)
- [🪣 Install scripts ⏱ `2mins`](#-install-scripts--2mins)
- [🐙 dagger-for-github ⏱ `2mins`](#-dagger-for-github--2mins)
- [🍺 dagger Homebrew ⏱ `2mins`](#-dagger-homebrew--2mins)
- [❄️ nix ⏱ `2mins`](#-nix--2mins)
- [⚙️ Improvements ⏱ `2mins`](#-improvements--2mins)

## Let the team know

Before you go ahead and produce a new release, remember that it's a team
effort. The first step is to let the team know what is going to happen,
preferably a few days in advance so that they can react. We do our release
threads in public wherever possible, to give the community more visibility, and
to help external contributors to more easily keep in sync with the release
cadence.

To let the team know:

- [ ] Create a new milestone in [GitHub](https://github.com/dagger/dagger/milestones),
      e.g. [`v0.11.5 Milestone`](https://github.com/dagger/dagger/milestone/47)
- [ ] Create a new post in [Discord #engine-dev](https://discord.com/channels/707636530424053791/1003718839739105300),
      e.g. [`v0.11.5 release - 28th May 2024`](https://discord.com/channels/707636530424053791/1240714480275689574)

This allows others to weigh in whether:

- we should go for a patch / minor bump
- there are any PRs that people are waiting to get merged
- any big features which need to remain experimental?
- etc.

Maybe there are breaking changes which we should be aware of and message
accordingly. Giving other team members a day or two to react - because
timezones! - will make this entire process smoother.

Most importantly, patch vs minor is **not** a technical decision. If you want
to read more about this, see [this (private) Discord
thread](https://discord.com/channels/707636530424053791/1101242942267601038/1101508879671623780).

- [ ] If doing a minor release, determine if there are any deprecations that
      can be removed.

> [!NOTE]
>
> Once you know what type of release we are producing - patch vs minor -
> remember to edit the `?` in the Discord thread.

### Backwards compatibility

Where possible, we try to ensure backwards compatibility between mismatched cli
and engine versions. However, for technical reasons, this isn't always possible:
sometime the communication protocol changes, or a bug fix or new feature
requires changes on both the CLI and the engine.

Before releasing, make sure to sanity check the backwards compatibility of a
release. If you enounter issues, then:

- [ ] Add a release note using `changie new` (or add it later manually).
- [ ] Bump the minimum version numbers in [engine/version.go](https://github.com/dagger/dagger/blob/main/engine/version.go).

If unsure, bump both the client and engine minimum version numbers, but if
the backwards compatibility is only an issue in one direction, you only need
to bump that one.

## Improve this doc while releasing 改善

In order to keep this relevant & accurate, we improve this doc during the
release process. It's the best time to pause, observe how it all fits together,
and improve it. We want small, constant improvements which compound. Therefore:

- [ ] Save a copy of this doc outside of this repository (e.g.
      `~/Downloads/RELEASING.md`). Now open that copy in your editor and start
      ticking items off it as you make progress. Remember to add / remove / edit
      any parts which could be improved. As inspiration, [see what a past PR with
      improvements looks like](https://github.com/dagger/dagger/pull/5056).
- [ ] Update the date in the shields.io badge, first line in this file.

> [!NOTE]
>
> We believe in documentation first, automation second. Documenting a process
> forces us to understand it well. Continuously editing this documentation
> refines our understanding. Once we have a good grasp of the problem space,
> and reach an elegant solution, it comes natural to automate & speed things
> up, to make the process more efficient. We should still be able to perform
> things manually if we need to - because sometimes automation fails 🤷. This
> is when everyone wishes they had good documentation, the original author(s)
> or both! It's also worth mentioning that when it's time to improve this
> automation, we want to be looking at the blueprint - this doc right here -
> not the implementation. If you ever had to migrate from Chef/Puppet to
> Ansible/Terraform, you know what it was like to migrate the implementation.

## Workflow for releases off non-main branch

Currently, some of the steps here vary depending on whether we are running a
release off of `main` vs. off of a separate branch.

They are marked with 🚨 in the steps below.

We do this process when we need to do a patch release but don't want to include
every commit that has been merged to `main` since the last release.

In the long term we should strive to make the process the same regardless of
the branch we are releasing off of, but for now, we need to be aware of the
differences.

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

## 📝 Preparation ⏱ `30mins`

Setup some variables used throughout the release process:

```console
# 🚨 change this from `main` to `release-vX.Y.Z` if releasing off a non-main branch
export RELEASE_BRANCH=main

# set to whatever github.com/dagger/dagger repo is named for you locally
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

<details>
<summary>🚨 Non-main branch release only:</summary>

Create a new branch off of the previous git tag being patched named `release-vX.Y.Z` (e.g. `release-v0.11.9`)

```console
git switch -c $RELEASE_BRANCH

# e.g. if creating a new v0.11.9 release off of v0.11.8, use v0.11.8 here
git reset --hard <previous tag being patched>

git push --set-upstream $DAGGER_REPO_REMOTE "$RELEASE_BRANCH"
```

</details>

Setup the local branch to align with the remote branch being released

```console
git checkout "$RELEASE_BRANCH"

git pull $DAGGER_REPO_REMOTE "$RELEASE_BRANCH"
```

<details>
<summary>🚨 Non-main branch release only:</summary>

`git cherry-pick <commit-hash>` the commits you want to include in the release from `main` to the release branch.

You will also want to ensure you _always_ cherry-pick a few special commits:

- [ ] The SDK bump PR for the release being patched.
  - e.g. if we are creating a `v0.11.9` release based off of `v0.11.8`, you want to ensure you cherry-pick [this PR](https://github.com/dagger/dagger/pull/7690).
- [ ] The PR that bumps CI to use the new Engine version.
  - e.g. if we are creating a `v0.11.9` release based off of `v0.11.8`, you want to ensure you cherry-pick [this PR](https://github.com/dagger/dagger/pull/7702) that upgrades CI to use `v0.11.8` engines.

</details>

- [ ] Ensure that `.changes/.next` contains `$ENGINE_VERSION` - if it doesn't,
      update it now!

- [ ] Determine the next release version (use `patch`/`minor`/`major` to set the release type):

```console
export ENGINE_VERSION="$(dagger call -m version next-release-version)"

# this is required to interpolate $ENGINE_VERSION to the SDK release notes
export CHANGIE_ENGINE_VERSION="$ENGINE_VERSION"
```

- [ ] Create the target release notes branch for a PR - e.g. `prep-v0.12.4`.
  - 🚨 Non-main branch release only: This PR will also include the cherry-picked commits mentioned above.

```console
git checkout -b prep-$ENGINE_VERSION
```

- [ ] Bump internal versions (sdks + docs + helm chart) to the target version

```console
dagger call -m releaser bump --version="$ENGINE_VERSION" -o ./
git add docs sdk helm
git commit -s -m "chore: bump dependencies to $ENGINE_VERSION"
```

- [ ] Push to `dagger/dagger` - we need access to secrets that PRs coming from forks will not have. Open the PR as a draft and capture the PR number:

```console
git push $DAGGER_REPO_REMOTE
gh pr create --draft --title "chore: prep for $ENGINE_VERSION" --body "" | tee /tmp/prep-pr.txt
export RELEASE_PREP_PR=$(cat /tmp/prep-pr.txt | sed -r 's/^[^0-9]*([0-9]+).*/\1/')
```

- [ ] Generate bump changes for each SDK + the helm charts

```console
export GITHUB_USERNAME=$(gh api /user --jq .login)
find sdk/go sdk/python sdk/typescript sdk/elixir sdk/php sdk/rust helm/dagger -maxdepth 1 -name .changie.yaml -execdir \
      changie new --kind "Dependencies" --body "Bump Engine to $ENGINE_VERSION" --custom PR="$RELEASE_PREP_PR" --custom Author="$GITHUB_USERNAME" \;
```

- [ ] Generate release notes `.changes/**/v0.12.4.md` for all releases by
      running `changie batch $ENGINE_VERSION`:

```console
find . sdk/go sdk/python sdk/typescript sdk/elixir sdk/php sdk/rust helm/dagger -maxdepth 1 -name .changie.yaml -execdir changie batch $ENGINE_VERSION \;
```

- [ ] Make any necessary edits to the newly generated file, e.g. `.changes/v0.12.4.md`
- [ ] Update `CHANGELOG.md` by running `changie merge`.

```console
find . sdk/go sdk/python sdk/typescript sdk/elixir sdk/php sdk/rust helm/dagger -maxdepth 1 -name .changie.yaml -execdir changie merge \;
git add **/.changes
git add **/CHANGELOG.md
git commit -s -m "chore: add release notes for $ENGINE_VERSION"
```

- [ ] Update `.changes/.next` with the next release number if known -
      otherwise, make the file empty (but don't remove it).

- [ ] Push changes, and bring the prep PR out of draft:

```console
git push
gh pr ready
```

- [ ] `30 mins` Review and merge the prep PR. The merged commit is what gets tagged in the next step.
  - 🚨 Non-main branch release only: Ideally use "Rebase and Merge" rather than squashing commits when merging so we can more easily preserve the history of the cherry-picked commits.

- [ ] Ensure that all GitHub Actions checks pass for the dagger.io PR which
      gets automatically created part of this PR. The PR is configured to deploy a
      Daggerverse preview environment with a `main` Dagger Engine (the one that is
      just about to be released). If all checks pass, close that PR & delete the
      branch (this will clean up the infra that gets provisioned). If checks fail, cc
      @jpadams @marcosnils @matipan @gerhard in the release thread and wait for a
      response before continuing with the release (this might be a blocker).

- [ ] `15 mins` Confirm that all checks on `$RELEASE_BRANCH` are green, for main you're basically [checking](https://github.com/dagger/dagger/commits/main/) that the merged prep commit is has a green check.
      Do not push tags until this is finished.

## 🚀 Release ⏱ `10mins`

- [ ] When you have confirmed that all checks on `$RELEASE_BRANCH` are green, run the following:

```console
git checkout "$RELEASE_BRANCH"
git pull "$DAGGER_REPO_REMOTE" "$RELEASE_BRANCH"

export ENGINE_GIT_SHA="$(git rev-parse --verify HEAD)"
git tag "$ENGINE_VERSION" "$ENGINE_GIT_SHA"
git push "$DAGGER_REPO_REMOTE" "$ENGINE_VERSION"
```

This will kick off [`.github/workflows/publish.yml`](https://github.com/dagger/dagger/actions/workflows/publish.yml) which publishes:

- A new image to [ghcr.io/dagger/engine](https://github.com/dagger/dagger/pkgs/container/engine) (mirrored to registry.dagger.io/engine using https://github.com/dagger/registry-redirect).
- New cli binaries to [dl.dagger.io](https://dl.dagger.io) (served from an S3 bucket, uploaded to by goreleaser)
- New docs to [docs.dagger.io](https://docs.dagger.io) (served from netlify)
- Go packages to [🐙 dagger.io/dagger](https://pkg.go.dev/dagger.io/dagger) via [github.com/dagger/dagger-go-sdk](https://github.com/dagger/dagger-go-sdk/tags).
- Python packages to [🐍 dagger-io](https://pypi.org/project/dagger-io).
- Typescript packages to [⬢ npmjs.com/package/@dagger.io/dagger](https://www.npmjs.com/package/@dagger.io/dagger).
- Elixir packages to [🧪 hex.pm/packages/dagger](https://hex.pm/packages/dagger).
- Rust crates to [⚙️ crates.io/crate/dagger-sdk](https://crates.io/crates/dagger-sdk).
- PHP packages to [🐘 packagist.org/packages/dagger/dagger](https://packagist.org/packages/dagger/dagger) via [github.com/dagger/dagger-php-sdk](https://github.com/dagger/dagger-php-sdk/tags).
- Helm charts to [☸️ registry.dagger.io/dagger-helm](https://github.com/dagger/dagger/pkgs/container/dagger-helm).

Finally:

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

- [ ] Double-check that git tags + github releases have been made for each component.

## 🚨 Non-main branch release only

After the Engine+SDKs are all released from the `release-vX.Y.Z` branch, you will need to "forward-port" a few of the commits there up to `main`. Namely:

- The commits from the release notes PR, e.g. when patching `v0.11.8` to `v0.11.9`, [this commit](https://github.com/dagger/dagger/commit/18fd28cfa8f2e5d5f2ccc58fb15a4a975a3660dd)
- The commits from the SDK engine version bump PR, e.g. when patching `v0.11.8` to `v0.11.9`, the commits from [this PR](https://github.com/dagger/dagger/pull/7746)
- The commits from the CI engine version bump PR, e.g. when patching `v0.11.8` to `v0.11.9`, [this commit](https://github.com/dagger/dagger/commit/58b1865969426357f7c38f2052a93789bd324534)

One easy way to do this is to re-use the engine version bump PR that was created against `main` earlier, cherry-picking in the missing commits.

Be sure to use "Rebase and Merge" when merging the PR to `main` to preserve the history of the cherry-picked commits.

- [Example of this here for `v0.11.9`](https://github.com/dagger/dagger/pull/7745)

## 🌌 Daggerverse ⏱ `2mins`

- [ ] Merge the newly opened PR in the dagger.io repository (this is created by
      the publish workflow). If anything fails, cc the following in the release thread
      on Discord: cc @jpadams @kpenfound @matipan @gerhard

## 🌥️ Dagger Cloud ⏱ `2mins`

- [ ] Mention in the release thread on Discord that Dagger Cloud can be updated
      to the just-released version. cc @marcosnils @matipan @sipsma

## 🐙 dagger-for-github ⏱ `2mins`

The action's `README.md` and `action.yml` should be relatively evergreen now, so we don't need to bump with Dagger releases anymore, just if there is a fix to `dagger-for-github`. We'll use `8.0.0` style versions for the action going forward to comply with GitHub immutable actions standards. Should not need separate `v8` tag. Should automatically get `v8`, `v8.0`, `v8.0.0` as well as `8.0.0` resolved by GitHub.

- [ ] Take a peek at the action or mention it in the release thread on Discord if there are updates to reflect in the docs. Not tied to the Dagger release itself outside of docs. cc @jpadams @kpenfound @vikram-dagger @jasonmccallister

- [ ] Create/push a new patch tag for fixes/changes to `dagger-for-github`

```console
# Find the latest released patch https://github.com/dagger/dagger-for-github/releases
# or via the `gh` CLI. Use that to figure out the NEXT_PATCH_VERSION.
gh release view --repo dagger/dagger-for-github --json tagName,publishedAt

# Sign the tag, using the date as the comment, e.g. 2024-07-22
git tag --sign -m $(date '+%Y-%m-%d') <NEXT_PATCH_VERSION>
git push origin <NEXT_PATCH_VERSION> #shouldn't need to force since new tag
```

- [ ] Create a new release from the patch tag (auto-fill release notes via the
      GitHub UI or via the `gh` CLI)
- [ ] Submit PR to change the version mentioned in Dagger docs. See example [here](https://github.com/dagger/dagger/pull/9705/files)

```console
# --verify-tag will ensure the last tag creation step was done
gh release create --generate-notes --verify-tag <NEXT_PATCH_VERSION>
```

## Improve releasing 改善

🚨 Non-main branch release only: you'll likely want the changes from this PR in both `$RELEASE_BRANCH` and `main`.

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
dagger call -m .github generate directory --path=.github/workflows export --path=.github/workflows --wipe
```

- [ ] Update non-autogenerated `.github` configs to `$ENGINE_VERSION`
  - The version numbers (of the form `<major>.<minor>.<patch>`) should be updated to the new version
  - The worker runner versions (of the form `dagger-v<major>-<minor>-<patch>-<worker>`)
  - e.g. if bumping 0.12.6->0.12.7, can run `find .github/ -type f -exec sed -i 's/0-12-6/0-12-7/g; s/0\.12\.6/0\.12\.7/g' {} +`

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

# run recursive dagger develop
dagger develop
dagger call with-mod-codegen source -o .

# add, commit and push the changes to the PR
git add .
git commit -s -m "chore: bump internal tooling to $ENGINE_VERSION"
git push
```

- [ ] Open a PR with the title `Improve Releasing during $ENGINE_VERSION`

```console
git add .  # or any other files changed during the last few steps
git commit -s -m "Improve releasing during $ENGINE_VERSION"
git push
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
