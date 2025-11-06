
export enum Mode {

  /**
   * @deprecated Use ModeV2 instead.
   */
  Value = "VALUE",
}

/**
 * Utility function to convert a Mode value to its name so
 * it can be uses as argument to call a exposed function.
 */
function ModeValueToName(value: Mode): string {
  switch (value) {
    case Mode.Value:
      return "VALUE"
    default:
      return value
  }
}

/**
 * Utility function to convert a Mode name to its value so
 * it can be properly used inside the module runtime.
 */
function ModeNameToValue(name: string): Mode {
  switch (name) {
    case "VALUE":
      return Mode.Value
    default:
      return name as Mode
  }
}