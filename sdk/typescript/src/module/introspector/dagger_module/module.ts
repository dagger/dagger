import Module from "node:module"
import ts from "typescript"

import { TypeDefKind } from "../../../api/client.gen.js"
import { IntrospectionError } from "../../../common/errors/index.js"
import {
  AST,
  CLIENT_GEN_FILE,
  ResolvedNodeWithSymbol,
} from "../typescript_module/index.js"
import { ENUM_DECORATOR, OBJECT_DECORATOR } from "./decorator.js"
import { DaggerEnum } from "./enum.js"
import { DaggerEnumsBase } from "./enumBase.js"
import { DaggerEnumClass } from "./enumClass.js"
import { DaggerInterface, DaggerInterfaces } from "./interface.js"
import { DaggerObject } from "./object.js"
import { DaggerObjectsBase } from "./objectBase.js"
import { References } from "./reference.js"
import { DaggerTypeObject } from "./typeObject.js"

/**
 * DaggerModule represents a TypeScript module with a set of files
 * with Dagger object conversion and notation.
 *
 * It starts from the module entrypoint (the class named the same as the module) and
 * then recursively resolve every references to other declared identifiers.
 * After resolution, it propagates all the references to the modules declarations
 * and finally it generates the final Dagger module representation.
 */
export class DaggerModule {
  /**
   * An object is either a decorated class or a type alias object.
   * Type alias objects cannot be decorated so they are resolved if referenced in the module.
   *
   * @example
   * ```ts
   * @object()
   * export class MyObject {
   *  @func()
   *  public name: string
   * }
   * ```
   *
   * @example
   * ```ts
   * export type Example = {
   *   name: string
   * }
   * ```
   */
  public objects: DaggerObjectsBase = {}

  /**
   * An enum is typically a native TypeScript enum declared with the `enum` keyword.
   * Decorated classes using `@enumType()` are still supported for backward compatibility.
   *
   * @example
   * ```ts
   * export enum Example {
   *   A = "a",
   *   B = "b",
   * }
   * ```
   */
  public enums: DaggerEnumsBase = {}

  /**
   * An interface is declared using the `interface` keyword.
   *
   * @example
   * ```ts
   * export interface Example {
   *   foo: (): string
   *   asyncFoo: (): Promise<string>
   * }
   * ```
   */
  public interfaces: DaggerInterfaces = {}

  public description: string | undefined

  private references: References = {
    // Float is a special case, it's an alias of number but it serves to declare a float type
    // in the Dagger API.
    // So we auto register it because it will be detected as a referenced type by the introspector.
    float: { kind: TypeDefKind.FloatKind },
  }

  constructor(
    public name: string,
    private userModule: Module[],
    private ast: AST,
  ) {
    const classObjects = this.findClasses()
    for (const classObject of classObjects) {
      // This only applies to cloud. If this is the true main object, it is correct
      // if this is a blueprint, the description will not matter in any situation
      const mainFileContent = classObject.file.getFullText()
      this.description = this.getDescription(mainFileContent)

      const daggerObject = new DaggerObject(classObject.node, this.ast)
      const objectName = classObject.node.name?.getText() || this.name

      this.objects[objectName] = daggerObject
      this.references[objectName] = {
        kind: TypeDefKind.ObjectKind,
        name: objectName,
      }

      this.resolveReferences(daggerObject.getReferences())
      this.propagateReferences()
    }
  }

