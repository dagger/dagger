import { object, func, Platform } from "@dagger.io/dagger"

@object()
export class Test {
	@func()
	fromPlatform(platform: Platform): string {
		return platform as string
	}

	@func()
	toPlatform(platform: string): Platform {
		return platform as Platform
	}

	@func()
	fromPlatforms(platform: Platform[]): string[] {
		return platform.map(p => p as string)
	}

	@func()
	toPlatforms(platform: string[]): Platform[] {
		return platform.map(p => p as Platform)
	}
}
