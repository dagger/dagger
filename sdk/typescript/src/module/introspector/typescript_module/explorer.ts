import Module from "node:module"

export function findModuleByExportedName(
  name: string,
  modules: Module[],
): Module | undefined {
  for (const module of modules) {
    if (module[name as keyof typeof module]) {
      return module
    }
  }
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function getValueByExportedName(name: string, modules: Module[]): any {
  for (const module of modules) {
    if (module[name as keyof typeof module]) {
      return module[name as keyof typeof module]
    }
  }

  return undefined
}
