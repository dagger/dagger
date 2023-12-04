# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Service do
  @moduledoc "A content-addressed service providing TCP connectivity."
  use Dagger.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "Retrieves an endpoint that clients can use to reach this container.\n\nIf no port is specified, the first exposed port is used. If none exist an error is returned.\n\nIf a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.\n\n\n\n## Optional Arguments\n\n* `port` - The exposed port number for the endpoint\n* `scheme` - Return a URL with the given scheme, eg. http for http://"
    @spec endpoint(t(), keyword()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def endpoint(%__MODULE__{} = service, optional_args \\ []) do
      selection = select(service.selection, "endpoint")

      selection =
        if is_nil(optional_args[:port]) do
          selection
        else
          arg(selection, "port", optional_args[:port])
        end

      selection =
        if is_nil(optional_args[:scheme]) do
          selection
        else
          arg(selection, "scheme", optional_args[:scheme])
        end

      execute(selection, service.client)
    end
  )

  (
    @doc "Retrieves a hostname which can be used by clients to reach this container."
    @spec hostname(t()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def hostname(%__MODULE__{} = service) do
      selection = select(service.selection, "hostname")
      execute(selection, service.client)
    end
  )

  (
    @doc "A unique identifier for this Service."
    @spec id(t()) :: {:ok, Dagger.ServiceID.t()} | {:error, term()}
    def id(%__MODULE__{} = service) do
      selection = select(service.selection, "id")
      execute(selection, service.client)
    end
  )

  (
    @doc "Retrieves the list of ports provided by the service."
    @spec ports(t()) :: {:ok, [Dagger.Port.t()]} | {:error, term()}
    def ports(%__MODULE__{} = service) do
      selection = select(service.selection, "ports")
      selection = select(selection, "description id port protocol skipHealthCheck")

      with {:ok, data} <- execute(selection, service.client) do
        {:ok,
         data
         |> Enum.map(fn value ->
           elem_selection = Dagger.QueryBuilder.Selection.query()
           elem_selection = select(elem_selection, "loadPortFromID")
           elem_selection = arg(elem_selection, "id", value["id"])
           %Dagger.Port{selection: elem_selection, client: service.client}
         end)}
      end
    end
  )

  (
    @doc "Start the service and wait for its health checks to succeed.\n\nServices bound to a Container do not need to be manually started."
    @spec start(t()) :: {:ok, Dagger.ServiceID.t()} | {:error, term()}
    def start(%__MODULE__{} = service) do
      selection = select(service.selection, "start")
      execute(selection, service.client)
    end
  )

  (
    @doc "Stop the service."
    @spec stop(t()) :: {:ok, Dagger.ServiceID.t()} | {:error, term()}
    def stop(%__MODULE__{} = service) do
      selection = select(service.selection, "stop")
      execute(selection, service.client)
    end
  )
end
