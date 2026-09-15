import { object, func, NetworkProtocol } from "@dagger.io/dagger";

@object()
export class Test {
  @func()
  fromProto(Proto: NetworkProtocol): string {
    return Proto as string;
  }

  @func()
  fromProtoDefault(Proto: NetworkProtocol = NetworkProtocol.Udp): string {
    return Proto as string;
  }

  @func()
  toProto(Proto: string): NetworkProtocol {
    return Proto as NetworkProtocol;
  }
}
