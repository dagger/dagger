import { dag, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  alpineVersion: string

  // This intentionally exercises an async constructor pattern accepted by the SDK.
  constructor() {
    return (async () => {
      this.alpineVersion = await dag
        .container()
        .from("alpine:3.22.1")
        .file("/etc/alpine-release")
        .contents()

      return this
    })()
  }
}
