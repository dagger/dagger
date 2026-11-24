module Dagger
  # A connection to one engine, and what has been loaded into it: each client
  # target is served once per session.
  class Session
    Load = Struct.new(:target, :lock, :done)

    def initialize(connection = nil)
      @connection = connection
      @lock = Mutex.new
      @loads = {}
    end

    def execute(document) = connection.execute(document)

    # A module and `dagger run` put a session in the environment; only a
    # plain program with neither starts one, and a module never does.
    def connection
      @lock.synchronize do
        @connection ||= Connection.from_env || provision
      end
    end

    def load(target)
      entry = @lock.synchronize { @loads[target.name] ||= Load.new(target, Mutex.new, false) }
      unless entry.target == target
        raise ClientLoadError.new("This session already holds #{entry.target.describe} and cannot also take " \
                                  "#{target.describe}", target:)
      end

      entry.lock.synchronize do
        next if entry.done

        serve(target)
        entry.done = true
      end
    end

    def close
      connection, provisioned = @lock.synchronize { release }
      connection&.close
      provisioned&.close
    end

    private

    # The engine behind a new connection has loaded nothing.
    def release
      @loads.clear
      connection = @connection
      provisioned = @provisioned
      @connection = @provisioned = nil
      [connection, provisioned]
    end

    def provision
      raise ConnectionError, "No active engine session to connect to" if Dagger.module_runtime?

      provisioned = Provision.new
      @provisioned = provisioned
      provisioned.connection
    end

    def serve(target)
      serve_module = QueryBuilder.new(session: self).select("serveModule", address: target.ref, refPin: target.pin)
      serve_module.execute(allow_nil: true)
    rescue Dagger::Error => e
      raise ClientLoadError.new("Failed to load client #{target.name.inspect} from #{target.ref.inspect}: #{e.message}",
                                target:)
    end
  end
end
