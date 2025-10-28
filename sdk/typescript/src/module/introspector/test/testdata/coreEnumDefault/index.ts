import { func, object, NetworkProtocol } from "@dagger.io/dagger"

@object()
export class CoreEnumDefault {
  @func()
  defaultProto(
    proto: NetworkProtocol = NetworkProtocol.Udp,
  ): NetworkProtocol {
    return proto
  }
}
