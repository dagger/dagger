---
slug: /1002/vs/
---

import CautionBanner from '../\_caution-banner.md'

# Dagger vs. Other Software

<CautionBanner old="0.1" new="0.2" />

## Dagger vs. CI (GitHub Actions, GitLab, CircleCI, Jenkins, etc.)

Dagger does not replace your CI: it improves it by adding a portable development layer on top of it.

- Dagger runs on all major CI products. This _reduces CI lock-in_: you can change CI without rewriting all your pipelines.
- Dagger also runs on your dev machine. This allows _dev/CI parity_: the same pipelines can be used in CI and development.

## Dagger vs. PaaS (Heroku, Firebase, etc.)

Dagger is not a PaaS, but you can use it to add PaaS-like features to your CICD pipelines:

- A simple deployment abstraction for the developer
- A catalog of possible customizations, managed by the platform team
- On-demand staging or development environments

Using Dagger is a good way to get many of the benefits of a PaaS (developer productivity and peace of mind),
without giving up the benefits of custom CICD pipelines (full control over your infrastructure and tooling).

## Dagger vs. artisanal deploy scripts

Most applications have a custom deploy script that usually gets the job done, but is painful to change and troubleshoot.

Using Dagger, you have two options:

1. You can _replace_ your script with a DAG that is better in every way: more features, more reliable, faster, easier to read, improve, and debug.
2. You can _extend_ your script by wrapping it, as-is, into a DAG. This allows you to start using Dagger right away, and worry about rewrites later.

## Dagger vs. Infrastructure as Code (Terraform, Pulumi, Cloudformation, CDK)

Dagger is the perfect complement to an IaC tool.

- IaC tools help infrastructure teams answer questions like: what is the current state of my infrastructure? What is its desired state? And how do I get there?
- Dagger helps CICD teams answer question like: what work needs to be done to deliver my application, in what order, and how do I orchestrate it?

It is very common for a Dagger configuration to integrate with at least one IaC tool.

## Dagger vs. Build Systems (Make, Maven, Bazel, Npm/Yarn, Docker Build, etc.)

Dagger is complementary to build systems. Most Dagger configurations involve integrating with at least one specialized build.
If several build systems are involved, Dagger helps integrate them into a unified graph.
