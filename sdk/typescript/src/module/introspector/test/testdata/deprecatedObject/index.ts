import { func, object } from "../../../../decorators.js"

/**
 * @deprecated Use `ModernModule` instead.
 */
@object()
export class LegacyModule {
  @func()
  manifest(): LegacyType {
    return {
      oldField: "legacy",
    }
  }
}

/**
 * @deprecated Type alias kept for compatibility.
 */
export type LegacyType = {
  /**
   * @deprecated Migrate to `newField`.
   */
  oldField: string
}
