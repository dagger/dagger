# frozen_string_literal: true

require 'dagger/node'
require 'dagger/gqlclient'
require 'dagger/client.gen'

# Dagger module
module Dagger
  # Returns a new client for the Dagger API
  # Common use case is to have a
  #   @dag = Dagger.connect
  # then to use this variable like
  #   @dag.container.from(...)
  def connect
    @connect ||= Client.new(nil, gqlclient, '')
  end
  module_function :connect

  # Returns the graphql client used to talk to the Dagger API
  def gqlclient
    @gqlclient ||= GraphQLClient.new
  end
  module_function :gqlclient
end
