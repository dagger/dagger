import { object, func, field } from "@dagger.io/dagger"

/**
 * This is the Minimal object
 */
@object
class Minimal {
	/**
	 * This is a field
	 */
	@field
	foo: string = "bar"

	@func
	hello(): string {
		return "hello"
	}

	@func
	echo(msg: string): string {
		return this.echoOpts(msg, "...", 3)
	}

	/**
	 * EchoOpts does some opts things
	 * 
	 * @param msg the message to echo
	 * @param suffix String to append to the echoed message
	 * @param times number of times to repeat the message
	 */
	@func
	echoOpts(msg: string, suffix: string = "", times: number = 1): string {
		msg = msg += suffix

		return msg.repeat(times)
	}

	/**
	 * @param msg the message to echo
	 * @param isQuestion set to true to add a question mark.
	 */
	@func
	echoMaybe(msg: string, isQuestion = false): string {
		if (isQuestion) {
			return this.echo(msg + "?")
		}

		return this.echo(msg)
	}

	@func
	echoOptional(msg = "default"): string {
		return this.echo(msg)
	}

	@func
	echoOptionalSlice(msg = ["foobar"]): string {
		return this.echo(msg.join("+"))
	}

	@func
	echoes(msgs: string[]): string[] {
		return [this.echo(msgs.join(" "))]
	}

	@func
	echoesVariadic(...msgs: string[]): string {
		return this.echo(msgs.join(" "))
	}

	@func
	helloVoid(): void {}
}