---
slug: /joatj/extension_publishing_guide
displayed_sidebar: '0.3'
---

# Extension Publishing Guide

## Overview

This document explains best practices for creating public extensions.

For information on writing the extension itself, visit the [Writing Extensions](bnzm7-writing_extensions.md) guide.

## The Extension Repository

Extensions should:
* Have their own git repo
* Have an Apache 2.0 license
* Have a dagger config at the repo root
* Designate releases with git tags (and ideally github releases)

## The README.md

* Give a high level overview of the purpose of the extension
* Give a basic example usage of the extension
* Provide a snippet to include the extension in your dagger project
* (Optional) Give more examples of common use cases
* (Optional) List new features as they're added

## Discoverability

To make your extension easily discoverable by the dagger community, add a link to it in [introduction](../unxpq-introduction.md).

## Get Started

Use the template repository [kpenfound/dagger-extension-template](https://github.com/kpenfound/dagger-extension-template). Click the "Use this template" button, and give your new repository a name! This template includes the basic best practices described above.
