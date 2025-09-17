import { dag, object, func } from "@dagger.io/dagger"

/**
 * Vulnerability severity levels
 */
export enum Severity {
  /**
   * Undetermined risk; analyze further.
   */
  Unknown = "UNKNOWN",

  /**
   * Minimal risk; routine fix.
   */
  Low = "LOW",

  /**
   * Moderate risk; timely fix.
   */
  Medium = "MEDIUM",

  /**
   * Serious risk; quick fix needed.
   */
  High = "HIGH",

  /**
   * Severe risk; immediate action.
   */
  Critical = "CRITICAL",
}

@object()
class MyModule {
  @func()
  async scan(ref: string, severity: Severity): Promise<string> {
    const ctr = dag.container().from(ref)

    return dag
      .container()
      .from("aquasec/trivy:0.50.4")
      .withMountedFile("/mnt/ctr.tar", ctr.asTarball())
      .withMountedCache("/root/.cache", dag.cacheVolume("trivy-cache"))
      .withExec([
        "trivy",
        "image",
        "--format=json",
        "--no-progress",
        "--exit-code=1",
        "--vuln-type=os,library",
        `severity=${severity}`,
        "--show-suppressed",
        "--input=/mnt/ctr.tar",
      ])
      .stdout()
  }
}
