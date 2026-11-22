module Dagger
  module Mod
    # `class Color < Dagger::Mod::Enum` registers an enum; `member :RED, "red"`
    # declares a member, sent and received by its name.
    class Enum < ::Dagger::Enum
      MEMBER_NAME = /\A[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*\z/

      attr_reader :doc, :deprecated

      def self.inherited(subclass)
        super
        Mod.registry.add_enum(subclass)
      end

      class << self
        attr_reader :dagger_description
      end

      def self.dagger_members = @dagger_members ||= []

      def self.description(text)
        @dagger_description = text
      end

      def self.member(name, value = name.to_s, doc: nil, deprecated: nil)
        raise RegistrationError, "#{self}: member #{name} is not upper snake case" unless name.to_s.match?(MEMBER_NAME)

        member = new(name.to_s, value.to_s, doc:, deprecated:)
        const_set(name, member)
        dagger_members << member
        member
      end

      def initialize(name, value, doc: nil, deprecated: nil)
        @doc = doc
        @deprecated = deprecated
        super(name, value)
      end
    end
  end
end
