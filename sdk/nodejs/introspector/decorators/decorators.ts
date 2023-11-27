export function fct(
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  target: any,
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  propertyKey: string,
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  descriptor: PropertyDescriptor
  // eslint-disable-next-line @typescript-eslint/no-empty-function
) {}

export function object<T extends { new (...args: unknown[]): unknown }>(
  constructor: T
) {
  return constructor
}
