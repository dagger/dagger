import { SEMRESATTRS_SERVICE_NAME } from "@opentelemetry/semantic-conventions"
import { Resource } from "@opentelemetry/resources"
import { NodeSDK } from "@opentelemetry/sdk-node"
import {
  ConsoleSpanExporter,
  BatchSpanProcessor,
  BasicTracerProvider,
} from "@opentelemetry/sdk-trace-node"
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-grpc"
import * as opentelemetry from "@opentelemetry/api"
import { SpanStatusCode } from "@opentelemetry/api"
import { credentials } from "@grpc/grpc-js"

const provider = new BasicTracerProvider({
  resource: new Resource({
    [SEMRESATTRS_SERVICE_NAME]: "dagger-typescript-sdk",
  }),
})

const grpcExporter = new OTLPTraceExporter({
  credentials: credentials.createInsecure(),
})

const consoleExporter = new ConsoleSpanExporter()

provider.addSpanProcessor(new BatchSpanProcessor(consoleExporter))
provider.addSpanProcessor(new BatchSpanProcessor(grpcExporter))

provider.register()

console.debug("Provider registered")

export const tracer = opentelemetry.trace.getTracer("dagger.io/sdk.typescript")

export function getContext() {
  const ctx = opentelemetry.context.active()

  const parentID = process.env.TRACEPARENT
  if (parentID) {
    console.debug(`Using parentID ${parentID}`)
    return opentelemetry.propagation.extract(ctx, { traceparent: parentID })
  }

  return ctx
}

export async function withTracer(name: string, fn: () => Promise<unknown>) {
  return await opentelemetry.context.with(getContext(), async () => {
    return tracer.startActiveSpan(name, {}, async (span) => {
      try {
        return await fn()
      } catch (e) {
        if (e instanceof Error) {
          span.setStatus({
            code: SpanStatusCode.ERROR,
            message: e.message,
          })
        }
        throw e
      } finally {
        span.end()
      }
    })
  })
}

export { grpcExporter, consoleExporter }
