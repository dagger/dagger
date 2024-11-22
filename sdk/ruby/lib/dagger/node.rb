# frozen_string_literal: true

# typed: true
require 'sorbet-runtime'

# Dagger module
module Dagger
  # Node class, base class for all objects.
  class Node
    extend T::Sig

    # Creates a new Node
    # @param parent Parent node
    # @param client Dagger client
    # @param name name of the node
    # @param args Arbitrary hash of arguments
    sig {params(parent: T.nilable(Node), client: GraphQLClient, name: String, args: T::Hash[String, T.untyped]).void}
    def initialize(parent, client, name, args = {})
      @parent_node = parent
      @parent_node&.set_child(self)
      @client = client
      @node_name = name
      @args = args
      @child = nil
    end

    # Get the Graphql query representation of this object
    sig {returns(String)}
    def to_s
      s = str
      n = self
      until n.parent_node.nil?
        n = T.cast(n.parent_node, Node)
        s = n.str + "{\n#{s}\n}"
      end
      s
    end

    # Get the value from the GraphQL response
    # @param res GrqphQL response
    sig {params(res: T::Hash[String, T.untyped]).returns(T.untyped)}
    def value(res)
      if res.key?('errors') && !res['errors'].empty?
        puts res['errors'].collect { |e| e['message'] }.join("\n")
        exit(false)
      end

      keys = [@node_name]
      n = self
      until n.parent_node.nil?
        n = T.cast(n.parent_node, Node)
        keys.unshift(n.node_name) unless n.node_name.empty?
      end

      keys.inject(res['data']) { |el, key| el[key] }
    end

    protected

    sig {returns(T.nilable(Node))}
    attr_reader :parent_node

    sig {returns(String)}
    attr_reader :node_name

    sig {params(child: Node).void}
    def set_child(child) # rubocop:disable Naming/AccessorMethodName
      @child = child
    end

    sig {returns(String)}
    def str
      s = String.new(@node_name)
      unless @args.empty?
        s << '('
        s << @args.map { |k, v| "#{k}:#{arg_str(v)}" }.join(', ')
        s << ')'
      end
      s
    end

    sig {params(value: T.untyped).returns(String)}
    def arg_str(value)
      case value
      when String
        "\"#{value}\""
      when Numeric
        value.to_s
      when Array
        "[#{value.map { |v| arg_str(v) }.join(', ')}]"
      when Hash
        "{ #{value.map { |k, v| "#{k}: #{arg_str(v)}" }.join(', ')} }"
      else
        if value.respond_to?(:id)
          "\"#{value.id}\""
        elsif value.respond_to?(:serialize)
           value.serialize
        else
          "\"#{value}\""
        end
      end
    end

    sig {params(name: Symbol, value: T.untyped).void}
    def assert_not_nil(name, value)
      return unless value.nil?

      warn("#{name} cannot be nil")
      exit(false)
    end
  end
end
