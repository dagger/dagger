  # A directory whose contents persist across runs
  class CacheVolume < Node
    # Return the Node ID for the GraphQL entity
    # @return [String]
    def id
      @client.invoke(Node.new(self, @client, 'id'))
    end
  end

  # Information about the host execution environment
  class Host < Node
    # Access a directory on the host
    # @return [Directory]
    def directory(path:, exclude: nil, include: nil)
      assert_not_nil(:path, path)
      args = {
        'path' => path
      }
      args['exclude'] = exclude unless exclude.nil?
      args['include'] = include unless include.nil?
      Directory.new(self, @client, 'directory', args)
    end

    # Lookup the value of an environment variable. Null if the variable is not available.
    # @return [HostVariable]
    def env_variable(name:)
      assert_not_nil(:name, name)
      args = {
        'name' => name
      }
      HostVariable.new(self, @client, 'envVariable', args)
    end

    # The current working directory on the host
    # @return [Directory]
    def workdir(exclude: nil, include: nil)
      args = {}
      args['exclude'] = exclude unless exclude.nil?
      args['include'] = include unless include.nil?
      Directory.new(self, @client, 'workdir', args)
    end
  end