import { NetworkProtocol } from "../../../../../api/client.gen.js"
import { func, object } from "../../../../decorators.js"

@object()
export class CoreEnumDefault {
  @func()
  defaultProto(
    proto: NetworkProtocol = NetworkProtocol.Udp,
  ): NetworkProtocol {
    return proto
  }
}
