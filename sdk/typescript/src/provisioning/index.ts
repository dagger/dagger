import { EngineConn } from "./engineconn.js"

export * from "./default.js"
export * from "./engineconn.js"

/**
 * Provision first tries to load the library provisioning, if it fails because we're in the context of a module
 * it tries to load the module provisioning.
 */
export async function loadProvioningLibrary(): Promise<EngineConn> {
  try {
    const { LibraryProvisioning } = await import("./library/index.js")

    return new LibraryProvisioning()
  } catch {
    const { ModuleProvisioning } = await import("./module/index.js")

    return new ModuleProvisioning()
  }
}
