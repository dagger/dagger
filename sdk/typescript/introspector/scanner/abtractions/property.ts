import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { FieldTypeDef, TypeDef } from "../typeDefs.js"
import { typeToTypedef } from "../utils.js"

const PROPERTY_DECORATOR = "field"

export type Properties = { [name: string]: Property }

/**
 * Property is an abstraction of a class property.
 *
 * This aims to simplify and adds clarity to how we analyse the code and using
 * clear accessor.
 */
export class Property {
  private symbol: ts.Symbol

  private checker: ts.TypeChecker

  private property: ts.PropertyDeclaration

  private decorator: ts.Decorator | undefined

  /**
   *
   * @param checker Checker to use to introspect the property.
   * @param property The property to introspect.
   *
   * @throws UnknownDaggerError If the property doesn't have any symbol.
   */
  constructor(checker: ts.TypeChecker, property: ts.PropertyDeclaration) {
    this.checker = checker
    this.property = property

    const propertySymbol = checker.getSymbolAtLocation(property.name)
    if (!propertySymbol) {
      throw new UnknownDaggerError(
        `could not get property symbol: ${property.name.getText()}`,
        {},
      )
    }

    this.symbol = propertySymbol

    this.decorator = ts.getDecorators(property)?.find((d) => {
      if (ts.isCallExpression(d.expression)) {
        return d.expression.expression.getText() === PROPERTY_DECORATOR
      }

      return false
    })
  }

  get name(): string {
    return this.property.name.getText()
  }

  get description(): string {
    return ts.displayPartsToString(
      this.symbol.getDocumentationComment(this.checker),
    )
  }

  /**
   * Return the alias of the property if it has one.
   */
  get alias(): string | undefined {
    if (!this.decorator) {
      return undefined
    }

    const expression = this.decorator.expression as ts.CallExpression
    const aliasArg = expression.arguments[0]

    if (!aliasArg) {
      return undefined
    }

    return JSON.parse(aliasArg.getText().replace(/'/g, '"'))
  }

  /**
   * Return the type of the property in a Dagger TypeDef format.
   */
  get type(): TypeDef<TypeDefKind> {
    if (!this.symbol.valueDeclaration) {
      throw new UnknownDaggerError(
        "could not find symbol value declaration",
        {},
      )
    }

    const type = this.checker.getTypeOfSymbolAtLocation(
      this.symbol,
      this.symbol.valueDeclaration,
    )

    return typeToTypedef(this.checker, type)
  }

  get isExposed(): boolean {
    return this.decorator !== undefined
  }

  // TODO(TomChv): replace with `ToJson` method
  // after the refactor is complete.
  get typeDef(): FieldTypeDef {
    return {
      name: this.name,
      description: this.description,
      alias: this.alias,
      typeDef: this.type,
      isExposed: this.isExposed,
    }
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      alias: this.alias,
      type: this.type,
      isExposed: this.isExposed,
    }
  }
}
