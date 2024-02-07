import {
  dag,
  Container,
  object,
  func,
  Directory,
  field,
} from "@dagger.io/dagger"

@object()
// eslint-disable-next-line @typescript-eslint/no-unused-vars
class TypescriptSdkDev {
  /**
   * Project dev environment.
   * Open a shell in with: `dagger call --source=. project terminal --cmd=/bin/sh`
   */
  @field()
  project: Container

  constructor(source: Directory) {
    // Extract package.json and yarn.lock to a temporary directory
    const dependencyFiles = dag
      .directory()
      .withFile("package.json", source.file("package.json"))
      .withFile("yarn.lock", source.file("yarn.lock"))

    // Get source without generated files nor useless files.
    const sourceCode = dag.directory().withDirectory("/", source, {
      include: [
        "**/*.ts",
        "tsconfig.json",
        "package.json",
        "yarn.lock",
        ".mocharc.json",
        ".eslintrc.cjs",
        ".prettierrc.cjs",
      ],
      exclude: ["node_modules", "dist", "dev"],
    })

    // Install dependencies and add source code.
    this.project = dag
      .node()
      .withPkgManager("yarn")
      .withSource(dependencyFiles)
      .install() // Install dependencies prior to adding source to improve caching
      .withSource(sourceCode)
      .container()
  }

  /**
   * Execute the TypeScript SDK unit tests.
   *
   * Example usage: `dagger call test stdout`
   */
  @func()
  test(): Container {
    // We cannot use node module here because the tests
    // need access to experimental dagger.
    // TODO: fix provisioning tests (that will be outdated with 0.10??)
    return this.project.withExec(["test"], {
      experimentalPrivilegedNesting: true,
    })
  }

  /**
   * Lint the TypeScript SDK.
   *
   * Example usage: `dagger call lint stdout`
   */
  @func()
  lint(): Container {
    return dag.node({ ctr: this.project }).commands().lint()
  }

  /**
   * Build the TypeScript SDK.
   *
   * Example usage `dagger call -o ./dist build directory --path dist`
   */
  @func()
  build(): Container {
    return dag.node({ ctr: this.project }).commands().build()
  }
}
