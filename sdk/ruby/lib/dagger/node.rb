# frozen_string_literal: true

# Dagger module
module Dagger
  # Node class, base class for all objects.
  class Node
    def initialize(parent, client, name, args = {})
      @parent_node = parent
      @parent_node&.set_child(self)
      @client = client
      @node_name = name
      @args = args
      @child = nil
    end

    def to_s
      s = str
      n = self
      until n.parent_node.nil?
        n = n.parent_node
        s = n.str + "{\n#{s}\n}"
      end
      s
    end

    def value(res)
      if res.key?('errors') && !res['errors'].empty?
        puts res['errors'].collect { |e| e['message'] }.join("\n")
        exit(false)
      end

      keys = [@node_name]
      n = @parent_node
      until n.nil?
        keys.unshift(n.node_name) unless n.node_name.empty?
        n = n.parent_node
      end

      keys.inject(res['data']) { |el, key| el[key] }
    end

    protected

    attr_reader :parent_node, :node_name

    def set_child(child) # rubocop:disable Naming/AccessorMethodName
      @child = child
    end

    def str
      s = String.new(@node_name)
      unless @args.empty?
        s << '('
        s << @args.map { |k, v| "#{k}:#{arg_str(v)}" }.join(', ')
        s << ')'
      end
      s
    end

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
        "\"#{value.id}\""
      end
    end

    def assert_not_nil(name, value)
      return unless value.nil?

      warn("#{name} cannot be nil")
      exit(false)
    end
  end
end
