require "fileutils"
require "json"

module Dagger
  module Mod
    # Renders a module description as the generated Dang entrypoint: the
    # types as literal TypeDef calls, and the call that runs the module's
    # process in a container built from its current source.
    module Entrypoint
      COMMAND = %w[ruby -rbundler/setup sdk/exe/dagger-mod call --output /dagger/result.json].freeze

      module_function

      def write(description, name:, output:, client_version:)
        ::FileUtils.mkdir_p(output)
        ::File.write(::File.join(output, "types.dang"), render_types(description))
        ::File.write(::File.join(output, "main.dang"), render_main(name, client_version:))
      end

      def render_types(description)
        entries = description.objects.map { object(it) } + description.enums.map { enum(it) }
        body = entries.map { indent(it, 6).sub(/\n\z/, ",\n") }.join
        "#{Build::HEADER}\ntype ModuleTypes {\n  pub all: [TypeDef!]! {\n    [\n#{body}    ]\n  }\n}\n"
      end

      # outside, outside_files: what the build reads outside the module's
      # directory, relative to it. sources selects the contract where types and
      # call take no workspace; a module with anything outside its directory
      # builds from its context directory.
      def render_main(name, client_version:, outside: [], outside_files: [])
        alone = (outside + outside_files).empty?
        format(MAIN, header: Build::HEADER, name: quote(name), client_version: quote(client_version),
                     command: Build.dang_strings(COMMAND), sources: sources(outside, outside_files),
                     runtime: alone ? ALONE_RUNTIME : CONTEXT_RUNTIME)
      end

      def sources(outside, outside_files)
        excludes = Build.dang_strings(Build::EXCLUDES)
        entries = outside.map { format(SOURCES_DIRECTORY, at: quote("/#{it}"), path: quote(it), excludes:) } +
                  outside_files.map { format(SOURCES_FILE, at: quote("/#{it}"), path: quote(it)) }
        format(SOURCES, entries: entries.join)
      end

      def object(type)
        head = type.interface ? "withInterface" : "withObject"
        lines = ["typeDef.#{head}(#{quote(type.name)}#{option("description", type.description)})"]
        lines += collection(type.collection) if type.collection
        lines += type.fields.map do |field|
          ".withField(#{quote(field.name)}, #{type_ref(field.type)}#{option("description", field.description)}" \
            "#{option("deprecated", field.deprecated)})"
        end
        lines += type.functions.flat_map { wrap("withFunction", function(it)) }
        lines += wrap("withConstructor", function(type.constructor)) if type.constructor
        lines
      end

      def collection(members)
        marked = { keys: "withCollectionKeys", get: "withCollectionGet", delta: "withCollectionDelta" }
        [".withCollection"] + marked.filter_map do |role, call|
          name = members.public_send(role)
          name && ".#{call}(#{quote(name)})"
        end
      end

      def function(definition)
        lines = ["function(#{quote(definition.name)}, #{type_ref(definition.returns)})"]
        lines << ".withDescription(#{quote(definition.description)})" if definition.description
        lines << ".withDeprecated(reason: #{quote(definition.deprecated)})" if definition.deprecated
        { check: "withCheck", generator: "withGenerator", up: "withUp", agent: "withAgent" }.each do |flag, call|
          lines << ".#{call}" if definition.public_send(flag)
        end
        lines << cache_policy(definition.cache) if definition.cache
        lines + definition.args.map { argument(it) }
      end

      def cache_policy(cache)
        policy = { "never" => "NEVER", "session" => "PER_SESSION" }.fetch(cache, "DEFAULT")
        ".withCachePolicy(FunctionCachePolicy.#{policy}#{option("timeToLive", cache) if policy == "DEFAULT"})"
      end

      def argument(arg)
        default = arg.default_value ? ", defaultValue: (#{quote(arg.default_value)} :: Dagger.JSON!)" : ""
        ignore = arg.ignore ? ", ignore: [#{arg.ignore.map { quote(it) }.join(", ")}]" : ""
        ".withArg(#{quote(arg.name)}, #{type_ref(arg.type)}#{option("description", arg.description)}#{default}" \
          "#{option("defaultAddress", arg.default_address)}#{ignore}" \
          "#{option("deprecated", arg.deprecated)})"
      end

      def enum(type)
        ["typeDef.withEnum(#{quote(type.name)}#{option("description", type.description)})"] +
          type.values.map do |member|
            ".withEnumMember(#{quote(member.name)}, value: #{quote(member.value)}" \
              "#{option("description", member.description)}#{option("deprecated", member.deprecated)})"
          end
      end

      def type_ref(ref)
        expr = ref.optional ? "typeDef.withOptional(true)" : "typeDef"
        case ref.kind
        when "LIST_KIND" then "#{expr}.withListOf(#{type_ref(ref.elem || raise(Error, "a list without an element"))})"
        when "ENUM_KIND" then "#{expr}.withEnum(#{quote(ref.name)})"
        when "SCALAR_KIND" then "#{expr}.withScalar(#{quote(ref.name)})"
        when "INTERFACE_KIND" then "#{expr}.withInterface(#{quote(ref.name)})"
        when "OBJECT_KIND" then "#{expr}.withObject(#{quote(ref.name)})"
        else "#{expr}.withKind(TypeDefKind.#{ref.kind})"
        end
      end

      # The readers and initializer `field` defines, and the constants
      # `member` defines, which Steep cannot see: the module's own RBS.
      def render_rbs(registry)
        objects = registry.objects.sort_by { it.name.to_s }.map { object_rbs(it, registry.fields(it).values) }
        enums = registry.enums.sort_by { it.name.to_s }.map do |klass|
          "class #{klass.name}\n#{klass.dagger_members.map { "  #{it.name}: ::#{klass.name}\n" }.join}end\n"
        end
        "#{Build::HEADER}\n#{(objects + enums).join("\n")}"
      end

      def object_rbs(klass, fields)
        readers = fields.map { "  attr_reader #{it.name}: #{rbs(it.type)}\n" }
        keywords = fields.map { "#{"?" unless required?(it)}#{it.name}: #{rbs(it.type)}" }
        "class #{klass.name}\n#{readers.join}  def initialize: (#{keywords.join(", ")}) -> void\nend\n"
      end

      def required?(field) = field.default.equal?(UNSET) && !field.type.is_a?(Optional)

      def rbs(type)
        case type
        when Optional then "#{rbs(type.type)}?"
        when ::Array then "::Array[#{rbs(type.first)}]"
        else type.equal?(Boolean) ? "bool" : "::#{type.name}"
        end
      end

      def option(name, value) = value.nil? || value.empty? ? "" : ", #{name}: #{quote(value)}"

      def quote(value) = JSON.generate(value)

      def wrap(call, body)
        [".#{call}(", *body.each_with_index.map { |line, index| index.zero? ? "  #{line}" : "    #{line}" }, ")"]
      end

      def indent(lines, width)
        pad = " " * width
        lines.each_with_index.map { |line, index| index.zero? ? "#{pad}#{line}\n" : "#{pad}  #{line}\n" }.join
      end

      MAIN = <<~DANG.freeze
        %<header>s
        type Entrypoint implements ModuleEntrypoint {
          let moduleName: String! = %<name>s

          # The version the module's generated clients were generated for: a nested
          # client that declares none gets the module's view, which the engine decides.
          let clientVersion: String! = %<client_version>s
        %<sources>s
          pub types: [TypeDef!]! {
            ModuleTypes().all
          }

          # The module with its gems installed, which every call runs: the engine
          # builds it ahead of a call, as the module's build, so a failed install
          # shows as the build's, with Bundler's output.
          pub prebuild: File! {
            runtime.file("Gemfile.lock")
          }

          pub call(
            receiverType: String!,
            receiverValue: JSON,
            fnName: String!,
            fnArgs: JSON!,
            callDigest: String,
          ): JSON! {
            let request = JSON.encode({{
              receiverType: receiverType,
              receiverValue: receiverValue,
              fnName: fnName,
              fnArgs: fnArgs,
              moduleSource: currentModule.source.id,
            }})
            # A failed install stays out of the rescue below, so it fails the call
            # with its own error. An engine that builds prebuild first fails the
            # call before it gets here.
            let installed = runtime.sync
            let result = installed
              .withEnvVariable("DAGGER_CLIENT_VERSION", clientVersion)
              # The exec is cached on its own recipe, which holds neither the
              # call's arguments nor its cache policy; the engine's digest of the
              # call does (dagger/dagger#14491). It also marks this exec, and no
              # other, as the module process, the one that runs the function.
              .withEnvVariable("DAGGER_MODULE_CALL_DIGEST", callDigest ?? "")
              .withExec(%<command>s, stdin: request)
              .file("/dagger/result.json")
              .contents rescue {
                err: GraphQLError => raiseFunctionError(err)
              }
            (result :: JSON!)
          }

          # An engine that gives the module process a file for the function's
          # error (DAGGER_FUNCTION_ERROR_PATH) puts the error the process wrote
          # there in its exec's error, and fails the call with it, values and all:
          # the exec's error is raised as it is. Otherwise, as with an older engine
          # or a file the process couldn't write, the exec error carries the end
          # of the process's stderr, where its last marker line holds the
          # function's error; without one, the exec failed before the function
          # returned, as when it exited by itself, and its own error is raised.
          let raiseFunctionError(err: GraphQLError!): String! {
            let reported = ((JSON.decode(err.extensions) :: {{functionError: {{message: String, id: String}}}}).functionError != null) rescue false
            if (reported) {
              raise err
            }
            let marker = "::dagger-function-error::"
            let stderr = ((JSON.decode(err.extensions) :: {{stderr: String}}).stderr ?? "") rescue ""
            let lines = stderr.split("\\n").filter { line => line.hasPrefix(marker) }
            if (lines.isEmpty) {
              raise err
            }
            raise ((JSON.decode(lines.takeLast(1).join("").trimPrefix(marker)) :: String!) rescue err.message)
          }

        %<runtime>s}
      DANG

      SOURCES = <<~DANG.gsub(/^(?=.)/, "  ").freeze

        # What the build reads outside the module's directory, from its Gemfile,
        # at its path from the workspace's root, with the manifest the engine
        # requires there: the engine adds it to the module's context, and keys
        # every call on it.
        pub sources(workspace: Workspace!): Directory! {
          let cwd = workspace.cwd
          directory
            .withFile(cwd + "/dagger-module.toml", workspace.file("dagger-module.toml"))%<entries>s
        }
      DANG

      SOURCES_DIRECTORY = "\n      .withDirectory(cwd + %<at>s, " \
                          "workspace.directory(%<path>s, exclude: %<excludes>s, gitignore: true))".freeze
      SOURCES_FILE = %(\n      .withFile(cwd + %<at>s, workspace.file(%<path>s))).freeze

      ALONE_RUNTIME = <<~DANG.gsub(/^(?=.)/, "  ").freeze
        # The module's own source, not the workspace sources reads: that is the
        # caller's, which holds the module only when the module sits in it.
        let runtime: Container! {
          RubyModuleBuild(source: currentModule.source, moduleName: moduleName).installed
        }
      DANG

      CONTEXT_RUNTIME = <<~DANG.gsub(/^(?=.)/, "  ").freeze
        # The module's context directory, with the module's own source at its
        # subpath, beside what sources returns.
        let runtime: Container! {
          let context = currentModule.contextDirectory
            .withDirectory(currentModule.sourceSubpath, currentModule.source)
          RubyModuleBuild(source: context, subpath: currentModule.sourceSubpath, moduleName: moduleName).installed
        }
      DANG
    end
  end
end
