# Server + client repos

## Pattern: two workflows, one toolchain
- Provide `backendBuild`, `backendServe`, `clientBuild`, and `build` functions.
- Keep shared inputs configurable (e.g., server path, client path, ports).

## Pattern: separate toolchains
- `toolchains/server` for backend build/serve.
- `toolchains/client` for frontend build/dev.
- A root `dagger.json` registers both toolchains.

## Common inputs
Use `Directory` inputs with `defaultPath` for both server and client.

Example signature (TypeScript):
```ts
buildAll(
  @argument({ defaultPath: "server" }) server: Directory,
  @argument({ defaultPath: "client" }) client: Directory,
): Promise<void>
```

## Serve UI with backend dependency
- Start the backend as a service and bind it into the client container.
- Pass the backend URL via env (e.g., `VITE_API_TARGET=http://backend:8081`).
- Expose only the client port to the host.

Example (TypeScript):
```ts
serve(
  @argument({ defaultPath: "server" }) server: Directory,
  @argument({ defaultPath: "client" }) client: Directory,
): Service {
  const backend = this.backendServe(server, 8081)
  return this.clientContainer(client)
    .withServiceBinding("backend", backend)
    .withEnvVariable("VITE_API_TARGET", "http://backend:8081")
    .withExposedPort(8080)
    .asService({ args: ["npm", "run", "dev", "--", "--host", "0.0.0.0", "--port", "8080"] })
}
```

## Frontend cache hints
- Cache package manager state (`npm`/`pnpm`/`yarn`) and build outputs.
- Consider `node:XX` images that match the repo's tooling.
