---
sidebar_position: 1
slug: /
sidebar_label: What is Dagger?
---

# What is Dagger?

Dagger integrates your existing build, CI, infrastructure management and deployment tools into a streamlined delivery platform, also known as "PaaS".

What if you didn't have to choose between the slick deployment experience of Heroku, and the ability to customize every aspect of your stack? Using Dagger, you get the best of both worlds: a programmable backend that can orchestrate even the most complex delivery logic; and a simple, standardized frontend that every developer can use without being a devops expert.

![Dagger_Website_Ship](https://user-images.githubusercontent.com/216487/122216381-328a3500-ce61-11eb-907f-d2b6f66b3b10.png)

## Programmable backend

Under the hood, Dagger is not just customizable but fully programmable, so as your application grows and evolves, your delivery logic can evolve along with it.

Key features:

* Configure your integrations declaratively, with the revolutionary [CUE](https://cuelang.org) language.
* Trivially load and integrate any JSON and YAML configurations, with schema validation, templating, and more
* A growing catalog of ready-to-use integrations: Kubernetes, Terraform, AWS Cloudformation, Google Cloud Run, Docker Compose, Netlify, Yarn, Maven, and more
* First-class composition: compose the nodes in your graph just like you would functions in regular code - with all the benefits of a declarative language.
* Develop your own integrations in minutes, with a powerful pipeline API powered by [https://github.com/moby/buildkit]. Run containers, fetch data sources, generate artifacts on-demand, securely load secrets, and more.
* Built-in support for encrypted secrets.
* Built-in support for just-in-time artifacts.
* Gitops-ready.

## Simple, standardized frontend

No matter how custom your delivery backend, developers can ignore the complexity and deploy with one simple command:

```shell
dagger up
```

This makes developers more productive, because they don't have to learn a new workflow every time their deployment
system changes. It also frees the delivery team to make more ambitious and rapid changes, without fearing that they will slow down or break delivery.

## Dagger is alpha software

Warning! Dagger is _alpha-quality software_. It has many bugs, the user interface is minimal, and it may change in incompatible ways at any time. If you are still
willing to try it, thank you! We appreciate your help and encourage you to [ask
questions](https://github.com/dagger/dagger/discussions) and [report issues](https://github.com/dagger/dagger/issues).