  /**
   * Find the reference of the module and register it to the module references.
   *
   * To do so, we check the user module to find a corresponding symbol (name) for each of
   * typedef we support.
   * This only applies for:
   * - classes
   * - enums
   * - scalars
   * - interfaces
   *
   * If the reference is an object, a class or an interface, recursively find the references of the object.
   *
   * *Note*: If a class is referenced but not exported and not decorated with `@object()`, we throw an error
   * because we aim to be explicit. (TomChv: Should we change this behaviour?)
   */
  private resolveReferences(references: string[]) {
    if (references.length === 0) {
      return
    }

    for (const reference of references) {
      // If we already know that reference, we don't need to explore it again.
      if (this.references[reference]) {
        continue
      }

      const classRef = this.ast.findResolvedNodeByName(
        reference,
        ts.SyntaxKind.ClassDeclaration,
      )
      if (classRef) {
        if (classRef.file.fileName.endsWith(CLIENT_GEN_FILE)) {
          this.references[reference] = {
            kind: TypeDefKind.ObjectKind,
            name: reference,
          }
          continue
        }

        if (this.ast.isNodeDecoratedWith(classRef.node, OBJECT_DECORATOR)) {
          const daggerObject = new DaggerObject(classRef.node, this.ast)
          this.objects[daggerObject.name] = daggerObject
          this.references[daggerObject.name] = {
            kind: TypeDefKind.ObjectKind,
            name: daggerObject.name,
          }

          this.resolveReferences(daggerObject.getReferences())

          continue
        }

        if (this.ast.isNodeDecoratedWith(classRef.node, ENUM_DECORATOR)) {
          const daggerEnum = new DaggerEnumClass(classRef.node, this.ast)
          this.enums[daggerEnum.name] = daggerEnum
          this.references[daggerEnum.name] = {
            kind: TypeDefKind.EnumKind,
            name: daggerEnum.name,
          }

          // There should be no references in enums.
          continue
        }

        throw new IntrospectionError(
          `class ${reference} in ${AST.getNodePosition(classRef.node)} is used by the module but not exposed with a dagger decorator.`,
        )
      }

      const enumRef = this.ast.findResolvedNodeByName(
        reference,
        ts.SyntaxKind.EnumDeclaration,
      )
      if (enumRef) {
        if (enumRef.file.fileName.endsWith(CLIENT_GEN_FILE)) {
          this.references[reference] = {
            kind: TypeDefKind.EnumKind,
            name: reference,
          }
          continue
        }

        // Typescript enum declaration cannot be decorated, so we don't check it.
        const daggerEnum = new DaggerEnum(enumRef.node, this.ast)
        this.enums[daggerEnum.name] = daggerEnum
        this.references[daggerEnum.name] = {
          kind: TypeDefKind.EnumKind,
          name: daggerEnum.name,
        }

        // There should be no reference in enums.
        continue
      }

      const interfaceRef = this.ast.findResolvedNodeByName(
        reference,
        ts.SyntaxKind.InterfaceDeclaration,
      )
      if (interfaceRef) {
        const daggerInterface = new DaggerInterface(interfaceRef.node, this.ast)
        this.interfaces[daggerInterface.name] = daggerInterface
        this.references[daggerInterface.name] = {
          kind: TypeDefKind.InterfaceKind,
          name: daggerInterface.name,
        }
        this.resolveReferences(daggerInterface.getReferences())
        continue
      }

      const typeAliasRef = this.ast.findResolvedNodeByName(
        reference,
        ts.SyntaxKind.TypeAliasDeclaration,
      )
      if (typeAliasRef) {
        // The resolution is to big so we split it in a sub function.
        this.resolveTypeAlias(reference, typeAliasRef)

        continue
      }

      // Handle primitives here
      if (reference === "String") {
        throw new IntrospectionError(
          `Use of primitive 'String' type detected, please use 'string' instead.`,
        )
      }

      if (reference === "Boolean") {
        throw new IntrospectionError(
          `Use of primitive 'Boolean' type detected, please use 'boolean' instead.`,
        )
      }

      if (reference === "Number") {
        throw new IntrospectionError(
          `Use of primitive 'Number' type detected, please use 'number' instead.`,
        )
      }

      throw new IntrospectionError(
        `could not resolve type reference for ${reference}.`,
      )
    }
  }

