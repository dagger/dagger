export type ConnectOpts = Record<string, unknown>
export type CallbackFct = (...args: any[]) => any

export function connection(cb: CallbackFct, opts?: ConnectOpts): any
export function connect(opts?: ConnectOpts): any
export class Context {}

export function func(): MethodDecorator
export function check(): MethodDecorator
export function argument(): ParameterDecorator
export function object(): ClassDecorator
export function field(): PropertyDecorator
export function enumType(): ClassDecorator
export function entrypoint(): MethodDecorator

export function getTracer(name?: string): any
