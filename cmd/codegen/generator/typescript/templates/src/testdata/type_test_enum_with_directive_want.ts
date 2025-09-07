
/**
 * Compression algorithm to use for image layers.
 */
export enum ImageLayerCompression {
  EstarGz = "EStarGZ",
  Estargz = ImageLayerCompression.EstarGz,
  Gzip = "Gzip",
  Uncompressed = "Uncompressed",
  Zstd = "Zstd",
}

/**
 * Utility function to convert a ImageLayerCompression value to its name so
 * it can be uses as argument to call an exposed function.
 */
function ImageLayerCompressionValueToName(value: ImageLayerCompression): string {
  switch (value) {
    case ImageLayerCompression.EstarGz:
      return "EStarGZ"
    case ImageLayerCompression.Gzip:
      return "Gzip"
    case ImageLayerCompression.Uncompressed:
      return "Uncompressed"
    case ImageLayerCompression.Zstd:
      return "Zstd"
    default:
      return value
  }
}

/**
 * Utility function to convert a ImageLayerCompression name to its value so
 * it can be properly used inside the module runtime.
 */
function ImageLayerCompressionNameToValue(name: string): ImageLayerCompression {
  switch (name) {
    case "EStarGZ":
      return ImageLayerCompression.EstarGz
    case "Gzip":
      return ImageLayerCompression.Gzip
    case "Uncompressed":
      return ImageLayerCompression.Uncompressed
    case "Zstd":
      return ImageLayerCompression.Zstd
    default:
      return name as ImageLayerCompression
  }
}