  /**
   * Resolve type alias to the corresponding TypeDef.
   * A type might refer to anything typeable in TypeScript but right now we supports:
   * - `type Example = string`
   * - `type Example = { prop: string}`
   * - `type Example = number`
   * - `type Example = boolean`
   * - `type Example = void`
   *
   * If the reference is an object, we recursively resolve its references.
   * If the type cannot be resolved or is not supported, we throw an error.
   */
  private resolveTypeAlias(
    reference: string,
    typeAlias: ResolvedNodeWithSymbol<ts.SyntaxKind.TypeAliasDeclaration>,
  ) {
    const type = this.ast.getTypeFromTypeAlias(typeAlias.node)

    if (type.flags & ts.TypeFlags.String) {
      this.references[reference] = { kind: TypeDefKind.StringKind }

      return
    }

    if (type.flags & ts.TypeFlags.Number) {
      this.references[reference] = { kind: TypeDefKind.IntegerKind }

      return
    }

    if (type.flags & ts.TypeFlags.Boolean) {
      this.references[reference] = { kind: TypeDefKind.BooleanKind }

      return
    }

    if (type.flags & ts.TypeFlags.Void) {
      this.references[reference] = { kind: TypeDefKind.VoidKind }

      return
    }

    // Scalar are defined with string intersection such as `type MyScalar = string & { __MyScalar: never }`
    if (
      type.flags & ts.TypeFlags.Intersection ||
      type.flags & ts.TypeFlags.Union
    ) {
      this.references[reference] = {
        kind: TypeDefKind.ScalarKind,
        name: reference,
      }

      return
    }

    if (type.flags & ts.TypeFlags.Object) {
      if (typeAlias.file.fileName.endsWith(CLIENT_GEN_FILE)) {
        this.references[reference] = {
          kind: TypeDefKind.ObjectKind,
          name: reference,
        }

        return
      }

      const daggerObject = new DaggerTypeObject(typeAlias.node, this.ast)
      this.objects[daggerObject.name] = daggerObject
      this.references[daggerObject.name] = {
        kind: TypeDefKind.ObjectKind,
        name: daggerObject.name,
      }

      this.resolveReferences(daggerObject.getReferences())

      return
    }

    throw new IntrospectionError(
      `could not resolve type reference for ${reference} at ${AST.getNodePosition(typeAlias.node)}`,
    )
  }

  /**
   * Find the classes in the AST. Returns only our main class if it exists
   */
  private findClasses(): ResolvedNodeWithSymbol<ts.SyntaxKind.ClassDeclaration>[] {
    const allClassDeclarations = this.ast.findAllDeclarations(
      ts.SyntaxKind.ClassDeclaration,
    )

    const allClasses: ResolvedNodeWithSymbol<ts.SyntaxKind.ClassDeclaration>[] =
      []
    for (const classDecl of allClassDeclarations) {
      const convertedDecl =
        classDecl as ResolvedNodeWithSymbol<ts.SyntaxKind.ClassDeclaration>

      // Check if the class matches this.name and return only that if so
      if (
        convertedDecl.node.name &&
        convertedDecl.node.name.getText() === this.name
      ) {
        return [convertedDecl]
      }

      // or we return all classes decorated with @object
      if (this.ast.isNodeDecoratedWith(classDecl.node, OBJECT_DECORATOR)) {
        allClasses.push(convertedDecl)
      }
    }
    return allClasses
  }

  /**
   * Recursively propagate references to all objects properties and functions.
   */
  private propagateReferences() {
    for (const object of Object.values(this.objects)) {
      object.propagateReferences(this.references)
    }

    for (const interface_ of Object.values(this.interfaces)) {
      interface_.propagateReferences(this.references)
    }
  }

  /**
   * Get the top level comment of the file that contains the module entrypoint.
   */
  private getDescription(sourceFileContent: string): string | undefined {
    const regex = /^(?!.*import)[\s]*\/\*\*([\s\S]*?)\*\//
    const match = sourceFileContent.match(regex)

    if (!match) {
      return undefined
    }

    const comment = match[1]
      .split("\n")
      .map((line) => line.replace(/^\s*\*\s?/, ""))
      .join("\n")

    return comment.trim()
  }

  toJSON() {
    return {
      name: this.name,
      description: this.description,
      objects: this.objects,
      enums: this.enums,
      interfaces: this.interfaces,
    }
  }
}
