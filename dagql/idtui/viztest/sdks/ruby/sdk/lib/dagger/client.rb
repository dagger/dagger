module Dagger
  # The calls generated client code makes into the SDK, and what a client's
  # descriptor tells it: which module to load, and the core it was made for.
  module Client
    GENERATE_HINT = "Run `dagger generate`.".freeze
    # What the engine's validator answers for a field the schema lacks.
    # Anchored, because a resolver may quote the same words.
    MISSING_FIELD = /\ACannot query field "([^"]*)" on type "([^"]*)"/i

    Target = Data.define(:name, :ref, :pin)

    class Target
      def initialize(name:, ref:, pin: nil) = super

      def describe = "#{name.inspect} from #{pin ? "#{ref} at #{pin}" : ref}"
    end

    # Registration is a phase of the process, not of a thread: a stale client
    # only warns during it, so a module with a client to itself can register
    # before that client is regenerated.
    @registering = 0
    @registering_lock = Mutex.new

    class << self
      def registering
        @registering_lock.synchronize { @registering += 1 }
        yield
      ensure
        @registering_lock.synchronize { @registering -= 1 }
      end

      def registering? = @registering_lock.synchronize { @registering.positive? }

      def root(target, field, args, session: nil)
        QueryBuilder.new(session:, targets: [target]).select(field, **args)
      end

      def select(receiver, target, field, args) = receiver.query.with_target(target).select(field, **args)

      # Every client the scope declares: Bundler activates each member's gem,
      # and core's and the client roots' are no clients.
      def declared_targets
        @declared_targets ||= Gem.loaded_specs.keys.sort.filter_map do |gem|
          dir = gem.delete_prefix("dagger-clients-")
          next if dir == gem || %w[core roots].include?(dir)

          require "dagger/clients/#{dir}"
          clients = _ = ::Dagger.const_get(:Clients)
          clients.const_get(dir.split("_").map(&:capitalize).join)::TARGET
        end
      end

      def check_core(name, expected, installed)
        return if expected == installed

        message = "Client #{name.inspect} was generated for core #{expected}, but the installed core is " \
                  "#{installed}. #{GENERATE_HINT}"
        raise StaleClientError, message unless registering?

        warn "dagger: #{message}"
      end

      # Validation fails before anything runs, so its error has no path and
      # says it is validation; any other error comes from something that ran.
      def stale_client_error(error, targets)
        missing = error.errors.find do |value|
          value["path"].nil? && value.dig("extensions", "code") == "GRAPHQL_VALIDATION_FAILED" &&
            value["message"].to_s.match?(MISSING_FIELD)
        end
        return nil unless missing

        clients = targets.sort_by(&:name).map(&:describe)
        which = clients.size == 1 ? "The client #{clients.first} is" : "The clients #{clients.join(", ")} are"
        StaleClientError.new("#{missing["message"]} #{which} out of date. #{GENERATE_HINT}")
      end
    end
  end
end
