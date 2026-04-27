# frozen_string_literal: true

# typed: true
require 'sorbet-runtime'
require 'json'
require 'net/http'
require 'base64'

module Dagger
  # Client to interact with the Dagger GraphQL API
  class GraphQLClient
    extend T::Sig

    sig { void }
    def initialize
      unless ENV.include?('DAGGER_SESSION_PORT')
        raise 'DAGGER_SESSION_PORT is not set'
      end
      port_str = ENV.fetch('DAGGER_SESSION_PORT')
      port = port_str.to_i
      if port_str != port.to_s
        raise "DAGGER_SESSION_PORT #{port_str} is invalid"
      end
      unless ENV.include?('DAGGER_SESSION_TOKEN')
        raise 'DAGGER_SESSION_TOKEN is not set'
      end

      @host = "http://127.0.0.1:#{port}/query"
      session_token = ENV.fetch('DAGGER_SESSION_TOKEN')
      encoded = Base64.strict_encode64("#{session_token}:")
      @headers = {
        'Authorization' => "Basic #{encoded}",
        'content-type' => 'application/json'
      }
    end

    sig { params(definition: String).returns(T.untyped) }
    def query(definition)
      uri = URI(@host)
      host = uri.host
      path = uri.path
      if host.nil? || host.empty? || path.nil? || path.empty?
        raise 'dagger host not found'
      end
      params = { 'query' => definition, 'variables' => {} }
      http = Net::HTTP.new(host, uri.port)
      res = http.post(path, params.to_json, @headers)
      ::JSON.parse(res.body)
    end

    sig { params(node: Node).returns(T.untyped) }
    def invoke(node)
      res = query(node.to_s)
      node.value(res)
    end
  end
end
