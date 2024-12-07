import ts from "typescript"

import { IntrospectionError } from "../../../common/errors/index.js"
import { AST } from "../typescript_module/index.js"
import { DaggerObjectBase } from "./objectBase.js"
import { References } from "./reference.js"
import {
  DaggerObjectTypeProperties,
  DaggerObjectTypeProperty,
} from "./typeObjectProperty.js"

export class DaggerTypeObject implements DaggerObjectBase {
  public name: string
  public description: string
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
    if (!this.node.name) {
      throw new IntrospectionError(
        `could not resolve name of enum at ${AST.getNodePosition(node)}.`,
      )
    }
    this.name = this.node.name.getText()
    this.symbol = this.ast.getSymbolOrThrow(this.node.name)
    this.description = this.ast.getDocFromSymbol(this.symbol)

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
    }
  }
}
