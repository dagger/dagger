module Dagger
  module Mod
    class Error < Dagger::Error; end

    class RegistrationError < Error; end

    class InvalidInputError < Error; end

    class InvalidResultError < Error; end

    class FunctionError < Error; end

    # What a module most likely needs when it no longer matches its generated
    # entrypoint, its lock or its clients.
    REGENERATE_HINT = "if you changed declarations, clients or gems, run `dagger generate`".freeze
  end
end
