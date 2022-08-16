# User Demo

This is a demo meant for external users. They are assumed to have general knowledge of what Dagger is, but aren't necessarily experts.

## 0. Setup

1. Ensure `dagger-buildkitd` is running (quickly invoke dagger if needed)
2. Build `cloak` and make sure it's in your PATH
   - `go build ./cmd/cloak`
   - `ln -sf "$(pwd)/cloak" /usr/local/bin`
3. Ensure that packages are already cached so that you don't have to wait a long time at the first command for various yarn packages to download
4. Export `NETLIFY_AUTH_TOKEN` env

## 1. Background

1. Previously, Dagger users implemented and orchestrated actions using CUE.
1. With Multi-lang, it's now possible to implement and orchestrate actions using a number of general-purpose programming languages such as Typescript and Go. This enables:
   - Using Dagger w/ languages you are more familiar with than CUE
   - Using libraries from other languages to implement actions (as opposed to creating shell wrappers)
   - Much more...
1. Additionally, actions written in one language are available to be called from any other language. If you want to write your action in Typescript, but someone else has written an action in Go that you'd like to use, you can import and call it seamlessly. We don't want fractured ecosystems.
1. In order to achieve interop between languages, we need a common API. For that we are using GraphQL

## 2. Low-level GraphQL API

_Andrea runs through his demo w/ graphiql_

## 3. Action Implementations

1. Alpine action
   - show how it maps to query ran in last part of demo
1. Yarn action
   - in typescript, but calls alpine
   - uses raw graphql api
   - simple imperative steps
1. Netlify action
   - Imports a 3rd party library, wraps it with all the buildkit caching goodness
1. TODOapp
   - Previous examples were re-usable generic actions, this is what a user who wants to add some CI automation to their project might do
   - Build+Test just passthrough to yarn
   - Deploy does build+test first in parallel, then passes build output to netlify deploy

## 4. Invoking Cloak

```console
cloak -p examples/todoapp/go/cloak.yaml do Deploy --local-dir src=examples/todoapp/app --secret token="$NETLIFY_AUTH_TOKEN"
```

- `--local-dir` maps `src` arg to the local app dir
- `--secret` maps `token` arg to my env var

1. Show command running, mention that cached because it was run previously, show deployed website
1. Open up `examples/todoapp/app/src/components/Form.js`, modify `What needs to be done?` to `What needs to be done???????!!!!!!`
1. Re-run command, show that changes were picked up automatically.
