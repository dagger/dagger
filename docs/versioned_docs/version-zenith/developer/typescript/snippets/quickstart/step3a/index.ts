import {  object, func } from "@dagger.io/dagger"

@object
class Potato {
	/**
	 * @param count The number of potatoes to process
	 * @param mashed Whether the potatoes are mashed
	 */
	@func
	helloWorld(count: number, mashed = false): string {
		if (mashed) {
			return `Hello world, I have mashed ${count} potatoes`
		}

		return `Hello world, I have ${count} potatoes`
	}
}

