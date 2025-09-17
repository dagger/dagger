import { func, object } from "../../../../decorators.js"

/**
 * Enum for Status
 */
export enum Status {
  /**
   * Active status
   */
  ACTIVE = "ACTIVE value",

  /**
   * Inactive status
   */
  INACTIVE = "INACTIVE value",
}

@object()
export class Enums {
  @func()
  status: Status = Status.ACTIVE

  @func()
  setStatus(status: Status): Enums {
    this.status = status

    return this
  }

  @func()
  getStatus(): Status {
    return this.status
  }
}
