import { dag, object, func } from "@dagger.io/dagger"

@object
class Minimal {
	@func
	hello(): string {
		return "hello"
	}

	@func
	echo(msg: string): string {
		return this.echoOpts(msg, "...", 3)
	}

	@func
	echoOpts(msg: string, suffix: string = "", times: number = 1): string {
		msg = msg += suffix

		return msg.repeat(times)
	}

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