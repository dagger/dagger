# Services (Dagger)

## Basic pattern
A service is a container prepared with the right ports and command, returned as a `Service`.

TypeScript example:
```ts
return dag
  .container()
  .from("alpine:latest")
  .withExposedPort(8080)
  .asService({ args: ["/bin/server"] })
```

## Interdependent services
Bind one service into another container to avoid exposing the dependency to the host.

TypeScript example:
```ts
const backend = this.backendServe(serverSource, 8081)
return this.clientContainer(clientSource)
  .withServiceBinding("backend", backend)
  .withEnvVariable("VITE_API_TARGET", "http://backend:8081")
  .withExposedPort(8080)
  .asService({ args: ["npm", "run", "dev", "--", "--host", "0.0.0.0", "--port", "8080"] })
```

## Ports and reachability
- Expose the container port with `withExposedPort`.
- Run with `dagger call <toolchain> serve up --ports <host>:<container>`.
- Inside the container, bind to `0.0.0.0` so the service is reachable.

Example run:
```
dagger call server serve up --ports 8080:8080
```

## Build vs serve
- **Build**: use `.sync()` on a container or object chain to force evaluation.
- **Serve**: return a `Service` and avoid `.sync()` in that function.
