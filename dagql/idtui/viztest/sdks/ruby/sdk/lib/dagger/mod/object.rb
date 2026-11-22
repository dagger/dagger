module Dagger
  module Mod
    # `extend Dagger::Mod::Object` registers a class as an object type. Its
    # declarations come right before the def they describe.
    module Object
      include Declaring

      module State
        def initialize(**state)
          fields = self.class.dagger_fields
          unknown = state.keys - fields.keys
          raise ArgumentError, "unknown field#{"s" if unknown.size > 1}: #{unknown.join(", ")}" unless unknown.empty?

          fields.each_value do |field|
            value = state.fetch(field.name) { field.default.equal?(UNSET) ? nil : field.default }
            instance_variable_set(:"@#{field.name}", value)
          end
        end
      end

      def self.extended(base)
        base.include(State)
        Mod.registry.add_object(base)
      end

      def dagger_fields = @dagger_fields ||= {}

      def dagger_constructor = @dagger_constructor

      # Where `collection` marks the class, or nil.
      def dagger_collection = @dagger_collection

      # A collection of objects, selected by key: its keys field, get function
      # and delta field are those named keys, get and delta, or the members
      # marked keys:, get: and delta:.
      def collection
        location = caller_locations(1, 1)&.first
        path = location&.path
        @dagger_collection = location && path ? "#{path.delete_prefix("#{Dir.pwd}/")}:#{location.lineno}" : name.to_s
      end

      # The field's type is the `#:` comment that ends its line.
      def field(name, default: UNSET, ignore: nil, private: false, doc: nil, deprecated: nil, keys: false, delta: false)
        raise RegistrationError, "#{self}: field #{name} comes between a declaration and its def" if @dagger_pending
        raise RegistrationError, "#{self}: field #{name} is marked both keys: and delta:" if keys && delta

        role = if keys then :keys
               elsif delta then :delta
               end
        dagger_fields[name.to_sym] = FieldDecl.new(name.to_sym, nil, default, ignore, private, doc, deprecated, role)
        attr_reader name
      end

      def constructor(doc = nil)
        pend(:constructor, { doc: })
      end

      def singleton_method_added(method_name)
        super
        return unless @dagger_pending

        where = "#{self}.#{method_name}"

        raise RegistrationError, "#{where}: a function is an instance method" unless constructor_pending?
        raise RegistrationError, "#{where}: the constructor is def self.create" unless method_name == :create

        @dagger_constructor = take(method_name, constructor: true)
      end
    end
  end
end
