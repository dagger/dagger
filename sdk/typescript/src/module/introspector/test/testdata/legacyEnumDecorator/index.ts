import { enumType, func, object } from "../../../../decorators.js"

/**
 * Legacy enum exposed through the deprecated decorator.
 */
@enumType()
export class LegacyStatus {
  /**
   * Active status
   */
  static readonly ACTIVE: string = "ACTIVE value"

  /**
   * Inactive status
   */
  static readonly INACTIVE: string = "INACTIVE value"
}

@object()
export class LegacyEnums {
  @func()
  status: LegacyStatus = LegacyStatus.ACTIVE

  @func()
  setStatus(status: LegacyStatus): LegacyEnums {
    this.status = status

    return this
  }

  @func()
  getStatus(): LegacyStatus {
    return this.status
  }
}
