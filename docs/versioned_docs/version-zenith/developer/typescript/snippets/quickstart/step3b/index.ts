import {  object, func, field } from "@dagger.io/dagger"

@object()
class PotatoMessage {
	@field()
	message: string

	@field()
	from: string

	constructor(message: string, from: string) {
		this.message = message
		this.from = from
	}
}

@object()
class Potato {
	@func
	helloWorld(message: string): PotatoMessage {
		return new PotatoMessage(message, "potato@example.com")
	}
}

