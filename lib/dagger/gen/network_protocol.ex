# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.NetworkProtocol do
  @moduledoc "Transport layer network protocol associated to a port."
  @type t() :: :UDP | :TCP
  (
    @doc "UDP (User Datagram Protocol)"
    @spec udp() :: :UDP
    def udp() do
      :UDP
    end
  )

  (
    @doc "TCP (Transmission Control Protocol)"
    @spec tcp() :: :TCP
    def tcp() do
      :TCP
    end
  )
end
