import { func, object } from "../../../../decorators.js"

@object()
export class DeprecationArgs {
	@func()
	legacy(
		/** @deprecated Use `other` instead. */
		input?: string,
		other: string,
	): string {
		return input ?? other
	}

	@func()
	current(other: string): string {
		return other
	}
}
