import ts from "typescript"

import { UnknownDaggerError } from "../../common/errors/UnknownDaggerError.js"
import { serializeSignature, serializeSymbol } from "./serialize.js"
import {
  ClassTypeDef,
  ConstructorTypeDef,
  FieldTypeDef,
  FunctionArg,
  FunctionTypedef,
} from "./typeDefs.js"
import {
  getAlias,
  isFunction,
  isMainObject,
  isObject,
  isOptional,
  isPublicProperty,
  typeNameToTypedef,
} from "./utils.js"

export type ScanResult = {
  module: {
    description?: string
  }
  classes: { [name: string]: ClassTypeDef }
  functions: { [name: string]: FunctionTypedef }
}

/**
 * Scan the list of TypeScript File using the TypeScript compiler API.
 *
 * This function introspect files and returns metadata of their class and
 * functions that should be exposed to the Dagger API.
 *
 * WARNING(28/11/23): This does NOT include arrow style function.
 *
 * @param files List of TypeScript files to introspect.
 * @param moduleName The name of the module to introspect.
 */
export function scan(files: string[], moduleName = ""): ScanResult {
  if (files.length === 0) {
    throw new UnknownDaggerError("no files to introspect found", {})
  }

  // Interpret the given typescript source files.
  const program = ts.createProgram(files, { experimentalDecorators: true })
  const checker = program.getTypeChecker()

  const metadata: ScanResult = {
    module: {},
    classes: {},
    functions: {},
  }

  for (const file of program.getSourceFiles()) {
    // Ignore type declaration files.
    if (file.isDeclarationFile) {
      continue
    }

    ts.forEachChild(file, (node) => {
      // Handle class
      if (ts.isClassDeclaration(node) && isObject(node)) {
        const classTypeDef = introspectClass(checker, node)

        if (isMainObject(classTypeDef.name, moduleName)) {
          metadata.module.description = introspectTopLevelComment(file)
        }

        metadata.classes[classTypeDef.name] = classTypeDef
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
): ClassTypeDef {
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
  const { name, description } = serializeSymbol(checker, classSymbol)

  // Create metadata object.
  const metadata: ClassTypeDef = {
    name,
    description,
    constructor: undefined,
    fields: {},
    methods: {},
  }

  // Loop through all members in the class to get their metadata.
  node.members.forEach((member) => {
    // Handle constructor
    if (ts.isConstructorDeclaration(member)) {
      metadata.constructor = introspectConstructor(checker, member)
    }

    // Handle method from the class.
    if (ts.isMethodDeclaration(member) && isFunction(member)) {
      const fctTypeDef = introspectMethod(checker, member)

      metadata.methods[fctTypeDef.alias ?? fctTypeDef.name] = fctTypeDef
    }

    // Handle public properties from the class.
    if (ts.isPropertyDeclaration(member)) {
      const fieldTypeDef = introspectProperty(checker, member)

      metadata.fields[fieldTypeDef.alias ?? fieldTypeDef.name] = fieldTypeDef
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
): FieldTypeDef {
  const propertySymbol = checker.getSymbolAtLocation(property.name)
  if (!propertySymbol) {
    throw new UnknownDaggerError(
      `could not get property symbol: ${property.name.getText()}`,
      {}
    )
  }

  const { name, typeName, description } = serializeSymbol(
    checker,
    propertySymbol
  )

  return {
    name,
    description,
    alias: getAlias(property, "field"),
    typeDef: typeNameToTypedef(typeName),
    isExposed: isPublicProperty(property),
  }
}

/**
 * Introspect the constructor of the class and return its metadata.
 */
function introspectConstructor(
  checker: ts.TypeChecker,
  constructor: ts.ConstructorDeclaration
): ConstructorTypeDef {
  const args = constructor.parameters.reduce(
    (acc: { [name: string]: FunctionArg }, param) => {
      const paramSymbol = checker.getSymbolAtLocation(param.name)
      if (!paramSymbol) {
        throw new UnknownDaggerError(
          `could not get constructor param: ${param.name.getText()}`,
          {}
        )
      }

      const { name, typeName, description } = serializeSymbol(
        checker,
        paramSymbol
      )
      const { optional, defaultValue } = isOptional(paramSymbol)

      acc[name] = {
        name,
        description,
        typeDef: typeNameToTypedef(typeName),
        optional,
        defaultValue,
        isVariadic: false,
      }

      return acc
    },
    {}
  )

  return { args }
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
  method: ts.MethodDeclaration
): FunctionTypedef {
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
    description: methodMetadata.description,
    alias: getAlias(method, "func"),
    args: methodSignature.params.reduce(
      (
        acc: { [name: string]: FunctionArg },
        { name, typeName, description, optional, defaultValue, isVariadic }
      ) => {
        acc[name] = {
          name,
          typeDef: typeNameToTypedef(typeName),
          description,
          optional,
          defaultValue,
          isVariadic,
        }

        return acc
      },
      {}
    ),
    returnType: typeNameToTypedef(methodSignature.returnType),
  }
}

/**
 * Return the content of the top level comment of the given file.
 *
 * @param file The file to introspect.
 */
function introspectTopLevelComment(file: ts.SourceFile): string | undefined {
  const firstStatement = file.statements[0]
  if (!firstStatement) {
    return undefined
  }

  const commentRanges = ts.getLeadingCommentRanges(
    file.getFullText(),
    firstStatement.pos
  )
  if (!commentRanges || commentRanges.length === 0) {
    return undefined
  }

  const commentRange = commentRanges[0]
  const comment = file
    .getFullText()
    .substring(commentRange.pos, commentRange.end)
    .split("\n")
    .slice(1, -1) // Remove start and ending comments characters `/** */`
    .map((line) => line.replace("*", "").trim()) // Remove leading * and spaces
    .join("\n")

  return comment
}
