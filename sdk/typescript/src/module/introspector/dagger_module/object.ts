import ts from "typescript"

import { IntrospectionError } from "../../../common/errors/index.js"
import { AST, Location } from "../typescript_module/index.js"
import { DaggerConstructor } from "./constructor.js"
import { FUNCTION_DECORATOR, OBJECT_DECORATOR } from "./decorator.js"
import { DaggerFunction, DaggerFunctions } from "./function.js"
import { Locatable } from "./locatable.js"
import { DaggerObjectBase } from "./objectBase.js"
import { DaggerProperties, DaggerProperty } from "./property.js"
import { References } from "./reference.js"

/**
 * Represents an object defined using the `class` keyword.
 *
 * The class may contains methods and fields, that may or may not be exposed to the Dagger API.
 *
 * @example
 * ```ts
 * type MyObject = {
 *   name: string
 *   age: number
 * }
 * ```
 */
export class DaggerObject extends Locatable implements DaggerObjectBase {
  public name: string
  public description: string
  public deprecated?: string
  public _constructor: DaggerConstructor | undefined = undefined
  public methods: DaggerFunctions = {}
  public properties: DaggerProperties = {}

  private symbol: ts.Symbol

  kind(): "class" | "object" {
    return "class"
  }

  constructor(
    private readonly node: ts.ClassDeclaration,
    private readonly ast: AST,
  ) {
    super(node)

    if (!this.node.name) {
      throw new IntrospectionError(
        `could not resolve name of class at ${AST.getNodePosition(node)}.`,
      )
    }
    this.name = this.node.name.getText()

    if (!this.ast.isNodeDecoratedWith(node, OBJECT_DECORATOR)) {
      throw new IntrospectionError(
        `class ${this.name} at ${AST.getNodePosition(node)} is used by the module but not exposed with a dagger decorator.`,
      )
    }

    const modifiers = ts.getCombinedModifierFlags(this.node)

    if (!(modifiers & ts.ModifierFlags.Export)) {
      console.warn(
        `missing export in class ${this.name} at ${AST.getNodePosition(node)} but it's used by the module.`,
      )
    }

    this.symbol = this.ast.getSymbolOrThrow(this.node.name)
    const { description, deprecated } = this.ast.getSymbolDoc(this.symbol)
    this.description = description
    this.deprecated = deprecated

    for (const member of this.node.members) {
      if (ts.isPropertyDeclaration(member)) {
        const property = new DaggerProperty(member, this.ast)
        this.properties[property.alias ?? property.name] = property

        continue
      }

      if (ts.isConstructorDeclaration(member)) {
        this._constructor = new DaggerConstructor(member, this.ast)

        continue
      }

      if (
        ts.isMethodDeclaration(member) &&
        this.ast.isNodeDecoratedWith(member, FUNCTION_DECORATOR)
      ) {
        const daggerFunction = new DaggerFunction(member, this.ast)
        this.methods[daggerFunction.alias ?? daggerFunction.name] =
          daggerFunction

        continue
      }
    }
  }

  public getLocation(): Location {
    return AST.getNodeLocation(this.node)
  }

  public getReferences(): string[] {
    const references: string[] = []

    if (this._constructor) {
      references.push(...this._constructor.getReferences())
    }

    for (const property of Object.values(this.properties)) {
      const ref = property.getReference()
      if (ref) {
        references.push(ref)
      }
    }

    for (const fn of Object.values(this.methods)) {
      references.push(...fn.getReferences())
    }

    return references.filter((v, i, arr) => arr.indexOf(v) === i)
  }

  public propagateReferences(references: References): void {
    if (this._constructor) {
      this._constructor.propagateReferences(references)
    }

    for (const property of Object.values(this.properties)) {
      property.propagateReferences(references)
    }

    for (const fn of Object.values(this.methods)) {
      fn.propagateReferences(references)
    }
  }

  public toJSON() {
    return {
      name: this.name,
      description: this.description,
      deprecated: this.deprecated,
      constructor: this._constructor,
      methods: this.methods,
      properties: this.properties,
    }
  }
}
