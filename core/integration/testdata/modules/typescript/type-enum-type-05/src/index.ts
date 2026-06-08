import { ImageLayerCompression, object, func } from "@dagger.io/dagger";

@object()
export class Test {
  @func()
  fromImageLayerCompression(imageLayerCompression: ImageLayerCompression): string {
    return imageLayerCompression
  }

  @func()
  toImageLayerCompression(imageLayerCompression: string): ImageLayerCompression {
    return imageLayerCompression as ImageLayerCompression
  }
}