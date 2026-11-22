module Dagger
  class Error < StandardError; end

  class ConnectionError < Error
    def initialize(message = nil)
      prefix = "Failed to establish client connection to the Dagger session"
      super(message ? "#{prefix}: #{message}" : prefix)
    end
  end

  class TransportError < Error; end

  class InvalidQueryError < Error; end

  class ClientLoadError < Error
    attr_reader :target

    def initialize(message, target: nil)
      super(message)
      @target = target
    end
  end

  class StaleClientError < Error; end

  class QueryError < Error
    attr_reader :errors, :query

    def self.from_response(errors, query)
      return nil unless errors.is_a?(Array) && !errors.empty?
      return nil unless errors.all? { it.is_a?(Hash) && it["message"].is_a?(String) }

      klass = errors.first.dig("extensions", "_type") == ExecError::TYPE ? ExecError : QueryError
      klass.new(errors, query)
    end

    def initialize(errors, query)
      @errors = errors
      @query = query
      super(errors.first["message"])
    end

    def path = errors.first["path"]

    def extensions = errors.first["extensions"] || {}

    def locations = errors.first["locations"] || []

    def debug_query
      lines = query.lines(chomp: true)
      pad = lines.size.to_s.size
      columns = locations.to_h { [it["line"], it["column"]] }
      lines.each_with_index.flat_map do |line, index|
        number = index + 1
        rendered = ["#{number.to_s.rjust(pad)}: #{line}"]
        rendered << "#{" " * (pad + 1 + columns[number])}^" if columns[number]
        rendered
      end.join("\n")
    end
  end

  class ExecError < QueryError
    TYPE = "EXEC_ERROR".freeze

    def command = extensions["cmd"]

    def exit_code = extensions["exitCode"]

    def stdout = extensions["stdout"]

    def stderr = extensions["stderr"]
  end
end
