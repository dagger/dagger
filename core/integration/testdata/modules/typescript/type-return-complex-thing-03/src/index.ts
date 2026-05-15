
import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
export class ScanReport {
  @func()
  contents: string

  @func()
  authors: string[]

  constructor(contents: string, authors: string[]) {
    this.contents = contents
    this.authors = authors
  }
}

@object()
export class ScanResult {
  @func("targets")
  containers: Container[]

  @func()
  report: ScanReport

  constructor(containers: Container[], report: ScanReport) {
    this.containers = containers
    this.report = report
  }
}

@object()
export class Test {
  @func()
  async scan(): Promise<ScanResult> {
    return new ScanResult(
      [
        dag.container().from("alpine:3.22.1").withExec(["echo", "hello world"])
      ],
      new ScanReport("hello world", ["foo", "bar"])
    )
  }
}
