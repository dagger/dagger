module Dagger
  module Mod
    class << self
      attr_accessor :module_source_id

      # The directory of the module being called, which its generated
      # entrypoint sends with every call. The module's process cannot ask the
      # engine for it, and a contextual directory resolves against the
      # caller's workspace, not the module.
      def module_source
        id = module_source_id or raise Error, "no module source: a module receives it only in a call"
        require "dagger/clients/core"
        clients = _ = ::Dagger.const_get(:Clients)
        clients::Core::Directory.new(QueryBuilder.new.node(id, "Directory"))
      end
    end
  end
end
