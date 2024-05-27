import { func, object, daggerInterface } from "../../../decorators/decorators.js"

/**
 * Image interface
 */
@daggerInterface()
export abstract class Image {
  /**
   * Return the type of the image.
   */
  abstract type(): string
}

@object()
export class PNG implements Image {
  type(): string {
    return "png"
  }
}

@object()
export class JPEG implements Image {
  type(): string {
    return "jpeg"
  }
}

/**
 * HelloWorld class
 */
@object()
export class Interfaces {
  @func()
  createImage(type: string): Image {
    switch (type) {
    case "png":
      return new PNG()
    case "jpeg":
      return new JPEG()
    default:
      throw new Error("Unknown image type")  
    }
  }
}
