import { getBooleanFromEnv } from "@opentelemetry/core"

export function isOtelEnabled(): boolean {
  if (!Object.keys(process.env).some((key) => key.startsWith("OTEL_"))) {
    return false
  }

  return getBooleanFromEnv("OTEL_SDK_DISABLED") !== true
}
