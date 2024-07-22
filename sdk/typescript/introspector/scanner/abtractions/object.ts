import ts from "typescript"

import { UnknownDaggerError } from "../../../common/errors/UnknownDaggerError.js"
import { object } from "../../decorators/decorators.js"
import { Constructor } from "./constructor.js"
import { Method, Methods, isMethodDecorated } from "./method.js"
import { Properties, Property } from "./property.js"

export const OBJECT_DECORATOR = object.name

/**
 * Return true if the given class declaration has the decorator @obj() on
 * top of its declaration.
 * @param object
 */
export function isObjectDecorated(object: ts.ClassDeclaration): boolean {
  return (
    ts.getDecorators(object)?.find((d) => {
      if (ts.isCallExpression(d.expression)) {
        return d.expression.expression.getText() === OBJECT_DECORATOR
      }

      return false
    }) !== undefined
  )
}

export type DaggerObjects = { [name: string]: DaggerObject }

export class DaggerObject {
  private checker: ts.TypeChecker

  private class: ts.ClassDeclaration

  private symbol: ts.Symbol

  public file: ts.SourceFile

  // Preloaded values.
  private _name: string
  private _description: string
  private _classConstructor: Constructor | undefined
  private _methods: Methods
  private _properties: Properties

  /**
   *
   * @param checker The checker to use to introspect the class.
   * @param classDeclaration The class to introspect.
   *
   * @throws UnknownDaggerError If the class doesn't have a name.
   * @throws UnknownDaggerError If the class doesn't have a symbol.
   */
  constructor(
    checker: ts.TypeChecker,
    file: ts.SourceFile,
    classDeclaration: ts.ClassDeclaration,
  ) {
    this.checker = checker
    this.class = classDeclaration
    this.file = file

    if (!classDeclaration.name) {
      throw new UnknownDaggerError(
        `could not introspect class: ${classDeclaration}`,
        {},
      )
    }

    const classSymbol = checker.getSymbolAtLocation(classDeclaration.name)
    if (!classSymbol) {
      throw new UnknownDaggerError(
        `could not get class symbol: ${classDeclaration.name.getText()}`,
        {},
      )
    }

    this.symbol = classSymbol

    // Preload values to optimize introspection.
    this._name = this.loadName()
    this._description = this.loadDescription()
    this._classConstructor = this.loadConstructor()
    this._methods = this.loadMethods()
    this._properties = this.loadProperties()
  }

  get name(): string {
    return this._name
  }

  get description(): string {
    return this._description
  }

  get _constructor(): Constructor | undefined {
    return this._classConstructor
  }

  get methods(): Methods {
    return this._methods
  }

  get properties(): Properties {
    return this._properties
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      constructor: this._constructor,
      methods: this.methods,
      properties: this.properties,
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

  private loadConstructor(): Constructor | undefined {
    const constructor = this.class.members.find((member) => {
      if (ts.isConstructorDeclaration(member)) {
        return true
      }
    })

    if (!constructor) {
      return undefined
    }

    return new Constructor(
      this.checker,
      constructor as ts.ConstructorDeclaration,
    )
  }

  private loadMethods(): Methods {
    return this.class.members
      .filter(
        (member) => ts.isMethodDeclaration(member) && isMethodDecorated(member),
      )
      .reduce((acc: Methods, member) => {
        const method = new Method(this.checker, member as ts.MethodDeclaration)

        acc[method.alias ?? method.name] = method

        return acc
      }, {})
  }

  private loadProperties(): Properties {
    return this.class.members
      .filter((member) => ts.isPropertyDeclaration(member))
      .reduce((acc: Properties, member) => {
        const property = new Property(
          this.checker,
          member as ts.PropertyDeclaration,
        )

        acc[property.alias ?? property.name] = property
        return acc
      }, {})
  }
}
