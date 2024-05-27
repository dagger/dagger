import { Constructor } from "./constructor.js";
import { Methods } from "./method.js";
import { Properties } from "./property.js";

export abstract class DaggerClass {
  abstract get name(): string

  abstract get _constructor(): Constructor | undefined

  abstract get properties(): Properties 

  abstract get description(): string

  abstract get methods(): Methods
}