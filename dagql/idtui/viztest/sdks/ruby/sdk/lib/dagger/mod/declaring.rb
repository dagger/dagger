module Dagger
  module Mod
    # The declarations an object type and an interface share. Each comes
    # right before the def it describes.
    module Declaring
      def dagger_functions = @dagger_functions ||= {}

      def dagger_description = @dagger_description

      def dagger_pending = @dagger_pending

      def description(text)
        @dagger_description = text
      end

      # get: marks a collection's get function, when it is not named get.
      def function(doc = nil, name: nil, check: false, generator: false, start: false, agent: false, get: false,
                   deprecated: nil, cache: nil)
        pend(:function, { doc:, name:, check:, generator:, start:, agent:, get:, deprecated:, cache: })
      end

      # What an argument's type cannot say. Its type is in the method's
      # annotation, its default in the def.
      def arg(ruby_name, name: nil, default_address: nil, ignore: nil, doc: nil, deprecated: nil)
        pending("arg #{ruby_name}").args << ArgDecl.new(ruby_name.to_sym, nil, name, UNSET, false, default_address,
                                                        ignore, doc, deprecated)
      end

      def method_added(method_name)
        super
        return unless @dagger_pending
        raise RegistrationError, "#{self}: a constructor is a class method, def self.create" if constructor_pending?

        dagger_functions[method_name] = take(method_name, constructor: false)
      end

      private

      def pend(kind, options)
        raise RegistrationError, "#{self}: #{kind} follows a declaration that has no def" if @dagger_pending

        @dagger_pending = Pending.new(kind, options)
      end

      def pending(what)
        @dagger_pending or raise RegistrationError, "#{self}: #{what} needs a function or constructor before it"
      end

      def constructor_pending? = @dagger_pending&.kind == :constructor

      def take(method_name, constructor:)
        pending = pending("def #{method_name}")
        @dagger_pending = nil
        options = pending.options
        FunctionDecl.new(method_name, options[:name], options[:doc], UNSET, pending.args.freeze,
                         options[:check], options[:generator], options[:start], options[:agent],
                         options[:get] || false, options[:deprecated], options[:cache], constructor)
      end
    end
  end
end
