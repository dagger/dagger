import {
  dag,
  Container,
  Directory,
  object,
  func,
  field,
  CacheVolume,
} from "@dagger.io/dagger"

import { Commands } from "./commands"
import { withNpm, withPnpm, withYarn } from "./pkgManager"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class Node {
  @field()
  version = "16-alpine"

  @field()
  container: Container

  constructor(version?: string, ctr?: Container) {
    this.version = version ?? this.version
    this.container = ctr ?? dag.container().from(`node:${this.version}`)
  }

  /**
   * Add source to the module container.
   *
   * @param source The source directory to mount in the container.
   * @param cacheKey The cache key to use for the node_modules cache (default to "node-modules").
   */
  @func()
  withSource(source: Directory, cache?: CacheVolume): Node {
    const workdir = "/src"

    this.container = this.container
      .withWorkdir(workdir)
      .withDirectory(workdir, source)
      .withMountedCache(
        `${workdir}/node_modules`,
        cache ?? dag.cacheVolume("node-modules")
      )

    return this
  }

  /**
   * Setup a package manager in the container and set entrypoint to it.
   *
   * @param pkgManager The package manager to use ("yarn", "npm", "pnpm").
   * @param cacheKey The cache key prefix to use for the downloaded packages (default to `node-module-${pkgManager}`).
   */
  @func()
  withPkgManager(pkgManager: string, cacheKey = "node-module"): Node {
    switch (pkgManager) {
      case "yarn": {
        this.container = this.container.with(withYarn(cacheKey))
        break
      }
      case "npm": {
        this.container = this.container.with(withNpm(cacheKey))
        break
      }
      case "pnpm": {
        this.container = this.container.with(withPnpm(cacheKey))
        break
      }
      default:
        throw new Error(`Unknown package manager: ${pkgManager}`)
    }

    return this
  }

  /**
   * Downloads dependencies in the container.
   *
   * @param pkgs Additonal packages to install in the container.
   */
  @func()
  install(pkgs: string[] = []): Node {
    this.container = this.container.withExec(["install", ...pkgs])

    return this
  }

  /**
   * Execute commands in the container.
   */
  @func()
  commands(): Commands {
    return new Commands(this.container)
  }
}
