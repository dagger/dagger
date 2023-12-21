import { object, func } from "@dagger.io/dagger"

@object
class Syntax {
	@func
	singleQuoteDefaultArgHello(msg: string = 'world'): string {
		return `hello ${msg}`
	}

	@func
	doubleQuotesDefaultArgHello(msg: string = "world"): string {
		return `hello ${msg}`
	}
}