import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { field, func } from "../../decorators/decorators.js"
import { TypeDef } from "../typeDefs.js"
import { typeToTypedef } from "./typeToTypedef.js"

const DEPRECATED_PROPERTY_DECORATOR = field.name
const PROPERTY_DECORATOR = func.name

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

  // Preloaded values.
  private _name: string
  private _description: string
  private _alias: string | undefined
  private _type: TypeDef<TypeDefKind>
  private _isExposed: boolean

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
        return (
          d.expression.expression.getText() === PROPERTY_DECORATOR ||
          d.expression.expression.getText() === DEPRECATED_PROPERTY_DECORATOR
        )
      }

      return false
    })

    // Preload values to optimize introspection
    this._name = this.loadName()
    this._description = this.loadDescription()
    this._alias = this.loadAlias()
    this._type = this.loadType()
    this._isExposed = this.loadIsExposed()
  }

  get name(): string {
    return this._name
  }

  get description(): string {
    return this._description
  }

  /**
   * Return the alias of the property if it has one.
   */
  get alias(): string | undefined {
    return this._alias
  }

  /**
   * Return the type of the property in a Dagger TypeDef format.
   */
  get type(): TypeDef<TypeDefKind> {
    return this._type
  }

  get isExposed(): boolean {
    return this._isExposed
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

  private loadName(): string {
    return this.symbol.getName()
  }

  private loadDescription(): string {
    return ts.displayPartsToString(
      this.symbol.getDocumentationComment(this.checker),
    )
  }

  private loadAlias(): string | undefined {
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

  private loadType(): TypeDef<TypeDefKind> {
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

  private loadIsExposed(): boolean {
    return this.decorator !== undefined
  }
}
