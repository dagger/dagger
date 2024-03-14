import { dag, func, object } from "@dagger.io/dagger"

@object()
class Trivy {
  @func()
  async scanImage(
    imageRef: string,
    severity = "UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL",
    exitCode = 0,
    format = "table",
  ): Promise<string> {
    return dag
      .container()
      .from("aquasec/trivy:latest")
      .withExec([
        "image",
        "--quiet",
        "--severity",
        severity,
        "--exit-code",
        `${exitCode}`,
        "--format",
        format,
        imageRef,
      ])
      .stdout()
  }
}
