
import { field, func, object } from "../../../../decorators.js"

/**
 * Module exposing deprecated fields.
 */
@object()
export class DeprecatedFields {
	/** @deprecated Inline note that this field is going away. */
	@field()
	legacyField = "legacy"

	@field()
	latestField = "latest"

	@func()
	record(): LegacyRecord {
		return { note: "legacy", message: "latest" }
	}
}

/** @deprecated Alias maintained only for compatibility. */
export type LegacyRecord = {
	/** @deprecated Prefer `message`. */
	note: string
	message: string
}