require "json"

module Dagger
  module Mod
    # Runs the call a generated entrypoint forwards: the receiver is rebuilt
    # from its state without initialize, and an empty function name is the
    # constructor.
    class Dispatch
      def initialize(registry)
        @registry = registry
        @convert = Convert.new(registry)
      end

      def call(request)
        Mod.module_source_id = request["moduleSource"]
        type_name = request.fetch("receiverType") { raise InvalidInputError, "the request has no receiverType" }
        klass = @registry.object_named(type_name) or raise InvalidInputError, "no object #{type_name} is registered"
        state = decode(request["receiverValue"], "receiverValue") || {}
        args = decode(request["fnArgs"], "fnArgs") || {}
        name = request.fetch("fnName", "")
        name.empty? ? construct(klass, args) : invoke(klass, state, name, args)
      end

      private

      # Both arrive as JSON text, and are decoded here once.
      def decode(text, what)
        return nil if text.nil?
        raise InvalidInputError, "#{what} is not JSON text: #{text.inspect}" unless text.is_a?(::String)

        text.strip.empty? ? nil : JSON.parse(text)
      rescue JSON::ParserError => e
        raise InvalidInputError, "#{what} is not valid JSON: #{e.message}"
      end

      def construct(klass, args)
        where = "#{@registry.type_name(klass)} constructor"
        decl = @registry.constructor(klass)
        object =
          if decl
            user_code { klass.create(**arguments(decl.args, args, where)) }
          else
            user_code { klass.new(**arguments(@registry.field_args(klass), args, where)) }
          end
        @convert.output(object, klass, where)
      end

      def invoke(klass, state, name, args)
        type_name = @registry.type_name(klass)
        functions = @registry.functions(klass).each_value
        decl = functions.find { (it.api_name || it.method_name.to_s) == name } or
          raise InvalidInputError, "#{type_name} has no function #{name}"
        where = "#{type_name}.#{decl.method_name}"
        receiver = @convert.restore(klass, state, "#{type_name} state")
        kwargs = arguments(decl.args, args, where)
        value = user_code { receiver.public_send(decl.method_name, **kwargs) }
        @convert.output(value, decl.returns, "#{where} result")
      end

      # An optional keyword the call leaves out, or sends as null, keeps the
      # default of the def.
      def arguments(decls, args, where)
        kwargs = {} #: Hash[Symbol, untyped]
        decls.each do |arg|
          value = args[arg.api_name || arg.ruby_name.to_s]
          next if value.nil? && arg.optional

          kwargs[arg.ruby_name] = @convert.input(value, arg.type, "#{where}(#{arg.ruby_name})")
        end
        kwargs
      end

      def user_code
        yield
      rescue Mod::Error, ::Dagger::QueryError
        raise
      rescue StandardError => e
        raise FunctionError, e.full_message(highlight: false).strip
      end
    end
  end
end
