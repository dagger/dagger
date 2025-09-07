
/**
 * Transport layer network protocol associated to a port.
 */
export enum NetworkProtocol {
  Tcp = "TCP",
  Udp = "UDP",
}

/**
 * Utility function to convert a NetworkProtocol value to its name so
 * it can be uses as argument to call an exposed function.
 */
function NetworkProtocolValueToName(value: NetworkProtocol): string {
  switch (value) {
    case NetworkProtocol.Tcp:
      return "TCP"
    case NetworkProtocol.Udp:
      return "UDP"
    default:
      return value
  }
}

/**
 * Utility function to convert a NetworkProtocol name to its value so
 * it can be properly used inside the module runtime.
 */
function NetworkProtocolNameToValue(name: string): NetworkProtocol {
  switch (name) {
    case "TCP":
      return NetworkProtocol.Tcp
    case "UDP":
      return NetworkProtocol.Udp
    default:
      return name as NetworkProtocol
  }
}