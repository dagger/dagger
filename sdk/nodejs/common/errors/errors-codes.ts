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
   * {@link EngineSessionEOFError}
   */
  EngineSessionEOFError: "D105",

  /**
   * {@link InitEngineSessionBinaryError}
   */
  InitEngineSessionBinaryError: "D106",

  /**
   * {@link DockerImageRefValidationError}
   */
  DockerImageRefValidationError: "D107",
} as const
