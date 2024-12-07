import logger from "node-color-log"

export const log = (stack?: string) =>
  logger.bgColor("red").color("black").log(stack)
