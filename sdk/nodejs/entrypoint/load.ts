import { dag, ID } from "../api/client.gen.js"

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
  const trimmedValue = value.slice(1, value.length - 1)

  const [source] = trimmedValue.split(":")

  const [origin, type] = source.split(".")
  if (origin === "core") {
    // Workaround to call get any object that has an id
    // eslint-disable-next-line @typescript-eslint/ban-ts-comment
    // @ts-ignore
    return dag[`load${type}FromID`](trimmedValue as ID)
  } else {
    return trimmedValue
  }
}
