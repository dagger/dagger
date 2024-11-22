# frozen_string_literal: true

# typed: true
require 'sorbet-runtime'

require 'json'

def basic_auth(username, password)
  Base64.encode64("#{username}:#{password}").rstrip
end

# Dagger module
module Dagger
  # Client to interact with the dagger GraphQL API
  class GraphQLClient
    extend T::Sig

    sig {void}
    def initialize
      unless ENV.include?('DAGGER_SESSION_PORT')
        warn('DAGGER_SESSION_PORT is not set')
        exit(false)
      end
      port_str = ENV['DAGGER_SESSION_PORT']
      port = port_str.to_i
      if port_str != port.to_s
        warn("DAGGER_SESSION_PORT #{port_str} is invalid")
        exit(false)
      end
      unless ENV.include?('DAGGER_SESSION_TOKEN')
        warn('DAGGER_SESSION_TOKEN is not set')
        exit(false)
      end

      @host = "http://127.0.0.1:#{port}/query"
      session_token = ENV['DAGGER_SESSION_TOKEN']
      @headers = {
        'Authorization' => "Basic #{basic_auth(session_token, '')}",
        'content-type' => 'application/json'
      }
    end

    sig {params(definition: String).returns(T.untyped)}
    def query(definition)
      uri = URI(@host)
      host = uri.host
      path = uri.path
      if host.nil? || host.empty? || path.nil? || path.empty?
        warn('dagger host not found')
        exit(false)
      end
      params = { 'query' => definition, 'variables' => {} }
      http = Net::HTTP.new(host, uri.port)
      res = http.post(path, params.to_json, @headers)
      ::JSON.parse(res.body)
    end

    sig {params(node: Node).returns(T.untyped)}
    def invoke(node)
      res = query(node.to_s)
      node.value(res)
    end
  end
end
