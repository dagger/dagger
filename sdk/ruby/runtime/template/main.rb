# frozen_string_literal: true

# typed: true
require 'dagger'

class DaggerModule
  extend T::Sig
  extend Dagger::Module

  sig { params(string_arg: String).returns(Dagger::Container) }
  def container_echo(string_arg:)
    dag.container
       .from(address: 'alpine:latest')
       .with_exec(args: ['echo', string_arg])
  end
end
