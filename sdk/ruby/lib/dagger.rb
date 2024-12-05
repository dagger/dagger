# frozen_string_literal: true

# typed: true
require 'sorbet-runtime'

require 'dagger/node'
require 'dagger/gqlclient'
require 'dagger/client.gen'

# Dagger module
module Dagger
  extend T::Sig
  # Returns a new client for the Dagger API
  # Common use case is to have a
  #   @dag = Dagger.connect
  # then to use this variable like
  #   @dag.container.from(...)
  sig {returns(Client)}
  def connect
    @connect ||= Client.new(nil, gqlclient, '')
  end
  module_function :connect

  # Returns the graphql client used to talk to the Dagger API
  sig {returns(GraphQLClient)}
  def gqlclient
    @gqlclient ||= GraphQLClient.new
  end
  module_function :gqlclient
end
