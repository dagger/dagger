export const ERROR_CODES = {
  /**
   * {@link GraphQLRequestError}
   */
  GraphQLRequestError: "D100",

  /**
   * {@link UnknownDaggerError}
   */
  UnknownDaggerError: "D101",

  /**
   * {@link TooManyNestedObjectsError}
   */
  TooManyNestedObjectsError: "D102",

  /**
   * {@link EngineSessionConnectParamsParseError}
   */
  EngineSessionConnectParamsParseError: "D103",

  /**
   * {@link EngineSessionConnectionTimeoutError}
   */
  EngineSessionConnectionTimeoutError: "D104",

  /**
   * {@link EngineSessionError}
   */
  EngineSessionError: "D105",

  /**
   * {@link InitEngineSessionBinaryError}
   */
  InitEngineSessionBinaryError: "D106",

  /**
   * {@link DockerImageRefValidationError}
   */
  DockerImageRefValidationError: "D107",

  /**
   * {@link NotAwaitedRequestError}
   */
  NotAwaitedRequestError: "D108",

  /**
   * (@link ExecError}
   */
  ExecError: "D109",
} as const

type ErrorCodesType = typeof ERROR_CODES
export type ErrorNames = keyof ErrorCodesType
export type ErrorCodes = ErrorCodesType[ErrorNames]

type ErrorNamesMap = { readonly [Key in ErrorNames]: Key }
export const ERROR_NAMES: ErrorNamesMap = (
  Object.keys(ERROR_CODES) as Array<ErrorNames>
).reduce<ErrorNamesMap>(
  (obj, item) => ({ ...obj, [item]: item }),
  {} as ErrorNamesMap
)
