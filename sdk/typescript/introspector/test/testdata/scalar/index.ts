import { Platform } from "../../../../api/client.gen.ts"
import { func, object } from "../../../decorators/decorators.ts"

@object()
export class Scalar {
  @func()
  fromPlatform(platform: Platform): string {
    return platform as string
  }

  @func()
  fromPlatforms(platforms: Platform[]): string[] {
    return platforms.map((p) => p as string)
  }
}
