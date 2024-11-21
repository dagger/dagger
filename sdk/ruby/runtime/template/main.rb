#!/usr/bin/env ruby

require 'bundler/setup'

require 'dagger'
require_relative './dagger'

class HelloDagger
  def hello_world(str)
    @dag
      .container
      .from(address: "alpine:latest")
      .with_exec(args: ["echo", "hello #{str}"])
      .stdout
  end

  def grep_dir(dir, pattern)
    mount_dir = @dag
      .host
      .directory(path: dir)
    @dag
      .container
      .from(address: "alpine:latest")
      .with_mounted_directory(path: "/mnt", directory: mount_dir)
      .with_workdir(path: "/mnt")
      .with_exec(args: ["grep", "-R", pattern, "."])
      .stdout
  end
end