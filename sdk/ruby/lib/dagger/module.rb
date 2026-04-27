# frozen_string_literal: true

# typed: true
require 'sorbet-runtime'

module Dagger
  # Mixin for defining Dagger modules.
  #
  # Extend this module in your class to mark it as a Dagger object type.
  # All public instance methods with Sorbet sigs are automatically exposed
  # as Dagger functions.
  #
  # Example:
  #   class MyModule
  #     extend T::Sig
  #     extend Dagger::Module
  #
  #     sig { params(name: String).returns(String) }
  #     def greeting(name:)
  #       "Hello, #{name}!"
  #     end
  #   end
  module Module
    def self.extended(base)
      base.include(InstanceMethods)
    end

    module InstanceMethods
      def dag
        @dag ||= Dagger.connect
      end
    end

    # Get all public instance methods defined on this class that have Sorbet sigs.
    # Returns a Hash mapping method_name (Symbol) to {method:, sig:}.
    def dagger_functions
      functions = {}
      instance_methods(false).each do |method_name|
        next if method_name == :dag
        next if method_name.to_s.start_with?('_')

        method_obj = instance_method(method_name)

        # Force finalization of any pending sigs before looking them up.
        # Sorbet lazily attaches sigs, so we need to ensure they're registered.
        begin
          sig_obj = T::Private::Methods.signature_for_method(method_obj)
        rescue StandardError
          next
        end
        next unless sig_obj

        functions[method_name] = {
          method: method_obj,
          sig: sig_obj,
        }
      end
      functions
    end

    # Get the description for this object type (from the class doc or nil)
    def dagger_object_doc
      nil
    end
  end

  # Maps Ruby types to Dagger TypeDefKind values
  module TypeMapper
    extend T::Sig

    SCALAR_MAP = {
      'String' => 'StringKind',
      'Integer' => 'IntegerKind',
      'Float' => 'FloatKind',
      'TrueClass' => 'BooleanKind',
      'FalseClass' => 'BooleanKind',
      'T::Boolean' => 'BooleanKind',
    }.freeze

    class << self
      # Convert a Sorbet type to a Dagger type def
      def to_typedef(dag, type)
        type_str = normalize_type(type)

        # Handle T.nilable(X) -> optional X
        optional = false
        if type_str.start_with?('T.nilable(') && type_str.end_with?(')')
          type_str = type_str[10..-2]
          optional = true
        end

        # Handle T::Array[X] -> list of X
        if type_str.start_with?('T::Array[') && type_str.end_with?(']')
          inner_type_str = type_str[9..-2]
          inner_td = to_typedef(dag, inner_type_str)
          td = dag.type_def.with_list_of(element_type: inner_td)
          td = td.with_optional(value: true) if optional
          return td
        end

        # Check if it's a scalar type
        if SCALAR_MAP.key?(type_str)
          kind_name = SCALAR_MAP[type_str]
          kind_enum = Dagger::TypeDefKind.deserialize(kind_name)
          td = dag.type_def.with_kind(kind: kind_enum)
          td = td.with_optional(value: true) if optional
          return td
        end

        # Check if it's a Dagger API type (e.g., Dagger::Container)
        if type_str.start_with?('Dagger::')
          dagger_type_name = type_str.sub('Dagger::', '')
          td = dag.type_def.with_object(name: dagger_type_name)
          td = td.with_optional(value: true) if optional
          return td
        end

        # Check if it's Void
        if type_str == 'NilClass' || type_str == 'T::Private::Types::Void'
          td = dag.type_def.with_kind(kind: Dagger::TypeDefKind.deserialize('VoidKind'))
          td = td.with_optional(value: true) if optional
          return td
        end

        # Default: treat as string
        td = dag.type_def.with_kind(kind: Dagger::TypeDefKind.deserialize('StringKind'))
        td = td.with_optional(value: true) if optional
        td
      end

      private

      def normalize_type(type)
        case type
        when String
          type
        when Class
          type.name
        when T::Types::Simple
          type.raw_type.name
        when T::Types::Union
          # T.nilable(X) is Union of [X, NilClass]
          non_nil = type.types.reject do |t|
            (t.is_a?(T::Types::Simple) && t.raw_type == NilClass) ||
              t.is_a?(T::Private::Types::Void)
          end
          if type.types.any? { |t| t.is_a?(T::Types::Simple) && t.raw_type == NilClass }
            "T.nilable(#{normalize_type(non_nil.first)})"
          else
            normalize_type(non_nil.first)
          end
        when T::Types::TypedArray
          "T::Array[#{normalize_type(type.type)}]"
        when T::Private::Types::Void
          'T::Private::Types::Void'
        else
          type.to_s
        end
      end
    end
  end
end
