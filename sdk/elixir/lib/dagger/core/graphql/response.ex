defmodule Dagger.Core.GraphQL.Response.Error do
  defexception [:message, :path, :locations, :extensions]

  def from_map(map) do
    %__MODULE__{
      message: map["message"],
      path: map["path"],
      locations: map["locations"],
      extensions: map["extensions"]
    }
  end

  @impl true
  def message(exception) do
    input = Enum.join(exception.path, ".")
    message = exception.message

    "input: #{input} #{message}"
  end
end

defmodule Dagger.Core.GraphQL.Response do
  @moduledoc """
  GraphQL Response type.
  """

  alias Dagger.Core.GraphQL.Response.Error

  defstruct [:data, :extensions, :errors]

  @doc """
  Serialize response from map to struct.
  """
  def from_map(%{} = map) do
    errors = map["errors"]

    %__MODULE__{
      data: map["data"],
      errors:
        unless is_nil(errors) do
          Enum.map(errors, &Error.from_map/1)
        end
    }
  end
end
