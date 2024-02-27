/**
 * This module returns information about the OS for a container image
 */
import { dag, object, func } from "@dagger.io/dagger"

@object()
class MyModule {
  @func()
  async version(): Promise<string> {
    return await dag
      .container()
      .from("alpine:3.14.0")
      .withExec(["/bin/sh", "-c", "cat /etc/os-release | grep VERSION_ID"])
      .stdout()
  }
}
