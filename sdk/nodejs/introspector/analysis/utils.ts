import ts from "typescript"

export function isObject(node: ts.ClassDeclaration): boolean {
  return (
    ts.getDecorators(node)?.find((d) => d.expression.getText() === "object") !==
    undefined
  )
}

export function isFunction(node: ts.MethodDeclaration): boolean {
  return (
    ts.getDecorators(node)?.find((d) => d.expression.getText() === "fct") !==
    undefined
  )
}
