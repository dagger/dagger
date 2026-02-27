import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-proto"
import type { Instrumentation } from "@opentelemetry/instrumentation"
import { NodeSDK } from "@opentelemetry/sdk-node"
import {
  BatchSpanProcessor,
  type SpanProcessor,
} from "@opentelemetry/sdk-trace-base"

import { DaggerContextManager } from "./context_manager"
import { LiveSpanProcessor, NEARLY_IMMEDIATE } from "./span_processor"
import { isOtelEnabled } from "./util"

/**
 * OtelSDK is a wrapper around the NodeSDK that simplifies the instantiation
 * of the client when using dagger.
 *
 * This automatically add the right context manager and live span processor.
 */
export class OtelSDK {
  private _otelSDK: NodeSDK

  /**
   * Provide instrumentation library for auto-instrumentation
   *
   * @example
   * ```ts
   * import { getNodeAutoInstrumentation } from "@opentelemetry/auto-instrumentations-node"
   * import { OtelSDK } from "@dagger.io/telemetry"
   *
   * const sdk = new OtelSDK([
   *   getNodeAutoInstrumentation(),
   * ])
   * ```
   */
  constructor(instrumentations?: Instrumentation[])

  /**
   * Provide a custom NodeSDK client
   *
   * @param sdk
   */
  constructor(sdk?: NodeSDK)

  constructor(sdkOrInstrumentations?: NodeSDK | Instrumentation[]) {
    if (sdkOrInstrumentations instanceof NodeSDK) {
      this._otelSDK = sdkOrInstrumentations
    } else {
      const exporter = new OTLPTraceExporter()

      let spanProcessor: SpanProcessor
      if (process.env.OTEL_EXPORTER_OTLP_TRACES_LIVE !== undefined) {
        spanProcessor = new LiveSpanProcessor(exporter)
      } else {
        spanProcessor = new BatchSpanProcessor(exporter, {
          scheduledDelayMillis: NEARLY_IMMEDIATE,
        })
      }

      this._otelSDK = new NodeSDK({
        instrumentations: sdkOrInstrumentations,
        contextManager: new DaggerContextManager(),
        spanProcessors: [spanProcessor],
      })
    }
  }

  public sdk() {
    return this._otelSDK
  }

  /**
   * Start the otel SDK.
   */
  public start() {
    if (isOtelEnabled() === true) {
      this._otelSDK.start()
    }
  }

  /**
   * Start the otel SDK.
   *
   * @deprecated please use `start`
   */
  public initialize() {
    this.start()
  }

  /**
   * Shutdown the otel SDK, this will also flush traces
   * before closing the client.
   */
  public async shutdown() {
    try {
      if (isOtelEnabled() === true) {
        await this._otelSDK.shutdown()
      }
    } catch {
      // Silently fail if otelSDK shutdown fails
      console.warn("failed to shutdown otel sdk")
    }
  }

  /**
   * Shutdown the otel SDK, this will also flush traces
   * before closing the client.
   *
   * @deprecated please use `shutdown`
   */
  public async close() {
    await this.shutdown()
  }
}
