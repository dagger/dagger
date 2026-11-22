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

      # What a call whose request or signatures no longer match the module's
      # code fails with: most likely an edit made without generation.
      STALE = [InvalidInputError, RegistrationError].freeze

      module_function

      # A call returns its exit status, as it reports its own errors (see
      # call); the other commands raise theirs.
      def run(argv, input: $stdin)
        command, *rest = argv
        return call(rest, input:) if command == "call"

        options = parse(rest)
        case command
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

      # Runs the call, and reports the error it fails with (ErrorReport),
      # followed by the hint to run dagger generate after one that most
      # likely follows an edit made without it, such as a file or a constant
      # the module no longer finds as it loads. Returns the exit status: 1
      # when the module's code doesn't load, 2 for any other error. An exit of
      # the function's own, or a signal, ends the process as it would.
      def call(argv, input: $stdin, report: ErrorReport.for_module)
        loaded = false
        options = parse(argv)
        output = options.fetch(:output) { raise Error, USAGE }
        request = parse_request(input.read)
        load_module(options)
        loaded = true
        result = Dispatch.new(Mod.registry).call(request)
        ::FileUtils.mkdir_p(::File.dirname(output))
        ::File.write(output, JSON.generate(result))
        0
      rescue SystemExit, SignalException
        raise
      rescue Exception => e # rubocop:disable Lint/RescueException
        unloadable = !loaded && (e.is_a?(ScriptError) || e.is_a?(NameError))
        stale = (unloadable && !e.is_a?(SyntaxError)) || STALE.any? { e.is_a?(it) }
        report.report(e, hint: stale ? "dagger-mod: #{REGENERATE_HINT}" : nil)
        unloadable ? 1 : 2
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
