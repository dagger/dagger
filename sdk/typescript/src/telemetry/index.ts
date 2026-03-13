import { OtelSDK } from "@dagger.io/telemetry"

export { getTracer } from "@dagger.io/telemetry"
export type { Tracer } from "@dagger.io/telemetry"

const sdk = new OtelSDK()

export function start() {
  sdk.start()
}

export async function shutdown() {
  await sdk.shutdown()
}
