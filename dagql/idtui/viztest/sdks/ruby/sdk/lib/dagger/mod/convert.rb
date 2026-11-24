module Dagger
  module Mod
    # Values on the wire of a module call: scalars as JSON scalars, a core
    # object as its ID, a module object as its state keyed by API names, an
    # enum as its member's name.
    class Convert
      # The engine's reference to the collection an object was selected from,
      # sent with a collection's state: it compares a returned object's keys
      # with that base for the delta, so the object carries it back unread.
      COLLECTION_BASE = "__daggerCollectionBase".freeze
      BASE_VARIABLE = :@__dagger_collection_base

      def initialize(registry)
        @registry = registry
      end

      def input(value, type, where)
        return input_optional(value, type, where) if type.is_a?(Optional)
        raise InvalidInputError, "#{where}: a value is required" if value.nil? && !type.equal?(Void)
        return expect(value, ::Array, where).map { input(it, type.first, where) } if type.is_a?(::Array)

        input_named(value, type, where)
      end

      def output(value, type, where)
        return (value.nil? ? nil : output(value, type.type, where)) if type.is_a?(Optional)
        return nil if type.equal?(Void)
        raise InvalidResultError, "#{where}: returned nil, which its type does not allow" if value.nil?
        return result(value, ::Array, where).map { output(it, type.first, where) } if type.is_a?(::Array)

        output_named(value, type, where)
      end

      def restore(klass, state, where)
        expect(state, ::Hash, where)
        klass.allocate.tap do |object|
          @registry.fields(klass).each_value do |field|
            value = input(state[field.name.to_s], field.type, "#{where}.#{field.name}")
            object.instance_variable_set(:"@#{field.name}", value)
          end
          object.instance_variable_set(BASE_VARIABLE, state[COLLECTION_BASE]) if state.key?(COLLECTION_BASE)
        end
      end

      # A copy of an object keeps its base, as a value copy does in Go; an
      # object made with new has none.
      def state(object, klass, where)
        fields = @registry.fields(klass).each_value.to_h do |field|
          value = object.instance_variable_get(:"@#{field.name}")
          [field.name.to_s, output(value, field.type, "#{where}.#{field.name}")]
        end
        base = object.instance_variable_get(BASE_VARIABLE)
        base.nil? ? fields : fields.merge(COLLECTION_BASE => base)
      end

      private

      def input_optional(value, type, where) = value.nil? ? nil : input(value, type.type, where)

      def input_named(value, type, where)
        if type.equal?(::String) || type.equal?(::Integer) then expect(value, type, where)
        elsif type.equal?(::Float) then expect(value, ::Numeric, where).to_f
        elsif type.equal?(Boolean) then boolean(value, where)
        elsif type.equal?(Void) then nil
        elsif @registry.objects.include?(type) then restore(type, value, where)
        elsif @registry.interfaces.include?(type) then load(binding(type), expect(value, ::String, where))
        elsif type < ::Dagger::Enum then enum(type, value, where)
        elsif type < ::Dagger::Type then load(type, expect(value, ::String, where))
        elsif type < ::Dagger::Scalar then type.new(expect(value, ::String, where))
        else raise InvalidInputError, "#{where}: #{type} cannot be decoded"
        end
      end

      def output_named(value, type, where)
        if type.equal?(::String) || type.equal?(::Integer) then result(value, type, where)
        elsif type.equal?(::Float) then result(value, ::Numeric, where).to_f
        elsif type.equal?(Boolean) then result_boolean(value, where)
        elsif @registry.objects.include?(type) then state(result(value, type, where), type, where)
        elsif @registry.interfaces.include?(type) then result(value, ::Dagger::Type, where).id
        elsif type < ::Dagger::Enum then result(value, type, where).name
        elsif type < ::Dagger::Type then result(value, type, where).id
        elsif type < ::Dagger::Scalar then result(value, ::String, where).to_s
        else raise InvalidResultError, "#{where}: #{type} cannot be encoded"
        end
      end

      def load(type, id) = type.new(::Dagger::QueryBuilder.new.node(id, type.graphql_name))

      def binding(interface) = Interface.binding(interface, @registry)

      def expect(value, type, where)
        return value if value.is_a?(type)

        raise InvalidInputError, "#{where}: expected #{type}, got #{value.inspect}"
      end

      def result(value, type, where)
        return value if value.is_a?(type)

        raise InvalidResultError, "#{where}: expected #{type}, got #{value.inspect}"
      end

      def boolean(value, where)
        return value if [true, false].include?(value)

        raise InvalidInputError, "#{where}: expected a boolean, got #{value.inspect}"
      end

      def result_boolean(value, where)
        return value if [true, false].include?(value)

        raise InvalidResultError, "#{where}: expected a boolean, got #{value.inspect}"
      end

      def enum(type, name, where)
        type.from_name(expect(name, ::String, where))
      rescue ::Dagger::InvalidQueryError
        raise InvalidInputError, "#{where}: #{type} has no member #{name.inspect}"
      end
    end
  end
end
