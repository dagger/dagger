import logger from "node-color-log"

export const log = (stack?: string) =>
  logger.bgColor("red").color("black").log(stack)

export function isDeno(): boolean {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return typeof (globalThis as any).Deno !== "undefined"
}
