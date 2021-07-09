---
sidebar_position: 1
slug: /
sidebar_label: What is Dagger?
---

# What is Dagger?

<<<<<<< HEAD
Dagger is a modular application delivery platform. It automates the delivery of applications to the cloud, by integrating your existing tools and infrastructure instead
of replacing them.

What if you didn't have to choose between the simplicity of Heroku and the flexibility of a custom stack?
With Dagger, you get the best of both worlds: a programmable backend that adapts to your existing stack; and a simple frontend
that standardizes the deployment experience.
=======
Dagger integrates your existing build, CI, infrastructure management and deployment tools into a streamlined delivery platform, also known as "PaaS".

What if you didn't have to choose between the slick deployment experience of Heroku, and the ability to customize every aspect of your stack? Using Dagger, you get the best of both worlds: a programmable backend that can orchestrate even the most complex delivery logic; and a simple, standardized frontend that every developer can use without being a devops expert.
>>>>>>> 57e0c11... Ported to os.#Container and added test cases

![Dagger_Website_Ship](https://user-images.githubusercontent.com/216487/122216381-328a3500-ce61-11eb-907f-d2b6f66b3b10.png)

## Programmable backend

<<<<<<< HEAD
Dagger works by integrating all your tools and infrastructure into a unified graph - a [DAG](https://en.wikipedia.org/wiki/Directed_acyclic_graph) to be precise.

Each node in your DAG represents an integration: for example a source repository, build script, artifact registry or deployment API. Each connection represents a flow of data between integrations: for example from source to build; from build to registry; etc.

What makes Dagger special is how much of your existing stack it can integrate in the DAG (probably all of it); how much
of your existing data flows it can manage (probably all of them); and how composable your DAG is (as much as regular software).

### Integrations

Each node in your DAG represents an integration. A crucial feature of Dagger is that it can integrate (almost) anything
into the DAG. The more components in your stack can be modeled in your DAG, the more useful the DAG.

A typical DAG may have the following integrations:

* Remote data sources (http/tar, git, OCI)
* Source control services: Git, Mercurial, SVN...
* Command-line tools: anything that can be run in a container
* Custom scripts: shell, python, ruby...
* Cloud APIs: AWS, Google Cloud, Azure...
* Infrastructure services: Kubernetes, Docker Swarm...
* CICD systems: Gitlab Runner, Github Actions...

### Data flows

Each connection in your DAG represents a data flow between integrations. A crucial feature of Dagger is that it
can orchestrate the flow of data between integrations *natively*, without requiring external infrastructure.

This means integrations can be trivially connected to exchange the following types of data:

* JSON-compatible values
* Encrypted secrets: passwords, API tokens, cryptographic keys...
* Artifacts: source repositories, container images, binaries, database exports, ML models...

### Composition

All Dagger integrations and data flows are configured with the revolutionary [CUE](https://cuelang.org) language.

This allows for first-class composition. In other words, you can develop your DAG like you would develop any other software, including:

* Publish and import reusable packages
* Encapsulate low-level components in higher-level components, at will
* Collaborate using industry-standard tools and workflows
* Testing, debugging, dependency injection, etc.

### Tying it all together

The combination of Dagger's 3 key features - integration, data flows and composition - allows for powerful patterns to emerge:

* Just-in-time artifacts. Since Dagger can receive source artifacts as input, run them through arbitrary integrations, and produce the final
outputs, you no longer have to worry about storing and managing intermediary artifacts: Dagger's data layer does it automatically for you. Artifacts are produced on demand, when they are needed, and automatically cached for later re-use.

* Gitops-ready. The state of your DAG is encoded as CUE files, and secrets are always encrypted. This you can always safely track your DAG
in source control, and intregate it in a gitops workflow.
=======
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
>>>>>>> 57e0c11... Ported to os.#Container and added test cases

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
