import {
  CacheVolumeID,
  ContainerID,
  dag,
  DirectoryID,
  FileID,
  SecretID,
  ServiceID,
  SocketID,
  TypeDefID,
} from "../api/client.gen.js"

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

  switch (source) {
    case "core.Directory":
      return dag.loadDirectoryFromID(trimmedValue as DirectoryID)
    case "core.File":
      return dag.loadFileFromID(trimmedValue as FileID)
    case "core.Container":
      return dag.loadContainerFromID(trimmedValue as ContainerID)
    case "core.Socket":
      return dag.loadSocketFromID(trimmedValue as SocketID)
    case "core.CacheVolume":
      return dag.loadCacheVolumeFromID(trimmedValue as CacheVolumeID)
    case "core.Secret":
      return dag.loadSecretFromID(trimmedValue as SecretID)
    case "core.Service":
      return dag.loadServiceFromID(trimmedValue as ServiceID)
    case "core.TypeDef":
      return dag.loadTypeDefFromID(trimmedValue as TypeDefID)
    default:
      return trimmedValue
  }
}
