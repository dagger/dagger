# frozen_string_literal: true

require 'dagger'

class DaggerModule
  def initialize
    @dag = Dagger.connect
  end

  def container_hello(string_arg)
    @dag
      .container
      .from(address: "alpine:latest")
      .with_exec(args: ["echo", string_arg])
      .stdout
  end
end
