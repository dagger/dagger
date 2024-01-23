import {
  dag,
  Container,
  Directory,
  object,
  func,
  field,
} from "@dagger.io/dagger"

import { Commands } from "./commands"
import { withNpm, withPnpm, withYarn } from "./pkgManager"

@object
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class Node {
  @field
  version = "16-alpine"

  // Private field that contains the node container
  ctr = dag.container().from(`node:${this.version}`)

  /**
   * Overrides the default node version (16-alpine) of the
   * module's container.
   *
   * @param version The node version to use.
   */
  @func
  withVersion(version: string): Node {
    this.version = version

    this.ctr = dag.container().from(`node:${this.version}`)

    return this
  }

  /**
   * Overrides the container used in the node module.
   *
   * @param ctr The container to use for the node module.
   */
  @func
  withContainer(ctr: Container): Node {
    this.ctr = ctr

    return this
  }

  /**
   * Returns the container used in the node module.
   */
  @func
  container(): Container {
    return this.ctr
  }

  /**
   * Add source to the module container.
   *
   * @param source The source directory to mount in the container.
   * @param workdir The working directory to use in the container (default to "/src").
   * @param cacheKey The cache key to use for the node_modules cache (default to "node-modules").
   */
  @func
  withSource(
    source: Directory,
    workdir = "/src",
    cacheKey = "node-modules"
  ): Node {
    this.ctr = this.ctr
      .withWorkdir(workdir)
      .withDirectory(workdir, source)
      .withMountedCache(`${workdir}/node_modules`, dag.cacheVolume(cacheKey))

    return this
  }

  /**
   * Setup a package manager in the container and set entrypoint to it.
   *
   * @param pkgManager The package manager to use ("yarn", "npm", "pnpm").
   * @param cacheKey The cache key prefix to use for the downloaded packages (default to `node-module-${pkgManager}`).
   */
  @func
  withPkgManager(pkgManager: string, cacheKey = "node-module"): Node {
    switch (pkgManager) {
      case "yarn": {
        this.ctr = this.ctr.with(withYarn(cacheKey))
        break
      }
      case "npm": {
        this.ctr = this.ctr.with(withNpm(cacheKey))
        break
      }
      case "pnpm": {
        this.ctr = this.ctr.with(withPnpm(cacheKey))
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
  @func
  install(pkgs: string[] = []): Node {
    this.ctr = this.ctr.withExec(["install", ...pkgs])

    return this
  }

  /**
   * Execute commands in the container.
   */
  @func
  commands(): Commands {
    return new Commands(this.ctr)
  }
}
