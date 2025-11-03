package tsutils

import (
	_ "embed"
	"fmt"
)

// This file directly embeds the static files content in the SDK so we don't have to
// fetch them from current module source.
// This is done to avoid having to fetch the SDK module's filesystem which can take up
// to 1s to load.

//go:embed template/tsconfig.json
var DefaultTSConfigJSON string

// StaticBundleTelemetryTS is the content of the sdk/index.ts file.
//
//go:embed module/index.ts
var StaticBundleIndexTS string

// StaticBundleTelemetryTS is the content of the sdk/telemetry.ts file.
//
//go:embed module/telemetry.ts
var StaticBundleTelemetryTS string

// StaticDefaultPackageJSON is the default content of the package.json file.
//
//go:embed template/package.json
var StaticDefaultPackageJSON string

// StaticEntrypoint is the content of the __dagger.entrypoint.ts file.
//
//go:embed bin/__dagger.entrypoint.ts
var StaticEntrypointTS string

var TemplateIndexTS = func(name string) string {
	return fmt.Sprintf(`/**
 * A generated module for QuickStart functions
 *
 * This module has been generated via dagger init and serves as a reference to
 * basic module structure as you get started with Dagger.
 *
 * Two functions have been pre-created. You can modify, delete, or add to them,
 * as needed. They demonstrate usage of arguments and return types using simple
 * echo and grep commands. The functions can be called from the dagger CLI or
 * from one of the SDKs.
 *
 * The first line in this comment block is a short description line and the
 * rest is a long description with more detail on the module's purpose or usage,
 * if appropriate. All modules should have a short description.
 */
import { dag, Container, Directory, object, func } from "@dagger.io/dagger"

@object()
export class %s {
  /**
   * Returns a container that echoes whatever string argument is provided
   */
  @func()
  containerEcho(stringArg: string): Container {
    return dag.container().from("alpine:latest").withExec(["echo", stringArg])
  }

  /**
   * Returns lines that match a pattern in the files of the provided Directory
   */
  @func()
  async grepDir(directoryArg: Directory, pattern: string): Promise<string> {
    return dag
      .container()
      .from("alpine:latest")
      .withMountedDirectory("/mnt", directoryArg)
      .withWorkdir("/mnt")
      .withExec(["grep", "-R", pattern, "."])
      .stdout()
  }
}
`, name)
}
