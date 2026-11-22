module Dagger
  module Mod
    module Naming
      module_function

      def file_name(module_name)
        module_name.gsub(/[^A-Za-z0-9]+/, "_").gsub(/([A-Z]+)([A-Z][a-z])/, '\1_\2').gsub(/([a-z\d])([A-Z])/, '\1_\2')
                   .downcase.squeeze("_").delete_prefix("_").delete_suffix("_")
      end

      def module_file(module_name) = "lib/#{file_name(module_name)}.rb"
    end
  end
end
