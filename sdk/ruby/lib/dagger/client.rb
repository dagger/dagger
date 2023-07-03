# frozen_string_literal: true

module Dagger
  class Client < Base
    def container
      Container.new(@graphql, [[:container]])
    end

    def host
      Host.new(@graphql, [[:host]])
    end
  end

  class Container < Base
    def from(address)
      add_node(:from, address: address)
    end

    def with_exec(*args)
      add_node(:with_exec, args: args)
    end

    def with_workdir(path)
      add_node(:with_workdir, path: path)
    end

    def with_directory(path, directory)
      add_node(:with_directory, path: path, directory: directory.send(:id))
    end

    def stdout
      add_node(:stdout).send(:evaluate)
    end

    def stderr
      add_node(:stderr).send(:evaluate)
    end
  end

  class Host < Base
    def directory(path, *args)
      args[0][:path] = path
      add_node(:directory, *args)
    end
  end
end
