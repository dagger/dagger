export type ConnectOpts = {
  Workdir?: string
  LogOutput?: unknown
}

export type CallbackFct = (client: unknown) => Promise<void>

export function connection(
  cb: () => Promise<void>,
  opts?: ConnectOpts,
): Promise<void>
export function connect(cb: CallbackFct, opts?: ConnectOpts): Promise<void>
export class Context {}

/**
 * Common base class of every generated API class (Client, Container,
 * dep-contributed types, ...). Lives in the runtime — not in
 * client.gen.ts — so per-dep generated files can `extends BaseClient`
 * without creating an ESM cycle through client.gen.ts.
 */
export class BaseClient {
  constructor(ctx?: Context)
}

export type ArgumentOptions = {
  defaultPath?: string
  ignore?: string[]
}

export function func(alias?: string): MethodDecorator
export function check(): MethodDecorator
export function generate(): MethodDecorator
export function argument(opts?: ArgumentOptions): ParameterDecorator
export function object(): ClassDecorator
export function field(alias?: string): PropertyDecorator
export function enumType(): ClassDecorator
export function entrypoint(files: string[]): Promise<void>
export function getRegisteredClass(
  name: string,
): { new (...args: unknown[]): unknown } | undefined

export function getTracer(name?: string): unknown
