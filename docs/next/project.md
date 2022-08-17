# Anatomy of a Dagger project

## Overview

Dagger helps software teams automate their workflows, so that they can spend less time on repetitive tasks, and more on building.

Any software project can adopt Dagger by following these steps:

1. Add a `dagger.yaml` file to the project directory
2. Program one or more workflows, in a supported language
3. Run the workflow from the CLI or on Play With Dagger


## Workflows

Configuration key: `workflows`.

Workflows are simple programs which automate various parts of software delivery. A typical project may have workflows such as "build", "test", "deploy" or "lint".

Workflows are designed to be portable, familiar, simple,operator-friendly and safe.

* *Portable*: workflows are run in containers, so they can be used without modification in most development and CI environments.

* *Familiar*: writing a workflow does not require learning a new programming language: there are SDKs for Bash, Go and Typescript, with more planned.

* *Simple*: most of the heavy lifting is done by the Dagger API and its extensions. Workflows act as  specialized clients, orchestrating API calls in the backend and presenting a user interface in the frontend.

* *Operator-friendly*: just like regular tools, workflows can read files, lookup environment variables and execute commands on the operator's system. This makes them easy to integrate with minimal disruption.

* *Safe*: unlike regular tools, workflows are fully sandboxed by default. Each access to the operator's system must be explicitly requested and allowed.

### Writing a workflow

*FIXME*

### Running a workflow

Workflows can be run by the CLI with `dagger do NAME`.

In Play With Dagger, workflows are run by selecting the workflow, filling its parameters, and clicking "run".


## API extensions

The Dagger API is a graphql-compatible API for composing and running powerful pipelines with minimal effort.

Developers can write *API extensions* to add new capabilities to the Dagger API. Extensions may be private to a project, or imported as dependencies by other projects.

### Using an API extension from a workflow

A workflow may declare a dependency on API extensions, using the `dependencies` key. Note that each workflow must declare its own dependencies.

When a workflow is run, it simply queries the Dagger API in the usual way: all extension types are loaded and available to be queried.

### Writing an API extension

The same SDK can be used to write workflows and API extensions. See the documentation for your SDK of choice.

A few important differences between workflows and API extensions:

* Workflows may access the operator's system; API extensions may not. Extensions are fully sandboxed to make them as safely reusable as possible.

* Extensions may be used as a dependency; workflows may not. Workflows are meant for direct use by a human operator, and wrapping them in software is not recommended. If a workflow contains logic that you wish to share with another workflow, it may be time to split out that logic into an extension, and have both workflows query it.

## Project File examples

### Todo App (simple)

```yaml
workflows:
	build: 
		# Note: sdk is optional, auto-discovered if omitted
		source: build.sh
		dependencies:
			- yarn
		privileges:
			workdir: true
	deploy:
		source: ./workflows/deploy
		sdk: go
		sdk_settings:
			flags: -v
		privileges:
			workdir: true
			env:
				- NETLIFY_TOKEN
			commands:
				- aws
```

### Todo App (advanced)

In this version of Todo App, custom build and deployment logic has been moved into an extension.

* Note that workflows no longer have dependencies (a project's own extensions are always loaded)

* Note that the `deploy` workflow is now a shell script, since it's now much simpler and more people on the team are comfortable with bash than go.

```yaml
workflows:
	build: 
		source: build.sh
		privileges:
			workdir: true
	deploy:
		source: deploy.sh
		privileges:
			workdir: true
			env:
				- NETLIFY_TOKEN
			commands:
				- aws

extensions:
	-
		source: ./dagger/extensions
		# SDK also optional here
		sdk: go
		dependencies:
			- yarn
			- netlify
			- aws/s3
```

### Netlify extension

Here we imagine the source code of the "netlify" extension with some types implemented in Go, and others in Typescript (probably not a good idea).

Note that this project combines two extensions. If it is used as a dependency, both extensions will be included. 

```yaml
extensions:
	-
		source: ./ts
		sdk: typescript
	-
		source: ./go
		sdk: go
```
