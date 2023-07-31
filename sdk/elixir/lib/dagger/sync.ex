defprotocol Dagger.Sync do
  @moduledoc false

  @fallback_to_any true

  @doc """
  Force evaluation of the resource.

  Returns its `resource` if it sync successfully.
  """
  @spec sync(struct()) :: {:ok, struct()} | {:error, term()}
  def sync(resource)
end

defimpl Dagger.Sync, for: Any do
  defmacro __deriving__(module, _struct, _opts) do
    quote do
      defimpl Dagger.Sync, for: unquote(module) do
        def sync(resource) do
          with {:ok, _} <- unquote(module).sync(resource) do
            {:ok, resource}
          end
        end
      end
    end
  end

  def sync(value) do
    # Borrowing from `:jason` library.
    raise Protocol.UndefinedError,
      protocol: @protocol,
      value: value,
      description: "Dagger.Sync protocol must be explicitly implemented"
  end
end
