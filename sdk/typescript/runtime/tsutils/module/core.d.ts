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

export type ArgumentOptions = {
  defaultPath?: string
  ignore?: string[]
}

export function func(alias?: string): MethodDecorator
export function check(): MethodDecorator
export function argument(opts?: ArgumentOptions): ParameterDecorator
export function object(): ClassDecorator
export function field(alias?: string): PropertyDecorator
export function enumType(): ClassDecorator
export function entrypoint(files: string[]): Promise<void>

export function getTracer(name?: string): unknown
