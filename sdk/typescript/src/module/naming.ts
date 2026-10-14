/**
 * Schema names the engine gives a module's own declarations.
 *
 * The runtime registers TypeScript names (`getUrl`) and the engine turns them
 * into schema names. Calls the runtime builds itself, against its own module's
 * interfaces, have to use those schema names.
 *
 * Modules at engine version v1.0.0 and later have their names formatted by the
 * engine's naming rules, which know acronyms (`URL`, `HTTP`) a plain case
 * conversion can't: `getUrl` is `getURL` in the schema. Rather than duplicate
 * those rules here, the runtime asks the engine to format its names
 * (`Query.formatIdentifiers`), with the module's own engine version, since
 * that's what the runtime's session is served at. Older modules don't have
 * that API, and keep the names they always had.
 */
import { dag } from "../api/client.gen.js"

/**
 * A casing `formatIdentifiers` formats names in. Passed by value so this also
 * works with clients generated before the `Casing` enum existed.
 */
export type NameCasing = "CAMEL" | "PASCAL" | "SNAKE"

/**
 * Formats names in a casing, returning them in input order.
 */
export type NameFormatter = (
  names: string[],
  casing: NameCasing,
) => Promise<string[]>

/**
 * The parts of an interface declared by the module that calls through it
 * name: its functions and their arguments.
 */
export type InterfaceNames = {
  name: string
  functions: { name: string; args: string[] }[]
}

/**
 * The schema names of a module's interfaces and their members.
 *
 * Names not found here fall back to what the caller used before the engine's
 * naming rules, which is also what an empty instance gives.
 */
export class SchemaNames {
  constructor(
    /** Function and argument names, from the name the runtime registers. */
    readonly fields: Record<string, string> = {},
    /** Interface type names, from the interface's declared name. */
    readonly types: Record<string, string> = {},
  ) {}

  /**
   * The schema name of a function or argument of a module interface.
   */
  fieldName(name: string): string {
    return this.fields[name] ?? name
  }

  /**
   * The schema name of an interface declared in the module, or `legacy` if
   * the engine doesn't name it with its naming rules.
   */
  interfaceName(name: string, legacy: string): string {
    return this.types[name] ?? legacy
  }
}

/**
 * Ask the engine for the schema names of the module's interfaces.
 *
 * Returns undefined if the engine doesn't format the module's names with its
 * naming rules (modules older than v1.0.0), or there is nothing to name.
 */
export async function resolveSchemaNames(
  moduleName: string,
  interfaces: InterfaceNames[],
  formatter?: NameFormatter,
): Promise<SchemaNames | undefined> {
  if (interfaces.length === 0) {
    return undefined
  }

  if (!formatter) {
    if (!(await supportsIdentifiers())) {
      return undefined
    }
    formatter = formatNames
  }

  const names = [
    ...new Set(
      interfaces.flatMap((iface) =>
        iface.functions.flatMap((fn) => [fn.name, ...fn.args]),
      ),
    ),
  ]
    .filter((name) => name !== "")
    .sort()
  const ifaceNames = interfaces.map((iface) => iface.name)

  const camel = names.length > 0 ? await formatter(names, "CAMEL") : []
  const snake = await formatter([moduleName, ...ifaceNames], "SNAKE")
  const pascal = await formatter(
    [...ifaceNames, ...ifaceNames.map((name) => `${moduleName}_${name}`)],
    "PASCAL",
  )

  const fields: Record<string, string> = {}
  names.forEach((name, i) => {
    fields[name] = camel[i]
  })

  // Like the engine, namespace an interface with the module's name unless
  // its name already starts with the module's name, comparing words.
  const [moduleSnake, ...ifaceSnakes] = snake
  const types: Record<string, string> = {}
  ifaceNames.forEach((name, i) => {
    const ifaceSnake = ifaceSnakes[i]
    const prefixed =
      ifaceSnake === moduleSnake || ifaceSnake.startsWith(`${moduleSnake}_`)
    types[name] = prefixed ? pascal[i] : pascal[ifaceNames.length + i]
  })

  return new SchemaNames(fields, types)
}

/**
 * Whether the session formats names with the engine's naming rules.
 *
 * The identifier API only exists in the API of engine version v1.0.0 and
 * later, the same version from which the engine names module declarations
 * with its naming rules. The runtime's session is served at the module's
 * engine version, so the field being there is the gate. The client can't
 * tell: the runtime library may bundle a client newer than the module.
 */
export async function supportsIdentifiers(): Promise<boolean> {
  try {
    const res = await dag.getGQLClient().request<{
      __type: { fields: { name: string }[] | null } | null
    }>(`{ __type(name: "Query") { fields(includeDeprecated: true) { name } } }`)
    return (
      res.__type?.fields?.some((f) => f.name === "formatIdentifiers") ?? false
    )
  } catch {
    return false
  }
}

/**
 * Format names with the engine's naming rules.
 *
 * A name the engine can't parse (non-ASCII, say) fails the whole batch. The
 * engine leaves such a name unchanged when it names module declarations, so
 * do the same, formatting the rest one by one.
 */
export async function formatNames(
  names: string[],
  casing: NameCasing,
): Promise<string[]> {
  // Only in clients generated for v1.0.0 and later: look it up when called.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const client = dag as any
  try {
    return await client.formatIdentifiers(names, casing)
  } catch {
    // fall back to formatting one by one
  }

  const out: string[] = []
  for (const name of names) {
    try {
      out.push(await client.identifier(name).format(casing))
    } catch {
      out.push(name)
    }
  }
  return out
}
