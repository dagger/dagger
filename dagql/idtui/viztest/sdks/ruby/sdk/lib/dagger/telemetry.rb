module Dagger
  module Telemetry
    TRACEPARENT = /\A[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}\z/

    def self.traceparent
      value = ENV.fetch("TRACEPARENT", nil)
      value if value&.match?(TRACEPARENT)
    end
  end
end
