import { func, object } from "../../../../decorators.js"

/** @deprecated Legacy enum kept only for compatibility. */
export enum LegacyMode {
	Alpha = "alpha",
	/** @deprecated Switch to Gamma. */
	Beta = "beta",
	Gamma = "gamma",
}

@object()
export class DeprecatedEnums {
	@func()
	useMode(mode: LegacyMode): LegacyMode {
		return mode
	}
}