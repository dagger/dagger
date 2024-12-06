import ts from "typescript"

import { AST, Location } from "../typescript_module/index.js"

/**
 * Locatable is a base class that any Dagger object can extend from to
 * provide a location method.
 */
export class Locatable {
  constructor(private readonly __node: ts.Node) {}

  getLocation(): Location {
    return AST.getNodeLocation(this.__node)
  }
}
