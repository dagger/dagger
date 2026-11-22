module Dagger
  module Mod
    # The module's types as plain data: what generation renders to Dang.
    module Description
      TypeRef = Data.define(:kind, :name, :optional, :elem)

      class TypeRef
        def initialize(kind:, name: "", optional: false, elem: nil) = super

        def to_h = { kind:, name:, optional:, elem: elem&.to_h }
      end

      Argument = Data.define(:name, :type, :description, :default_value, :default_address, :ignore,
                             :deprecated)

      Function = Data.define(:name, :returns, :description, :deprecated, :check, :generator, :up, :agent, :cache,
                             :args)

      Field = Data.define(:name, :type, :description, :deprecated)

      EnumMember = Data.define(:name, :value, :description, :deprecated)

      Enum = Data.define(:name, :description, :values)

      # The API names of a collection's members marked in the DSL; the others
      # go by the engine's names, keys, get and delta.
      Collection = Data.define(:keys, :get, :delta)

      ObjectType = Data.define(:name, :interface, :description, :fields, :functions, :constructor, :collection)

      Module = Data.define(:main_object, :description, :objects, :enums)
    end
  end
end
