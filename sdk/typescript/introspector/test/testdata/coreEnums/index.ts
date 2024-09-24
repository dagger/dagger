import { ImageLayerCompression } from "../../../../api/client.gen.ts"
import { func, object } from "../../../decorators/decorators.ts"

@object()
export class CoreEnums {
  @func()
  toImageLayerCompression(compression: string): ImageLayerCompression {
    return compression as ImageLayerCompression
  }

  @func()
  fromImageLayerCompression(compression: ImageLayerCompression): string {
    return compression
  }
}
