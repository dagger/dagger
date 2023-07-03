# frozen_string_literal: true

require "graphlient"

module Dagger
  class Client
    def host
      add_node(:host)
    end

    def directory(path, *args)
      args[0][:path] = path
      add_node(:directory, *args)
    end

    def container
      add_node(:container)
    end

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

    private

    def initialize(graphql, operations = [])
      @graphql = graphql
      @operations = operations
    end

    def id
      add_node(:id).send(:evaluate)
    end

    def add_node(method_name, *args, &block)
      Client.new(@graphql, @operations + [[method_name, *args]])
    end    

    def evaluate
      send_ops = ->(target, rest) {
        op, *rest = *rest
        method_name, *args = *op
        
        m = camelize(method_name)

        return target.send(m, *args) if rest.empty?
        return target.send(m, *args) do
          send_ops.(self, rest)
        end
      }

      rest = [[:query], [:query]] + @operations
      response = send_ops.(@graphql, rest)

      return @operations.map(&:first).inject(response.data) { |node, op| node.send(op) }
    end

    def camelize(s)
      s.to_s.gsub!(/(?:_)([a-z\d]*)/i) { $1.capitalize } || s.to_s
    end
  end
end
