import { object, func } from "@dagger.io/dagger"

@object()
class Potato {
	/**
	 * @param count The number of potatoes to process
	 * @param mashed Whether the potatoes are mashed
	 */
	@func()
	helloWorld(count: number, mashed = false): string {
		if (mashed) {
			return `Hello Daggernauts, I have mashed ${count} potatoes`
		}

		return `Hello Daggernauts, I have ${count} potatoes`
	}
}
