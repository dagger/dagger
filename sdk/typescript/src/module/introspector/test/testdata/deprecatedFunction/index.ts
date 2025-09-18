import { func, object } from "../../../../decorators.js"

@object()
export class DeprecatedFunctions {
	/** @deprecated Use `current` instead. */
	@func()
	legacy(): string {
		return "legacy"
	}

	/** @deprecated */
	@func()
	empty(): string {
		return "empty"
	}

	@func()
	current(): string {
		return "current"
	}
}