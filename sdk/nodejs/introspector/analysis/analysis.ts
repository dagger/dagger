import ts from "typescript"

import { UnknownDaggerError } from "../../common/errors/UnknownDaggerError.js"
import { ClassMetadata, FunctionMetadata, Metadata } from "./metadata.js"
import { serializeSignature, serializeSymbol } from "./serialize.js"
import { isFunction, isObject } from "./utils.js"

export function analysis(files: string[]): Metadata {
  if (files.length === 0) {
    throw new UnknownDaggerError("no files to introspect found", {})
  }

  // Interpret the given typescript source files
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
      if (ts.isClassDeclaration(node) && node.name) {
        const classMetadata = introspectClass(checker, node)
        if (classMetadata) {
          metadata.classes.push(classMetadata)
        }
      }
    })
  }

  return metadata
}

function introspectClass(
  checker: ts.TypeChecker,
  node: ts.ClassDeclaration
): ClassMetadata | undefined {
  if (!isObject(node) || !node.name) {
    return
  }

  const classSymbol = checker.getSymbolAtLocation(node.name)
  if (!classSymbol) {
    throw new UnknownDaggerError(
      `could not get class symbol: ${node.name.getText()}`,
      {}
    )
  }

  const classMetadata = serializeSymbol(checker, classSymbol)

  const metadata: ClassMetadata = {
    name: classMetadata.name,
    doc: classMetadata.doc,
    methods: [],
  }

  const members = node.members
  members.forEach((member) => {
    if (!member.name) {
      return
    }

    if (ts.isMethodDeclaration(member) && isFunction(member)) {
      metadata.methods.push(introspectMethod(checker, member))
    }
  })

  return metadata
}

function introspectMethod(
  checker: ts.TypeChecker,
  method: ts.MethodDeclaration
): FunctionMetadata {
  const memberSymbol = checker.getSymbolAtLocation(method.name)
  if (!memberSymbol) {
    throw new UnknownDaggerError(
      `could not get method symbol: ${method.name.getText()}`,
      {}
    )
  }

  const memberMetadata = serializeSymbol(checker, memberSymbol)
  const memberSignatures = memberMetadata.type
    .getCallSignatures()
    .map((memberSignature) => serializeSignature(checker, memberSignature))[0]

  return {
    name: memberMetadata.name,
    doc: memberMetadata.doc,
    params: memberSignatures.params.map((signature) => ({
      name: signature.name,
      typeName: signature.typeName,
      doc: signature.doc,
    })),
    returnType: memberSignatures.returnType,
  }
}
