---
slug: /
---

# What is Dagger?

Dagger is a universal deployment engine. Write your deployment logic once, run it securely from anywhere.

Key features:

* Escape YAML hell. Thanks to the Cue language, writing configuration is as fun and productive as writing code.
* A powerful declarative execution engine to automate even the most complex and custom logic
* Static type checking so you can catch configuration errors before running that huge deployment
* Built-in caching and parallelism for maximum scale and performance
* An ecosystem of reusable packages to save you time and share your expertise with the community
* Avoid CI lock-in: the same Dagger configuration can be used in any CI, or with no CI at all
* Rapid local development: test and run everything locally, in seconds.
* Deploy from CI or the laptop: no more duplicating your CI config in a Makefile just so you can perform a task locally.
* Native support for encrypted secrets
* Just-in-time artifacts: fetch, transform and produce any artifact on the fly: source repositories, container images, binaries, database exports, ML models...

Using Dagger, teams with different deployment workflows can more easily collaborate and deploy each other's software,
without being forced to change their tools.

Typical use cases:

* On-demand staging environments for reviewing code changes
* Manage CICD across multiple repositories and CI runners, with one unified configuration
* Iterate on infrastructure without disrupting development teams
* Lock down access to the production cluster so that only authorized configurations are applied
* Common ground between the PaaS, Kubernetes and Serverless siloes.
* On-demand integration environments for testing complex changes spanning several teams

![Dagger_Website_Ship](https://user-images.githubusercontent.com/216487/122216381-328a3500-ce61-11eb-907f-d2b6f66b3b10.png)

## Dagger is alpha software

Warning! Dagger is _alpha-quality software_. It has many bugs, the user interface is minimal, and it may change in incompatible ways at any time. If you are still
willing to try it, thank you! We appreciate your help and encourage you to [ask
questions](https://github.com/dagger/dagger/discussions) and [report issues](https://github.com/dagger/dagger/issues).
