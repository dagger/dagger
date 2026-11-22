require "fileutils"
require "json"
require_relative "../mod"

module Dagger
  module Mod
    # The commands a generated entrypoint and generation run in the module's
    # container. The module comes from --name and --file, or from the
    # DAGGER_MODULE and DAGGER_MODULE_FILE the module's container sets.
    module CLI
      USAGE = "usage: dagger-mod call|entrypoint|objects [--name <module>] [--file <path>] " \
              "[--output <path>] [--client-version <version>] [--main-object <class>]".freeze

      module_function

      def run(argv, input: $stdin)
        command, *rest = argv
        options = parse(rest)
        case command
        when "call" then call(options, input)
        when "entrypoint" then entrypoint(options)
        when "objects" then objects(options)
        else raise Error, USAGE
        end
      end

      def parse(args)
        args.each_slice(2).to_h do |flag, value|
          raise Error, USAGE unless flag&.start_with?("--") && value

          [flag.delete_prefix("--").tr("-", "_").to_sym, value]
        end
      end

      # The candidates for the main object, which the engine picks by the
      # module's name, in a file: module code may print as it loads.
      def objects(options)
        output = options.fetch(:output) { raise Error, USAGE }
        ::Dagger::Client.registering { load_module(options) }
        ::FileUtils.mkdir_p(::File.dirname(output))
        ::File.write(output, JSON.generate(Mod.registry.objects.map { Mod.registry.type_name(it) }))
      end

      def entrypoint(options)
        output = options.fetch(:output) { raise Error, USAGE }
        client_version = options.fetch(:client_version) { raise Error, USAGE }
        name = ::Dagger::Client.registering { load_module(options) }
        Entrypoint.write(Mod.registry.describe(options[:main_object]), name:, output:, client_version:)
        ::File.write(::File.join(output, "module.rbs"), Entrypoint.render_rbs(Mod.registry))
      end

      def call(options, input)
        output = options.fetch(:output) { raise Error, USAGE }
        request = parse_request(input.read)
        load_module(options)
        result = Dispatch.new(Mod.registry).call(request)
        ::FileUtils.mkdir_p(::File.dirname(output))
        ::File.write(output, JSON.generate(result))
      end

      def parse_request(text)
        request = JSON.parse(text)
        raise InvalidInputError, "the request is not a JSON object" unless request.is_a?(::Hash)

        request
      rescue JSON::ParserError => e
        raise InvalidInputError, "the request is not valid JSON: #{e.message}"
      end

      def load_module(options)
        name = options[:name] || ENV.fetch("DAGGER_MODULE", nil) or raise Error, USAGE
        file = options[:file] || ENV.fetch("DAGGER_MODULE_FILE", nil) || Naming.module_file(name)
        raise Error, "the module file #{file} does not exist" unless ::File.exist?(file)

        require ::File.expand_path(file)
        Mod.registry.annotate(Signatures.read)
        name
      end
    end
  end
end
