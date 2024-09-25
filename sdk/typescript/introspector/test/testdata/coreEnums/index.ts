import { ImageLayerCompression } from '../../../../api/client.gen.js'
import { func, object } from "../../../decorators/decorators.js"


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
