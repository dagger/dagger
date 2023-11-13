defprotocol Dagger.ID do
  @moduledoc false

  @fallback_to_any true

  @doc """
  Fetch an id against the resource.

  Raise exception on error.
  """
  @spec id!(struct()) :: String.t()
  def id!(resource)
end

defimpl Dagger.ID, for: Any do
  defmacro __deriving__(module, _struct, _opts) do
    quote do
      defimpl Dagger.ID, for: unquote(module) do
        def id!(resource) do
          {:ok, id} = unquote(module).id(resource)
          id
        end
      end
    end
  end

  def id!(value) do
    # Borrowing from `:jason` library.
    raise Protocol.UndefinedError,
      protocol: @protocol,
      value: value,
      description: "Dagger.ID protocol must be explicitly implemented"
  end
end
