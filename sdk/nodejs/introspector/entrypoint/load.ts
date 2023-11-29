/**
 * Import all given typescript files so that trigger their decorators
 * and register their class and functions inside the Registry.
 *
 * @param files List of files to load.
 */
export async function load(files: string[]): Promise<void> {
  await Promise.all(files.map(async (f) => await import(f)))
}
