import ts from "typescript"

import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { daggerEnum } from "../../decorators/decorators.js"
import { DaggerEnumValue, DaggerEnumValues } from "./enumValue.js"

export const ENUM_DECORATOR = daggerEnum.name

/**
 * Return true if the given class declaration has the decorator @enum() on
 * top of its declaration.
 */
export function isEnumDecorated(object: ts.ClassDeclaration): boolean {
  return (
    ts.getDecorators(object)?.find((d) => {
      if (ts.isCallExpression(d.expression)) {
        return d.expression.expression.getText() === ENUM_DECORATOR
      }

      return false
    }) !== undefined
  )
}

export type DaggerEnums = { [name: string]: DaggerEnum }

export class DaggerEnum {
  private checker: ts.TypeChecker

  private enumClass: ts.ClassDeclaration

  private symbol: ts.Symbol

  private file: ts.SourceFile

  private _name: string

  private _description: string

  private _values: DaggerEnumValues

  constructor(
    checker: ts.TypeChecker,
    file: ts.SourceFile,
    enumClassDeclaration: ts.ClassDeclaration,
  ) {
    this.checker = checker
    this.enumClass = enumClassDeclaration
    this.file = file

    if (!enumClassDeclaration.name) {
      throw new UnknownDaggerError(
        `could not introspect enum class: ${enumClassDeclaration}`,
        {},
      )
    }

    const enumClassSymbol = checker.getSymbolAtLocation(
      enumClassDeclaration.name,
    )
    if (!enumClassSymbol) {
      throw new UnknownDaggerError(
        `could not get enum class symbol: ${enumClassDeclaration.name.getText()}`,
        {},
      )
    }

    this.symbol = enumClassSymbol

    // Preload definition to optimize the introspection.
    this._name = this.loadName()
    this._description = this.loadDescription()
    this._values = this.loadEnumValues()
  }

  get name(): string {
    return this._name
  }

  get description(): string {
    return this._description
  }

  get values(): DaggerEnumValues {
    return this._values
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      values: this._values,
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

  private loadEnumValues(): DaggerEnumValues {
    return this.enumClass.members
      .filter((member) => ts.isPropertyDeclaration(member))
      .reduce((acc, member) => {
        const value = new DaggerEnumValue(
          this.checker,
          member as ts.PropertyDeclaration,
        )

        return { ...acc, [value.name]: value }
      }, {})
  }
}
