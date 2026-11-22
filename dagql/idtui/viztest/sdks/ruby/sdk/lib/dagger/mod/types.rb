module Dagger
  module Mod
    # What an annotation's type resolves to besides a constant: Ruby has no
    # Boolean class, and void and T? name none.
    module Boolean; end

    module Void; end

    Optional = Data.define(:type)

    class Optional
      def self.[](type) = new(type:)
    end
  end
end
