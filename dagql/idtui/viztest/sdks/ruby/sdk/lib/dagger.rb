require "json"
require "open3"
require_relative "dagger/errors"
require_relative "dagger/telemetry"
require_relative "dagger/connection"
require_relative "dagger/session"
require_relative "dagger/types"
require_relative "dagger/query"
require_relative "dagger/client"
require_relative "dagger/provision"

module Dagger
  autoload :Mod, "#{__dir__}/dagger/mod"

  @default_session = nil
  @default_session_lock = Mutex.new
  @module_runtime = false

  class << self
    # A module's process gets its session from the engine that runs it, so
    # it never provisions one.
    def mark_module_runtime! = @module_runtime = true

    def module_runtime? = @module_runtime

    def default_session
      @default_session_lock.synchronize do
        @default_session ||= Session.new.tap { |session| at_exit { session.close } }
      end
    end

    def connect(port: nil, token: nil)
      connection = port && Connection.new(port:, token: token || raise(ArgumentError, "a port needs its token"))
      session = Session.new(connection)
      return session unless block_given?

      begin
        yield session
      ensure
        session.close
      end
    end

    def close
      session = @default_session_lock.synchronize { @default_session.tap { @default_session = nil } }
      session&.close
    end
  end
end
