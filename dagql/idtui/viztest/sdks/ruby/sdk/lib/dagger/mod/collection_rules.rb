module Dagger
  module Mod
    # A collection by the engine's rules (core/collection.go), refused at the
    # member that breaks one, before the engine would refuse the module. A
    # marker outside a collection is refused too.
    class CollectionRules
      def initialize(registry, klass)
        @registry = registry
        @klass = klass
        @name = registry.type_name(klass)
        @fields = registry.fields(klass).values
        @functions = registry.functions(klass).values
      end

      # The API names of the members marked in the DSL, or nil when the class
      # is no collection.
      def describe
        unless @klass.dagger_collection
          check_unmarked
          return nil
        end
        keys = member(:keys)
        get = getter
        check_get(get, check_keys(keys))
        delta = member(:delta)
        check_delta(delta) if delta
        Description::Collection.new(keys: keys&.role && keys.name.to_s,
                                    get: get.get ? get.api_name || get.method_name.to_s : nil,
                                    delta: delta&.role && delta.name.to_s)
      end

      private

      def at(kind, name) = @registry.member_at(@klass, kind, name)

      def check_unmarked
        if (field = @fields.find(&:role))
          raise RegistrationError, "#{at(:field, field.name)} is marked #{field.role}:, but #{@name} is not a " \
                                   "collection; add collection to the class"
        end
        get = @functions.find(&:get) or return

        raise RegistrationError, "#{at(:function, get.method_name)} is marked get:, but #{@name} is not a " \
                                 "collection; add collection to the class"
      end

      def member(role)
        marked = @fields.select { it.role == role }
        if marked.size > 1
          raise RegistrationError, "#{@klass.dagger_collection}: #{@name} marks #{marked.map(&:name).join(" and ")} " \
                                   "#{role}:; a collection has one"
        end
        marked.first || @fields.find { it.name == role && it.role.nil? }
      end

      def getter
        marked = @functions.select(&:get)
        if marked.size > 1
          names = marked.map(&:method_name).join(" and ")
          raise RegistrationError, "#{@klass.dagger_collection}: #{@name} marks #{names} get:; a collection has one"
        end
        get = marked.first || @functions.find { (it.api_name || it.method_name.to_s) == "get" }
        get or raise RegistrationError, "#{@klass.dagger_collection}: the collection #{@name} has no get function; " \
                                        "declare def get(key:) returning an object, or mark one get: true"
      end

      # The key type, which the keys field lists.
      def check_keys(keys)
        unless keys
          raise RegistrationError, "#{@klass.dagger_collection}: the collection #{@name} has no keys field; declare " \
                                   "field :keys #: Array[String], or mark one keys: true"
        end
        where = at(:field, keys.name)
        raise RegistrationError, "#{where}: a collection's keys field is not private: the engine reads it" \
          if keys.private

        element = keys.type.is_a?(::Array) && keys.type.first
        return element if element && key_type?(element)

        raise RegistrationError, "#{where}: a collection's keys are a list of strings, numbers, booleans, scalars " \
                                 "or enum members, none of them nil, as #: Array[String]"
      end

      def key_type?(type)
        Registry::BUILTINS.key?(type) || (type.is_a?(::Class) && (type < ::Dagger::Scalar || type < ::Dagger::Enum))
      end

      def check_get(get, key)
        where = at(:function, get.method_name)
        arg = get.args.first
        unless get.args.size == 1 && !arg.optional && arg.type.equal?(key)
          key_name = key.equal?(Boolean) ? "bool" : @registry.type_name(key)
          raise RegistrationError, "#{where}: a collection's get function takes one required argument of the key " \
                                   "type, as #: (key: #{key_name}) -> Item"
        end
        return if object?(get.returns)

        raise RegistrationError, "#{where}: a collection's get function returns an object, never nil"
      end

      def object?(type)
        @registry.objects.include?(type) || (type.is_a?(::Class) && type < ::Dagger::Type)
      end

      def check_delta(delta)
        type = delta.type.is_a?(Optional) ? delta.type.type : delta.type
        delta_type = type.is_a?(::Class) && type < ::Dagger::Type && type.graphql_name == "CollectionDelta"
        return if delta_type && !delta.private

        raise RegistrationError, "#{at(:field, delta.name)}: a collection's delta field is not private, and its " \
                                 "type is Dagger::Clients::Core::CollectionDelta"
      end
    end
  end
end
