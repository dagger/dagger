# frozen_string_literal: true

def basic_auth(username, password)
  Base64.encode64("#{username}:#{password}").rstrip
end

# Dagger module
module Dagger
  # Client to interact with the dagger GraphQL API
  class GraphQLClient
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

      @root = Node.new(nil, @client, '')
    end

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
      JSON.parse(res.body)
    end

    def container
      Container.new(@root.dup, self, 'container')
    end

    def host
      Host.new(@root.dup, self, 'host')
    end

    def cache_volume(key:)
      CacheVolume.new(@root.dup, self, 'cacheVolume', { 'key' => key })
    end

    def invoke(node)
      res = query(node.to_s)
      node.value(res)
    end
  end

  def connect
    @connect ||= Client.new(nil, gqlclient, '')
  end
  module_function :connect

  def gqlclient
    @gqlclient ||= GraphQLClient.new
  end
  module_function :gqlclient
end
