import { expect, test, describe } from "bun:test"

import { getTracer } from "./index"

describe("create and use a tracer", async () => {
  test("startActivespan", async () => {
    const tracer = getTracer()

    const before = Date.now()
    await tracer.startActiveSpan("hello world", async () => {
      await Bun.sleep(1000)
    })
    const after = Date.now()

    expect(
      after - before,
      `${after} - ${before} is not greater than 1000ms`,
    ).toBeGreaterThan(1000)
  })

  test("startSpan", () => {
    const tracer = getTracer()

    const span = tracer.startSpan("hello world")
    span.end()
  })
})
