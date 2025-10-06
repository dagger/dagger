import ts from "typescript"

export type DeclarationsMap = {
  [ts.SyntaxKind.ClassDeclaration]: ts.ClassDeclaration
  [ts.SyntaxKind.MethodDeclaration]: ts.MethodDeclaration
  [ts.SyntaxKind.PropertyDeclaration]: ts.PropertyDeclaration
  [ts.SyntaxKind.FunctionDeclaration]: ts.FunctionDeclaration
  [ts.SyntaxKind.EnumDeclaration]: ts.EnumDeclaration
  [ts.SyntaxKind.InterfaceDeclaration]: ts.InterfaceDeclaration
  [ts.SyntaxKind.TypeAliasDeclaration]: ts.TypeAliasDeclaration
}

export type Declarations<T extends ts.SyntaxKind> =
  T extends keyof DeclarationsMap ? DeclarationsMap[T] : ts.Node

export const isDeclarationOf: {
  [K in keyof DeclarationsMap]: (node: ts.Node) => node is DeclarationsMap[K]
} = {
  [ts.SyntaxKind.ClassDeclaration]: ts.isClassDeclaration,
  [ts.SyntaxKind.MethodDeclaration]: ts.isMethodDeclaration,
  [ts.SyntaxKind.PropertyDeclaration]: ts.isPropertyDeclaration,
  [ts.SyntaxKind.FunctionDeclaration]: ts.isFunctionDeclaration,
  [ts.SyntaxKind.EnumDeclaration]: ts.isEnumDeclaration,
  [ts.SyntaxKind.InterfaceDeclaration]: ts.isInterfaceDeclaration,
  [ts.SyntaxKind.TypeAliasDeclaration]: ts.isTypeAliasDeclaration,
}
