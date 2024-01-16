import { dag } from "../api/client.gen.js"

/**
 * Import all given typescript files so that trigger their decorators
 * and register their class and functions inside the Registry.
 *
 * @param files List of files to load.
 */
export async function load(files: string[]): Promise<void> {
  await Promise.all(files.map(async (f) => await import(f)))
}

/**
 * Load argument as Dagger type.
 *
 * This function remove the quote from the identifier and checks
 * if it's a Dagger type, if it is, it loads it according to
 * its type.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export async function loadArg(value: string): Promise<any> {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let parsedValue: any

  const isString = (): boolean =>
    (value.startsWith('"') && value.endsWith('"')) ||
    (value.startsWith(`'`) && value.endsWith(`'`))
  const isArray = (): boolean => value.startsWith("[") && value.endsWith("]")

  // Apply JSON parse to parse array or string if the value is wrapped into a string or array
  if (isString() || isArray()) {
    parsedValue = JSON.parse(value)
  } else {
    parsedValue = value
  }

  // If it's a string, it might contain an identifier to load, or it might be a hidden array
  if (typeof parsedValue === "string") {
    const [source] = parsedValue.split(":")

    const [origin, type] = source.split(".")
    if (origin === "core") {
      // Workaround to call get any object that has an id
      // eslint-disable-next-line @typescript-eslint/ban-ts-comment
      // @ts-ignore
      return dag[`load${type}FromID`](parsedValue)
    }
  }

  return parsedValue
}
