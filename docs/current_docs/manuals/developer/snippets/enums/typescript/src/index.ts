import { dag, object, func, enumType } from "@dagger.io/dagger"

/**
 * Vulnerability severity levels
 */
@enumType()
class Severity {
  /**
   * Undetermined risk; analyze further.
   */
  static readonly Unknown: string = "UNKNOWN"

  /**
   * Minimal risk; routine fix.
   */
  static readonly Low: string = "LOW"

  /**
   * Moderate risk; timely fix.
   */
  static readonly Medium: string = "MEDIUM"

  /**
   * Serious risk; quick fix needed.
   */
  static readonly High: string = "HIGH"

  /**
   * Severe risk; immediate action.
   */
  static readonly Critical: string = "CRITICAL"
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
