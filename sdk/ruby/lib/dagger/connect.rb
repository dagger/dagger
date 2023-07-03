# frozen_string_literal: true

require "graphlient"

module Dagger
  class << self
    def connect!
      port = lookup_env("DAGGER_SESSION_PORT")
      token = lookup_env("DAGGER_SESSION_TOKEN")
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

    def lookup_env(key)
      ENV[key].tap do |v|
        raise "#{key} environment variable is not set" if v.empty?
      end
    end
  end
end
