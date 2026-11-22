require "json"

module Dagger
  module Mod
    # Every class and enum a module's files register when they load, their
    # declarations with the types the module's signatures give them, and
    # their description for generation.
    class Registry
      BUILTINS = { ::String => "STRING_KIND", ::Integer => "INTEGER_KIND", ::Float => "FLOAT_KIND",
                   Boolean => "BOOLEAN_KIND" }.freeze
      ARGUMENT = /\A[a-z_][a-z0-9_]*\z/
      ROOTS = "Dagger::Clients::Roots".freeze
      EXAMPLE = "#: (name: String) -> String".freeze
      # Whole hours, minutes and seconds, which Go's time.ParseDuration reads exactly; the engine takes one second
      # to a week.
      TIME_TO_LIVE = /\A(?:\d+[hms])+\z/

      attr_reader :objects, :enums, :interfaces

      def initialize
        @objects = []
        @enums = []
        @interfaces = []
        @fields = {}
        @functions = {}
        @constructors = {}
        @locations = {}
      end

      def add_object(klass) = @objects << klass

      def add_interface(mod) = @interfaces << mod

      def add_enum(klass) = @enums << klass

      def object_named(name) = @objects.find { type_name(it) == name }

      def type_name(klass) = klass.name.to_s.split("::").last.to_s

      # Gives each declaration the types of its annotation, once the module
      # has loaded: what the DSL declared, typed.
      def annotate(signatures)
        @objects.each do |klass|
          @fields[klass] = klass.dagger_fields.transform_values { typed_field(klass, it, signatures) }
          declared = klass.dagger_constructor
          @constructors[klass] = declared && typed_function(klass, declared, signatures)
        end
        (@objects + @interfaces).each do |klass|
          @functions[klass] = klass.dagger_functions.transform_values { typed_function(klass, it, signatures) }
        end
      end

      def fields(klass) = @fields.fetch(klass)

      def field_args(klass)
        fields(klass).values.map do |field|
          ArgDecl.new(field.name, field.type, nil, field.default, !field.default.equal?(UNSET), nil, field.ignore,
                      field.doc, field.deprecated)
        end
      end

      def functions(klass) = @functions.fetch(klass)

      def constructor(klass) = @constructors.fetch(klass)

      def describe(main_object)
        main = object_named(main_object) or raise RegistrationError, missing_main(main_object)

        (@objects + @interfaces).each { check_complete(it) }
        @objects.each { check_client_methods(it) }
        objects = @objects.map { describe_object(it, main: it.equal?(main)) } +
                  @interfaces.map { describe_interface(it) }
        Description::Module.new(
          main_object: type_name(main),
          description: main.dagger_description,
          objects: objects.sort_by(&:name),
          enums: @enums.sort_by { type_name(it) }.map { describe_enum(it) }
        )
      end

      def type_ref(type, context, optional: false)
        case type
        when Optional then type_ref(type.type, context, optional: true)
        when ::Array then list_ref(type, context, optional)
        when ::Module then class_ref(type, context, optional)
        else raise RegistrationError, "#{context}: #{type.inspect} is not a type"
        end
      end

      # A member as its errors name it: its file and line, then Type.member.
      def member_at(klass, kind, name)
        where = "#{type_name(klass)}.#{name}"
        location = @locations[[klass, kind, name]]
        location ? "#{location}: #{where}" : where
      end

      private

      def missing_main(name)
        unless name
          return "the engine takes none of #{@objects.map { type_name(it) }.join(", ")} as the main object: lib/ " \
                 "must define a class named after the module with `extend Dagger::Mod::Object`"
        end

        "the main object #{name} is not registered: lib/ must define class #{name} with `extend Dagger::Mod::Object`"
      end

      def typed_field(klass, field, signatures)
        where = "#{type_name(klass)}.#{field.name}"
        signature = signatures.field_of(klass, field.name)
        @locations[[klass, :field, field.name]] = signature&.location
        at = signature ? "#{signature.location}: #{where}" : where
        raise RegistrationError, "#{at}: #{signature.refused}" if signature&.refused
        unless signature&.type
          raise RegistrationError, "#{at}: the field has no type; end its line with one, as field :#{field.name} " \
                                   "#: String"
        end

        field.with(type: resolve(signature.type, signature.nesting, klass, at))
      end

      # The method must take what its annotation said when generation read
      # it: a def edited since then no longer matches its entrypoint.
      def typed_function(klass, decl, signatures)
        where = "#{type_name(klass)}.#{decl.method_name}"
        method = decl.constructor ? klass.method(:create) : klass.instance_method(decl.method_name)
        signature = signatures.method_of(klass, decl.method_name, singleton: decl.constructor) or
          raise RegistrationError, "#{location(method)}: #{where} has no annotation that generation read; write " \
                                   "its type on the line above the def, as #{EXAMPLE}"
        at = "#{signature.location}: #{where}"
        @locations[[klass, :function, decl.method_name]] = signature.location
        raise RegistrationError, "#{at}: #{signature.refused}" if signature.refused

        check_parameters(method, decl, signature, at)
        args = signature.params.map { typed_arg(klass, decl, signature, it, "#{at}(#{it.name})") }
        decl.with(args:, returns: resolve(signature.returns, signature.nesting, klass, "#{at} result"))
      end

      def location(method)
        path, line = method.source_location
        "#{path.to_s.delete_prefix("#{Dir.pwd}/")}:#{line}"
      end

      def check_parameters(method, decl, signature, at)
        unknown = decl.args.map(&:ruby_name) - signature.params.map(&:name)
        raise RegistrationError, "#{at}: arg :#{unknown.first} names no keyword of the method" unless unknown.empty?

        taken = method.parameters.map do |kind, name|
          case kind
          when :keyreq then "#{name}:"
          when :key then "?#{name}:"
          else "#{name} (#{kind})"
          end
        end
        read = signature.params.map { "#{"?" if it.default}#{it.name}:" }
        return if taken.sort == read.sort

        raise RegistrationError, "#{at}: the method takes (#{taken.join(", ")}) but its annotation took " \
                                 "(#{read.join(", ")}) when generation read it"
      end

      def typed_arg(klass, decl, signature, param, context)
        options = decl.args.find { it.ruby_name == param.name }
        type = resolve(param.type, signature.nesting, klass, context)
        default = param.default ? default_value(param.default, signature.nesting, context) : UNSET
        ArgDecl.new(param.name, type, options&.api_name, default, !param.default.nil?, options&.default_address,
                    options&.ignore, options&.doc, options&.deprecated)
      end

      # A default the API can carry: a literal, or a constant holding one or
      # naming an enum member. Any other default is Ruby's alone.
      def default_value(default, nesting, context)
        return default["value"] if default.key?("value")
        return UNSET unless default.key?("constant")

        value = Signatures.constant(default["constant"], nesting)
        value.is_a?(::Dagger::Enum) || value.nil? || plain?(value) ? value : UNSET
      rescue NameError => e
        raise RegistrationError, "#{context}: the default #{default["constant"]}: #{e.message}"
      end

      def resolve(type, nesting, klass, context)
        Signatures.resolve(type, nesting, klass)
      rescue NameError => e
        raise RegistrationError, "#{context}: #{e.message}"
      end

      def check_complete(klass)
        pending = klass.dagger_pending or return

        raise RegistrationError, "#{klass}: a #{pending.kind} declaration has no def after it"
      end

      # The scope's generated Dagger::Clients::Roots gives a class that
      # includes it one private method per client: a name of the class equal
      # to one would hide that client, or be hidden by it.
      def check_client_methods(klass)
        return unless ::Object.const_defined?(ROOTS)

        roots = ::Object.const_get(ROOTS)
        return unless klass.include?(roots)

        ancestors = klass.ancestors
        index = ancestors.index(roots) || 0
        roots.clients.each do |method, client|
          given = "the method #{method} that #{roots} gives the client #{client.dump}"
          clash = declared_clash(klass, method)
          raise RegistrationError, "#{klass}: #{clash} has the name of #{given}; rename it" if clash

          if (owner = ancestors.first(index).find { defines?(it, method) })
            raise RegistrationError, "#{klass}: #{owner}##{method} hides #{given}; rename it"
          end
          if (owner = ancestors.drop(index + 1).find { defines?(it, method) })
            raise RegistrationError, "#{klass}: #{given} would hide #{owner}##{method}"
          end
        end
      end

      def declared_clash(klass, method)
        if fields(klass).key?(method) then "the field #{method}"
        elsif functions(klass).key?(method) then "the function #{method}"
        elsif constructor(klass)&.args&.any? { it.ruby_name == method }
          "the constructor argument #{method}"
        end
      end

      def defines?(mod, method) = mod.method_defined?(method, false) || mod.private_method_defined?(method, false)

      def describe_object(klass, main:)
        name = type_name(klass)
        fields = fields(klass).values.reject(&:private).map do |field|
          Description::Field.new(field.name.to_s, type_ref(field.type, "#{name}.#{field.name}"), field.doc,
                                 field.deprecated)
        end
        functions = functions(klass).values.map { describe_function(klass, it) }
        raise RegistrationError, "#{name}: only the main object has a constructor" if constructor(klass) && !main

        Description::ObjectType.new(name, false, klass.dagger_description, fields, functions,
                                    main ? describe_constructor(klass) : nil, CollectionRules.new(self, klass).describe)
      end

      def describe_interface(mod)
        functions = functions(mod).values.map do |decl|
          if decl.get
            raise RegistrationError, "#{member_at(mod, :function, decl.method_name)} is marked get:, which marks a " \
                                     "collection's get function; an interface is no collection"
          end
          check_interface_result(mod, decl.returns)
          describe_function(mod, decl)
        end
        Description::ObjectType.new(type_name(mod), true, mod.dagger_description, [], functions, nil, nil)
      end

      def check_interface_result(mod, type)
        type = type.type if type.is_a?(Optional)
        type = type.first while type.is_a?(::Array)
        return unless @objects.include?(type)

        raise RegistrationError, "#{type_name(mod)}: an interface function cannot return the module object " \
                                 "#{type_name(type)}; return an interface instead"
      end

      def describe_constructor(klass)
        declared = constructor(klass)
        return describe_function(klass, declared) if declared

        Description::Function.new("", object_ref(klass), nil, nil, false, false, false, false, nil,
                                  field_args(klass).map { describe_arg(type_name(klass), it) })
      end

      def describe_function(klass, decl)
        where = "#{type_name(klass)}.#{decl.method_name}"
        check_cache(member_at(klass, :function, decl.method_name), decl.cache)
        returns = decl.constructor ? object_ref(klass) : type_ref(decl.returns, "#{where} result")
        name = decl.constructor ? "" : function_name(decl, where)
        Description::Function.new(name, returns, decl.doc, decl.deprecated, decl.check, decl.generator, decl.start,
                                  decl.agent, decl.cache, decl.args.map { describe_arg(where, it) })
      end

      def check_cache(member, cache)
        return if cache.nil? || %w[never session].include?(cache)

        if cache.is_a?(String) && cache.match?(TIME_TO_LIVE)
          seconds = cache.split(/(?<=[hms])/).sum do |part|
            part.to_i * { "h" => 3600, "m" => 60, "s" => 1 }.fetch(part.delete("0-9"))
          end
          return if seconds.between?(1, 168 * 3600)
        end

        raise RegistrationError, "#{member}: cache: takes \"never\", \"session\" or a time to live from 1s to 168h, " \
                                 "such as \"5m\" or \"1h30m\", not #{cache.inspect}"
      end

      def function_name(decl, where)
        return decl.api_name if decl.api_name
        raise RegistrationError, "#{where}: a name ending in ? or ! needs name:" if decl.method_name.end_with?("?", "!")

        decl.method_name.to_s
      end

      # A keyword whose default only Ruby knows is optional to callers, who
      # may then omit it.
      def describe_arg(where, arg)
        context = "#{where}(#{arg.ruby_name})"
        raise RegistrationError, "#{context}: not a Ruby argument name" unless arg.ruby_name.to_s.match?(ARGUMENT)

        type = type_ref(arg.type, context, optional: arg.optional && arg.default.equal?(UNSET))
        Description::Argument.new(
          arg.api_name || arg.ruby_name.to_s, type, arg.doc, default_json(arg.default, type, context),
          arg.default_address, arg.ignore, arg.deprecated
        )
      end

      def default_json(default, type, context)
        return nil if default.equal?(UNSET)
        return JSON.generate(default.name) if default.is_a?(::Dagger::Enum)
        return JSON.generate(default) if default.nil? || plain?(default)

        raise RegistrationError, "#{context}: a default must be a string, number, boolean, nil, enum member or list " \
                                 "of those, not #{default.inspect} for #{type.kind}"
      end

      def plain?(value)
        case value
        when ::String, ::Integer, ::Float, true, false then true
        when ::Array then value.all? { plain?(it) }
        else false
        end
      end

      def describe_enum(klass)
        members = klass.dagger_members.map do |member|
          Description::EnumMember.new(member.name, member.value, member.doc, member.deprecated)
        end
        Description::Enum.new(type_name(klass), klass.dagger_description, members)
      end

      def list_ref(type, context, optional)
        raise RegistrationError, "#{context}: a list type is [T], with one element" unless type.size == 1

        Description::TypeRef.new(kind: "LIST_KIND", optional:, elem: type_ref(type.first, context))
      end

      def object_ref(klass) = Description::TypeRef.new(kind: "OBJECT_KIND", name: type_name(klass))

      def class_ref(klass, context, optional)
        return Description::TypeRef.new(kind: BUILTINS.fetch(klass), optional:) if BUILTINS.key?(klass)
        return Description::TypeRef.new(kind: "VOID_KIND", optional: true) if klass.equal?(Void)

        kind = class_kind(klass) or
          raise RegistrationError, "#{context}: #{klass} is not a type the API can carry"
        Description::TypeRef.new(kind:, name: api_type_name(klass), optional:)
      end

      def class_kind(klass)
        if @interfaces.include?(klass) then "INTERFACE_KIND"
        elsif @objects.include?(klass) || (klass.is_a?(::Class) && klass < ::Dagger::Type) then "OBJECT_KIND"
        elsif klass.is_a?(::Class) && klass < ::Dagger::Scalar then "SCALAR_KIND"
        elsif klass.is_a?(::Class) && klass < ::Dagger::Enum then "ENUM_KIND"
        end
      end

      def api_type_name(klass)
        klass.is_a?(::Class) && klass < ::Dagger::Type ? klass.graphql_name : type_name(klass)
      end
    end

    def self.registry = @registry ||= Registry.new

    def self.reset_registry! = @registry = Registry.new
  end
end
