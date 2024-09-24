import { func, object } from '../../../decorators/decorators.js'
import { Platform } from '../../../../api/client.gen.js'

@object()
export class Scalar {
    @func()
    fromPlatform(platform: Platform): string {
		return platform as string
	}

    @func()
	fromPlatforms(platforms: Platform[]): string[] {
		return platforms.map(p => p as string)
	}
}
