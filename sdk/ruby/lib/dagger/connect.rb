# frozen_string_literal: true

require "graphlient"

module Dagger
  class << self
    def connect!
      port = ENV.fetch("DAGGER_SESSION_PORT")
      token = ENV.fetch("DAGGER_SESSION_TOKEN")
      Client.new(connect_to_dagger!(port, token))
    end

    private

    def connect_to_dagger!(port, token)
      url = "http://127.0.0.1:#{port}/query"
      auth = Base64::encode64("#{token}:")
      Graphlient::Client.new(url, headers: {
        "Authorization" => "Basic #{auth}"
      })
    end
  end
end
