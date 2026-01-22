# Rust server patterns

## Base image vs edition
- Match the Rust toolchain to the project's edition.
- Rust 2024 edition requires Rust 1.85+.

## Typical container setup
```ts
return dag
  .container()
  .from("rust:1.85-slim")
  .withMountedCache("/usr/local/cargo/registry", dag.cacheVolume("cargo-registry"))
  .withMountedCache("/usr/local/cargo/git", dag.cacheVolume("cargo-git"))
  .withMountedCache("/app/target", dag.cacheVolume("cargo-target"))
  .withMountedDirectory("/app", source)
  .withWorkdir("/app")
  .withExec(["cargo", "build"])
```

## Run a server
- Set `HOST=0.0.0.0` and `PORT` if the server honors env vars.
- Use `.asService({ args: ["cargo", "run"] })` or run a built binary.
