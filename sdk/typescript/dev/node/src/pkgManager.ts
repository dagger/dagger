import { dag, Container } from "@dagger.io/dagger"

/**
 * Add yarn as package manager in the container.
 *
 * @param cacheKey The cache key to use for the downloaded packages.
 */
export function withYarn(cacheKey: string): (ctr: Container) => Container {
  return (ctr: Container) =>
    ctr
      .withEntrypoint(["yarn"])
      .withMountedCache(
        "/usr/local/share/.cache/yarn",
        dag.cacheVolume(`${cacheKey}-yarn`)
      )
}

/**
 * Add npm as package manager in the container.
 *
 * @param cacheKey The cache key to use for the downloaded packages.
 */
export function withNpm(cacheKey: string): (ctr: Container) => Container {
  return (ctr: Container) =>
    ctr
      .withEntrypoint(["npm"])
      .withMountedCache("/root/.npm", dag.cacheVolume(`${cacheKey}-npm`))
}

/**
 * Add pnpm as package manager in the container.
 *
 * @param cacheKey The cache key to use for the downloaded packages.
 */
export function withPnpm(cacheKey: string): (ctr: Container) => Container {
  return (ctr: Container) =>
    ctr
      .withExec(["npm", "install", "-g", "pnpm"])
      .withEntrypoint(["pnpm"])
      .withMountedCache("/pnpm/store", dag.cacheVolume(`${cacheKey}-pnpm`))
}
