import ts from "typescript"

import { IntrospectionError } from "../../../common/errors/index.js"
import { AST, Location } from "../typescript_module/index.js"
import { Locatable } from "./locatable.js"
import { DaggerObjectBase } from "./objectBase.js"
import { References } from "./reference.js"
import {
  DaggerObjectTypeProperties,
  DaggerObjectTypeProperty,
} from "./typeObjectProperty.js"

/**
 * Represents an object defined using the `type` keyword.
 *
 * Type object can only contains fields, no methods are allowed.
 * All fields are public and exposed to the Dagger API.
 *
 * @example
 * ```ts
 * @object()
 * export class MyObject {
 *  @func()
 *  public name: string
 *
 *  @func()
 *  async getName(): Promise<string> {
 *    return this.name
 *  }
 * }
 * ```
 */
export class DaggerTypeObject extends Locatable implements DaggerObjectBase {
  public name: string
  public description: string
  public deprecated?: string
  public _constructor = undefined
  public methods = {}
  public properties: DaggerObjectTypeProperties = {}

  private symbol: ts.Symbol

  kind(): "class" | "object" {
    return "object"
  }

  constructor(
    private readonly node: ts.TypeAliasDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    if (!this.node.name) {
      throw new IntrospectionError(
        `could not resolve name of enum at ${AST.getNodePosition(node)}.`,
      )
    }
    this.name = this.node.name.getText()
    this.symbol = this.ast.getSymbolOrThrow(this.node.name)
    const { description, deprecated } = this.ast.getSymbolDoc(this.symbol)
    this.description = description
    this.deprecated = deprecated

    const type = this.ast.getTypeFromTypeAlias(this.node)

    if (type.flags & ts.TypeFlags.Object) {
      const objectType = type as ts.ObjectType
      const properties = objectType.getProperties()
      for (const property of properties) {
        const daggerProperty = new DaggerObjectTypeProperty(
          this.node,
          property,
          this.ast,
        )
        this.properties[daggerProperty.name] = daggerProperty
      }
    }
  }

  public getLocation(): Location {
    return AST.getNodeLocation(this.node)
  }

  public getReferences(): string[] {
    const references: string[] = []

    for (const property of Object.values(this.properties)) {
      const ref = property.getReference()
      if (ref) {
        references.push(ref)
      }
    }

    return references.filter((v, i, arr) => arr.indexOf(v) === i)
  }

  public propagateReferences(references: References): void {
    for (const property of Object.values(this.properties)) {
      property.propagateReferences(references)
    }
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      properties: this.properties,
      deprecated: this.deprecated,
    }
  }
}
