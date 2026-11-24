require "json"
require "net/http"

module Dagger
  class Connection
    # Retrying is safe: every API call is cached on its inputs.
    MAX_ATTEMPTS = 5
    MAX_BACKOFF = 2.0
    TRANSPORT_ERRORS = [
      IOError, EOFError, SystemCallError, SocketError, Net::OpenTimeout, Net::ReadTimeout, Net::WriteTimeout
    ].freeze

    attr_reader :port

    def self.from_env
      port = ENV.fetch("DAGGER_SESSION_PORT", "")
      return nil if port.empty?

      token = ENV.fetch("DAGGER_SESSION_TOKEN", "")
      raise ConnectionError, "DAGGER_SESSION_TOKEN must be set when using DAGGER_SESSION_PORT" if token.empty?

      number = Integer(port, exception: false) or raise ConnectionError, "Invalid DAGGER_SESSION_PORT: #{port}"
      new(port: number, token:)
    end

    def initialize(port:, token:, read_timeout: nil)
      raise ConnectionError, "Invalid port value: #{port}" unless port.is_a?(Integer) && port.positive?

      @port = port
      @token = token
      @read_timeout = read_timeout
      @clients = {}
      @lock = Mutex.new
    end

    def execute(document)
      attempt = 0
      begin
        attempt += 1
        read(post(document), document)
      rescue *TRANSPORT_ERRORS => e
        drop_client
        raise TransportError, "Failed to execute request: #{e.message}" if attempt >= MAX_ATTEMPTS

        sleep([0.1 * (2**(attempt - 1)), MAX_BACKOFF].min)
        retry
      end
    end

    def close
      clients = @lock.synchronize { @clients.values.tap { @clients.clear } }
      clients.each { it.finish if it.started? }
    end

    private

    def post(document)
      request = Net::HTTP::Post.new("/query", "Content-Type" => "application/json")
      request.basic_auth(@token, "")
      traceparent = Telemetry.traceparent
      request["traceparent"] = traceparent if traceparent
      request.body = JSON.generate(query: document)
      client.request(request)
    end

    # One keep-alive connection per thread: Net::HTTP is not thread-safe.
    def client
      @lock.synchronize do
        @clients[Thread.current] ||= Net::HTTP.new("127.0.0.1", @port).tap do |http|
          # nil turns the read timeout off, which the stdlib signature does not allow.
          http.read_timeout = _ = @read_timeout
          http.start
        end
      end
    end

    def drop_client
      http = @lock.synchronize { @clients.delete(Thread.current) }
      http.finish if http&.started?
    rescue IOError
      nil
    end

    def read(response, document)
      body = parse(response)
      if (errors = body["errors"])
        raise QueryError.from_response(errors, document) ||
              TransportError.new("Unexpected error response from engine: #{errors.inspect}")
      end
      raise TransportError, unexpected(response) unless response.code == "200"

      body["data"]
    end

    def parse(response)
      body = JSON.parse(response.body.to_s)
      raise TransportError, unexpected(response) unless body.is_a?(Hash)

      body
    rescue JSON::ParserError
      raise TransportError, unexpected(response)
    end

    def unexpected(response) = "Unexpected response from engine: #{response.code} #{response.body.to_s[0, 200]}"
  end
end
