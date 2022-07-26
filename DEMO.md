# User Demo

This is a demo meant for external users. They are assumed to have general knowledge of what Dagger is, but aren't necessarily experts.

## 0. Setup

1. Ensure `dagger-buildkitd` is running (quickly invoke dagger if needed)
1. Ensure that packages are already cached so that you don't have to wait a long time at the first command for various yarn packages to download
   - TODO: add a command for doing this (`cloak import -f dagger.yaml`?)
1. Export `NETLIFY_AUTH_TOKEN` env

## 1. Background

1. Previously, Dagger users implemented and orchestrated actions using CUE.
1. With Multi-lang, it's now possible to implement and orchestrate actions using a number of general-purpose programming languages such as Typescript and Go. This enables:
   - Using Dagger w/ languages you are more familiar with than CUE
   - Using libraries from other languages to implement actions (as opposed to creating shell wrappers)
   - Much more...
1. Additionally, actions written in one language are available to be called from any other language. If you want to write your action in Typescript, but someone else has written an action in Go that you'd like to use, you can import and call it seamlessly.

## 2. TODOApp Deploy

1. For the demo, let's say we have a website we'd like to deploy, the TODOApp.
1. We are using Dagger to setup some basic CI steps: build, test and deploy. Deploy, specifically, should ensure that the app has been built+tested successfully prior to deployment.
1. Let's start by running the deploy action and then dive into what actually happened.

Run:

```console
go run cmd/cloak/main.go -f examples/todoapp/dagger.yaml -q examples/todoapp/operations.graphql --op Deploy --local-dir src=examples/todoapp/app --secret token="$NETLIFY_AUTH_TOKEN"
```

1. Click on the output URL, show the TODOApp.

## 3. Action Implementations

1. The command above executed an action from the TODOApp package.
1. Open up `examples/todoapp/index.ts` and `examples/todoapp/dagger.graphql` and `examples/todoapp/operations.graphql`
1. Show the action implementations, explain mapping of args in typescript to `dagger.graphql`.

### 3.1 Yarn Action

1. Mention that todoapp is just calling out to the separate `yarn` action in build+test, now going to go look at that one
1. Open up `examples/yarn/index.ts` and `examples/yarn/dagger.graphql`
1. Go over how it is calling out to the `yarn` CLI via core.Exec.
   - `core` is a built-in package, always available to any action.

### 3.2 Alpine Action

1. Note that `yarn` calls out to `alpine` to create an image with the `yarn` cli present.
1. Open up `examples/alpine/alpine.go` and `examples/alpine/dagger.graphql`
1. Note that this action is implemented in Go.
   - However, the same schema format is used to define it as other actions we've seen. This enables it to be called from Typescript as easily as any other language.
1. Briefly show how Alpine action works; just uses `core` actions to install the provided packages.

### 3.3 Netlify Action

1. Go back to `examples/todoapp/index.ts`
1. Show how the deploy action calls build and test in parallel with one another before moving onto the actual deploy.
1. Note that it calls out to the `netlify` action to do the deployment
1. Open up `examples/netlify/index.ts`
1. Note that in this case, rather than calling out to `core.Exec`, we are running code directly in the action, with a combination of client libraries and shelling out to the netlify CLI when necessary

## 4. Modifying an Action

1. Now we're going to try a simple modification to the TODOApp actions.
1. Go back to `examples/todoapp/index.ts`
1. Change the site name constant to something different.
1. Run same command as before:

```console
go run cmd/cloak/main.go -f examples/todoapp/dagger.yaml -q examples/todoapp/operations.graphql --op Deploy --local-dir src=examples/todoapp/app --secret token="$NETLIFY_AUTH_TOKEN"
```

1. Click on the output URL, show the TODOApp at the new URL.

TODO: this is kind of lame, immediate question will likely be "can I make the siteName an input parameter". Should think of something better than this.

## 5. Creating a New Action

TODO: not ready to be part of a demo for a user quite yet (DX is still too rough, too much copy-pasting schemas, need a bit more automation)
Dream goal is something like:

1. User starts with empty directory. Creates `dagger.graphql` either from scratch or from a template.
1. User fills out actions they'd like to implement in `dagger.graphql` in addition to declaring which other actions they want to use in their implementation (aka their dependencies)
   - (This assumes we have migrated `dagger.yaml` to be directives in `dagger.graphql`)
   - Declaring dependencies could be optional too, can have whole universe imported by default if not declared or similar DX sugar.
1. User runs a command that reads `dagger.graphql` and outputs to a local directory all dependency schemas, client stubs for calling the dependencies and implementation stubs that the user can fill out to implement their action.
1. User fills in implementations.
1. User can now call their action with the CLI interface (or from an embedded SDK for more advanced use cases)
