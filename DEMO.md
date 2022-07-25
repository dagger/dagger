# User Demo

This is a demo meant for external users. They are assumed to have general knowledge of what Dagger is, but aren't necessarily experts.

## 0. Setup

1. Ensure `dagger-buildkitd` is running (quickly invoke dagger if needed)
1. Ensure that packages are already cached so that you don't have to wait a long time at the first command for various yarn packages to download
   - TODO: add a command for doing this (`cloak import -f dagger.yaml`?)

## 1. TODOApp Deploy

Run:

```console
go run cmd/cloak/main.go -f examples/todoapp/dagger.yaml -local-dirs src=examples/todoapp/app -secrets token="$NETLIFY_AUTH_TOKEN" <<'EOF'
query Build($local_src: FS!, $secret_token: String!) {
    todoapp{deploy(src: $local_src, token: $secret_token){url}}
}
EOF
```

1. Click on the output URL, show the TODOApp.

## 2. Action Implementations

1. Open up `examples/todoapp/index.ts` and `examples/todoapp/dagger.graphql`
1. Show the action implementations, explain mapping of args in typescript to `dagger.graphql`.

### 2.1 Yarn Action

1. Mention that todoapp is just calling out to the separate `yarn` action in build+test, now going to go look at that one
1. Open up `examples/yarn/index.ts` and `examples/yarn/dagger.graphql`
1. Go over how it is calling out to the `yarn` CLI via core.Exec.
   - `core` is a built-in package, always available to any action.

### 2.2 Alpine Action

1. Note that `yarn` calls out to `alpine` to create an image with the `yarn` cli present.
1. Open up `examples/alpine/alpine.go` and `examples/alpine/dagger.graphql`
1. Note that this action is implemented in Go.
   - However, the same schema format is used to define it as other actions we've seen. This enables it to be called from Typescript as easily as any other language.
1. Briefly show how Alpine action works; just uses `core` actions to install the provided packages.

### 2.3 Netlify Action

1. Go back to `examples/todoapp/index.ts`
1. Show how the deploy action calls build and test in parallel with one another before moving onto the actual deploy.
1. Note that it calls out to the `netlify` action to do the deployment
1. Open up `examples/netlify/index.ts`
1. Note that in this case, rather than calling out to `core.Exec`, we are running code directly in the action, with a combination of client libraries and shelling out to the netlify CLI when necessary

## 3. Modifying an Action

1. Now we're going to try a simple modification to the TODOApp actions.
1. Go back to `examples/todoapp/index.ts`
1. Change the site name constant to something different.
1. Run same command as before:

```console
go run cmd/cloak/main.go -f examples/todoapp/dagger.yaml -local-dirs src=examples/todoapp/app -secrets token="$NETLIFY_AUTH_TOKEN" <<'EOF'
query Build($local_src: FS!, $secret_token: String!) {
    todoapp{deploy(src: $local_src, token: $secret_token){url}}
}
EOF
```

1. Click on the output URL, show the TODOApp at the new URL.

TODO: this is kind of lame, immediate question will likely be "can I make the siteName an input parameter". Should think of something better.

## 4. Creating a New Action

TODO: not ready to be part of a demo for a user quite yet (DX is still too rough, too much copy-pasting schemas, need a bit more automation)
