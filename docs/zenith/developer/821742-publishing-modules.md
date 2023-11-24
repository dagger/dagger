---
slug: /zenith/developer/191108/publishing-modules
displayed_sidebar: "zenith"
authors: ["Alex Suraci"]
date: "2023-11-03"
---

# Publishing Modules

{@include: ../partials/_experimental.md}

## Introduction

This guide explains how to publish your modules to the [Daggerverse](https://daggerverse.dev), so that other users can easily discover and use them.

## Requirements

This guide assumes that:

- You have a Dagger module. If not, create a module using the [Go](./go/525021-quickstart.md) or [Python](./python/419481-quickstart.md) quickstarts.
- You have a GitHub account. If not, [register for a GitHub account](https://github.com/signup).

## Process

To publish a module to the Daggerverse, follow the steps below:

1. Create a Git repository for the module and push the code to GitHub (replace the `USERNAME` placeholder with your GitHub username):

  ```sh
  # assuming your module is in "potato/"
  git init
  git add potato/
  git commit -m "Initial commit"

  git remote add origin git@github.com:USERNAME/daggerverse.git
  git push origin main
  ```

  :::tip
  It isn't mandatory to use `daggerverse` as the name of your Git repository - it's just a handy way to have all your modules in one Git repository together. But you can always split them out into separate repositories, or use a different name.
  :::

1. Navigate to the [Daggerverse](https://daggerverse.dev) and click the `Publish` button in the top navigation bar. On the resulting page, paste the URL to the GitHub repository containing your module in the format `github.com/USERNAME/REPOSITORY[/SUBPATH][@VERSION]`.

  :::info
  When VERSION is provided the reference is "pinned", referring to a specific version of the module. A pinned ref is only pinned as firmly as the provided VERSION implies. For maximal stability it should point to a commit SHA or tag, but you are free to use a branch name if you like. When VERSION is omitted, the reference is "moving", referring to the latest commit on the default branch of the repository
  :::

1. Click "Publish" to have your module published to the Daggerverse. This process may take a few minutes. Once complete, your module will appear in the Daggerverse module listing.

## Limitations

- Currently, only URLs beginning with `github.com` are supported.
- The Daggerverse only fetches publicly available information from GitHub. Modules are not hosted on the Daggerverse.

:::tip
 If you need a module removed from the Daggerverse for some reason, let the Dagger team know in [Discord](https://discord.gg/dagger-io).
:::
