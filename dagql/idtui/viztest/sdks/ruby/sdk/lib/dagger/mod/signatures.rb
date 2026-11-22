require "json"

module Dagger
  module Mod
    # The types generation read from a module's `#:` annotations, and the
    # defaults of its defs, as sdk/entrypoint/signatures.json holds them: a
    # loaded method keeps no trace of its comments.
    class Signatures
      FILE = "sdk/entrypoint/signatures.json".freeze

      Method = Data.define(:location, :nesting, :params, :returns, :refused)
      Param = Data.define(:name, :type, :default)
      Field = Data.define(:location, :nesting, :type, :refused)

      def self.read
        raise Error, "#{FILE} is missing: run `dagger generate`" unless ::File.exist?(FILE)

        new(JSON.parse(::File.read(FILE)))
      end

      # The constant or marker a type names. A constant is found as Ruby finds
      # one written in the class body: in the lexical scopes, then in the
      # innermost one's ancestors and at the top level.
      def self.resolve(type, nesting, owner)
        case type
        when "bool" then Boolean
        when "void" then Void
        when "self" then owner
        when ::String then constant(type, nesting)
        else
          inner = type["optional"] || type.fetch("list")
          type.key?("optional") ? Optional[resolve(inner, nesting, owner)] : [resolve(inner, nesting, owner)]
        end
      end

      def self.constant(name, nesting)
        return ::Object.const_get(name.delete_prefix("::")) if name.start_with?("::")

        head = name.split("::").first.to_s
        scopes = nesting.map { ::Object.const_get(it) }
        (scopes.find { it.const_defined?(head, false) } || scopes.first || ::Object).const_get(name)
      end

      # files: the JSON's path to the classes each file opens.
      def initialize(files)
        @methods = {}
        @fields = {}
        files.each { |file, scopes| scopes.each { add(file, it) } }
      end

      def method_of(klass, name, singleton: false) = @methods[[klass.name, singleton, name.to_s]]

      def field_of(klass, name) = @fields[[klass.name, name.to_s]]

      private

      def add(file, scope)
        name = scope.fetch("class")
        nesting = scope.fetch("nesting")
        none = {} #: Hash[String, json]
        scope.fetch("fields", none).each do |field, record|
          @fields[[name, field]] = Field.new("#{file}:#{record["line"]}", nesting, record["type"], record["refused"])
        end
        { "methods" => false, "singleton_methods" => true }.each do |key, singleton|
          scope.fetch(key, none).each do |method, record|
            params = (record["params"] || []).map { Param.new(it["name"].to_sym, it["type"], it["default"]) }
            @methods[[name, singleton, method]] =
              Method.new("#{file}:#{record["line"]}", nesting, params, record["returns"], record["refused"])
          end
        end
      end
    end
  end
end
