import { func, object } from "../../../../decorators.js"

@object()
export class DeprecationArgs {
	@func()
	legacy(
		other: string,
		/** @deprecated Use `other` instead. */
		input?: string,
	): string {
		return input ?? other
	}

	@func()
	current(other: string): string {
		return other
	}
}
