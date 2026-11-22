module Dagger
  module Mod
    # `extend Dagger::Mod::Interface` registers a module as an interface type.
    # Its functions are declarations only: the engine passes whatever object
    # implements them, and module code calls that object through a binding.
    module Interface
      include Declaring

      def self.extended(base)
        Mod.registry.add_interface(base)
      end

      # The class of the values module code gets for an interface, with one
      # method per declared function.
      def self.binding(interface, registry)
        bindings = (@bindings ||= {}) #: Hash[untyped, singleton(::Dagger::Type)]
        bindings[interface] ||= Binding.new(interface, registry).build
      end

      # The engine names an interface's functions and arguments by the
      # module's naming rules, which a call through it selects on the
      # implementing object: one query per process answers for every interface.
      def self.field_names(registry, root)
        answers = (@field_names ||= {}) #: Hash[Registry, Hash[String, String]]
        answers[registry] ||= begin
          names = registry.interfaces.flat_map { registry.functions(it).values }.flat_map do |decl|
            [decl.api_name || decl.method_name.to_s, *decl.args.map { it.api_name || it.ruby_name.to_s }]
          end.uniq
          naming = root.select("naming").select("module", name: ENV.fetch("DAGGER_MODULE"))
          names.zip(naming.select("fieldNames", names:).execute).to_h
        end
      end

      # Builds a binding class. Its methods return what a generated client
      # returns: a value for a leaf, an object to chain on otherwise.
      #
      # The engine passes the implementing object's ID, and the interface's
      # own type is not in this module's schema, so a value is loaded as a
      # Node and its first call selects on the type __typename names. That
      # type is served by one of the module's clients, which the call loads.
      class Binding
        def initialize(interface, registry)
          @interface = interface
          @registry = registry
          @types = {} #: Hash[String, String]
        end

        def build
          klass = _ = ::Class.new(::Dagger::Type)
          klass.define_singleton_method(:graphql_name) { "Node" }
          @registry.functions(@interface).each_value { define(klass, it) }
          klass
        end

        # One call of an interface function on the value `instance` holds.
        def call(instance, decl, kwargs)
          names = Interface.field_names(@registry, instance.query.root)
          field = names.fetch(decl.api_name || decl.method_name.to_s)
          result(concrete(instance.query).select(field, **arguments(names, decl, kwargs)), decl.returns)
        end

        def result(query, type)
          case type
          when Optional then leaf?(type.type) ? query.execute(allow_nil: true) : result(query, type.type)
          when ::Array then list(query, type.first)
          else single(query, type)
          end
        end

        private

        # An optional argument left out is sent with its default, when the API
        # can carry it, and left to the implementation otherwise.
        def arguments(names, decl, kwargs)
          args = {} #: Hash[Symbol, untyped]
          decl.args.each do |arg|
            given = kwargs.key?(arg.ruby_name)
            raise ::ArgumentError, "missing keyword: :#{arg.ruby_name}" unless given || arg.optional

            value = kwargs.fetch(arg.ruby_name, arg.default)
            args[names.fetch(arg.api_name || arg.ruby_name.to_s).to_sym] = value unless value.equal?(UNSET)
          end
          args
        end

        def concrete(query)
          id = query.loaded_id or return query
          type = @types[id] ||= query.select("__typename").execute
          node = query.root.node(id, type)
          ::Dagger::Client.declared_targets.reduce(node) { |served, target| served.with_target(target) }
        end

        def define(klass, decl)
          binder = self #: Binding
          klass.define_method(decl.method_name) { |**kwargs| binder.call(self, decl, kwargs) }
        end

        def single(query, type)
          if type.equal?(Void) then query.execute(allow_nil: true) && nil
          elsif leaf?(type) then query.execute
          elsif type.is_a?(::Class) && type < ::Dagger::Enum then type.from_name(query.execute)
          else object_class(type).new(query)
          end
        end

        def list(query, type)
          return query.execute.map { type.from_name(it) } if type.is_a?(::Class) && type < ::Dagger::Enum
          return query.execute if leaf?(type) || type.is_a?(::Array)

          query.execute_object_list(object_class(type))
        end

        def leaf?(type)
          Registry::BUILTINS.key?(type) || (type.is_a?(::Class) && type < ::Dagger::Scalar)
        end

        def object_class(type)
          return Interface.binding(type, @registry) if @registry.interfaces.include?(type)
          return type if type.is_a?(::Class) && type < ::Dagger::Type

          raise RegistrationError, "#{@interface}: an interface function cannot return #{type}"
        end
      end
    end
  end
end
