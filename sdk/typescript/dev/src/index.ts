import { dag, Container, object, func, Directory } from "@dagger.io/dagger"

import { CI } from "./ci"

@object
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class TypescriptSdkDev {
  source: Directory

  constructor(source: Directory) {
    this.source = source
  }

  /**
   * CI commands for the TypeScript SDK.
   */
  @func
  ci(): CI {
    return new CI(this.project())
  }

  /**
   * Get the TypeScript SDK project inside the CI container.
   *
   * This is useful for debugging the CI locally or test commands in
   * an isolated environment.
   * Example usage: `dagger call project shell --args /bin/sh`
   */
  @func
  project(): Container {
    // Extract package.json and yarn.lock to a temporary directory
    const dependencyFiles = dag
      .directory()
      .withFile("package.json", this.source.file("package.json"))
      .withFile("yarn.lock", this.source.file("yarn.lock"))

    return dag
      .node()
      .withPkgManager("yarn")
      .withSource(dependencyFiles)
      .install() // Install dependencies prior to adding source to improve caching
      .withSource(this.source)
      .container()
  }
}
