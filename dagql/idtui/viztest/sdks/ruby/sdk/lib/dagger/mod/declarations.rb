module Dagger
  module Mod
    UNSET = ::Object.new.freeze

    # A type is nil, and a function's result UNSET, until Registry#annotate
    # reads them from the module's signatures. An argument is optional when
    # its keyword has a default in the def; its default is UNSET unless the
    # API can carry it.
    # role: :keys or :delta for a collection's member marked as such.
    FieldDecl = Data.define(:name, :type, :default, :ignore, :private, :doc, :deprecated, :role)
    ArgDecl = Data.define(:ruby_name, :type, :api_name, :default, :optional, :default_address, :ignore, :doc,
                          :deprecated)
    FunctionDecl = Data.define(:method_name, :api_name, :doc, :returns, :args, :check, :generator, :start, :agent,
                               :get, :deprecated, :cache, :constructor)

    # What a declaration says until the def it describes comes.
    class Pending
      attr_reader :kind, :options, :args

      def initialize(kind, options)
        @kind = kind
        @options = options
        @args = []
      end
    end
  end
end
