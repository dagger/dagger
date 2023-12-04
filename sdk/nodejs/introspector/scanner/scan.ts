import ts from "typescript"

import { UnknownDaggerError } from "../../common/errors/UnknownDaggerError.js"
import {
  ClassMetadata,
  FunctionMetadata,
  Metadata,
  PropertyMetadata,
} from "./metadata.js"
import { serializeSignature, serializeSymbol } from "./serialize.js"
import { isFunction, isObject, isPublicProperty } from "./utils.js"

/**
 * Scan the list of Typescript File using the Typescript compiler API.
 *
 * This function introspect files and returns metadata of their class and
 * functions that should be exposed to the Dagger API.
 *
 * WARNING(28/11/23): This does NOT include arrow style function.
 *
 * @param files List of Typescript files to introspect.
 */
export function scan(files: string[]): Metadata {
  if (files.length === 0) {
    throw new UnknownDaggerError("no files to introspect found", {})
  }

  // Interpret the given typescript source files.
  const program = ts.createProgram(files, { experimentalDecorators: true })
  const checker = program.getTypeChecker()

  const metadata: Metadata = {
    classes: [],
    functions: [],
  }

  for (const file of program.getSourceFiles()) {
    // Ignore type declaration files.
    if (file.isDeclarationFile) {
      continue
    }

    ts.forEachChild(file, (node) => {
      // Handle class
      if (ts.isClassDeclaration(node) && isObject(node)) {
        metadata.classes.push(introspectClass(checker, node))
      }
    })
  }

  return metadata
}

/**
 * Introspect a class and return its metadata.
 *
 * This function goes throw all class' method that have the @fct decorator
 * and all its public properties.
 *
 * This function throws an error if it cannot read its symbol.
 *
 * @param checker The typescript compiler checker.
 * @param node The class to check.
 */
function introspectClass(
  checker: ts.TypeChecker,
  node: ts.ClassDeclaration
): ClassMetadata {
  // Throw error if node.name is undefined because we cannot scan its symbol.
  if (!node.name) {
    throw new UnknownDaggerError(`could not introspect class: ${node}`, {})
  }

  // Retrieve class symbol.
  const classSymbol = checker.getSymbolAtLocation(node.name)
  if (!classSymbol) {
    throw new UnknownDaggerError(
      `could not get class symbol: ${node.name.getText()}`,
      {}
    )
  }

  // Serialize class symbol to extract name and doc.
  const { name, doc } = serializeSymbol(checker, classSymbol)

  // Create metadata object.
  const metadata: ClassMetadata = {
    name,
    doc,
    properties: [],
    methods: [],
  }

  // Loop through all members in the class to get their metadata.
  node.members.forEach((member) => {
    if (!member.name) {
      return
    }

    // Handle method from the class.
    if (ts.isMethodDeclaration(member) && isFunction(member)) {
      metadata.methods.push(introspectMethod(checker, member))
    }

    // Handle public properties from the class.
    if (ts.isPropertyDeclaration(member) && isPublicProperty(member)) {
      // Handle properties
      metadata.properties.push(introspectProperty(checker, member))
    }
  })

  return metadata
}

/**
 * Introspect a property from a class and return its metadata.
 *
 * This function throws an error if it cannot retrieve the property symbols.
 *
 * @param checker The typescript compiler checker.
 * @param property The method to check.
 */
function introspectProperty(
  checker: ts.TypeChecker,
  property: ts.PropertyDeclaration
): PropertyMetadata {
  const propertySymbol = checker.getSymbolAtLocation(property.name)
  if (!propertySymbol) {
    throw new UnknownDaggerError(
      `could not get property symbol: ${property.name.getText()}`,
      {}
    )
  }

  const { name, typeName, doc } = serializeSymbol(checker, propertySymbol)

  return { name, typeName, doc }
}

/**
 * Introspect a method from a class and return its metadata.
 *
 * This function first retrieve the symbol of the function signature and then
 * loop on its parameters to get their metadata.
 *
 * This function throws an error if it cannot retrieve the method symbols.
 *
 * @param checker The typescript compiler checker.
 * @param method The method to check.
 */
function introspectMethod(
  checker: ts.TypeChecker,
  method: ts.MethodDeclaration | ts.ArrowFunction
): FunctionMetadata {
  const methodSymbol = checker.getSymbolAtLocation(method.name)
  if (!methodSymbol) {
    throw new UnknownDaggerError(
      `could not get method symbol: ${method.name.getText()}`,
      {}
    )
  }

  const methodMetadata = serializeSymbol(checker, methodSymbol)
  const methodSignature = methodMetadata.type
    .getCallSignatures()
    .map((methodSignature) => serializeSignature(checker, methodSignature))[0]

  return {
    name: methodMetadata.name,
    doc: methodMetadata.doc,
    params: methodSignature.params.map(
      ({ name, typeName, doc, optional, defaultValue }) => ({
        name,
        typeName,
        doc,
        optional,
        defaultValue,
      })
    ),
    returnType: methodSignature.returnType,
  }
}
