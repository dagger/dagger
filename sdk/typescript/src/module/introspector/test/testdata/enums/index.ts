import { enumType, field, func, object } from "../../../../decorators.js"

/**
 * Enum for Status
 */
@enumType()
export class Status {
  /**
   * Active status
   */
  static readonly ACTIVE: string = "ACTIVE"

  /**
   * Inactive status
   */
  static readonly INACTIVE: string = "INACTIVE"
}

@object()
export class Enums {
  @field()
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
