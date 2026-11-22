require "json"

module Dagger
  class QueryBuilder
    Field = Data.define(:name, :args, :inline_type)

    NONE = Array.new(0).freeze
    NAME = /\A[_A-Za-z][_0-9A-Za-z]*\z/
    INDENT = "  ".freeze

    attr_reader :selections, :targets

    def self.literal(value)
      case value
      when nil then "null"
      when true, false, Integer then value.to_s
      when Enum then enum_literal(value)
      when String then string_literal(value)
      when Float then float_literal(value)
      when Input then "{#{value.graphql_fields.map { |name, field| "#{name}: #{literal(field)}" }.join(", ")}}"
      when Array then "[#{value.map { literal(it) }.join(", ")}]"
      else raise InvalidQueryError, "Cannot serialize #{value.inspect} as a GraphQL value"
      end
    end

    def self.enum_literal(value)
      # Rendered bare, so anything but a name would inject query syntax.
      raise InvalidQueryError, "Invalid enum value name: #{value.name.inspect}" unless value.name.match?(NAME)

      value.name
    end

    # GraphQL string escapes are a subset of JSON's.
    def self.string_literal(value)
      JSON.generate(String.new(value))
    rescue JSON::GeneratorError, EncodingError => e
      raise InvalidQueryError, "Cannot serialize string #{value.inspect}: #{e.message}"
    end

    def self.float_literal(value)
      raise InvalidQueryError, "Cannot serialize #{value} as a GraphQL value" unless value.finite?

      value.to_s
    end

    def initialize(session: nil, selections: NONE, targets: NONE, reevaluated: false)
      @session = session
      @selections = selections
      @targets = targets
      @reevaluated = reevaluated
    end

    def session = @session || Dagger.default_session

    # Built through a field marked @reevaluate, or chained on such an object:
    # each use evaluates that field again, so no ID of it may be remembered.
    def reevaluated? = @reevaluated

    def reevaluate = with_selections(selections, reevaluated: true)

    # @reevaluate(when:): only a call that sets one of those arguments.
    def reevaluate_when(*conditions) = conditions.any? ? reevaluate : self

    def select(name, **args)
      field = Field.new(name: name.to_s, args: args.compact.transform_values { snapshot(it) }.freeze, inline_type: nil)
      with_selections([*selections, field].freeze)
    end

    def node(id, type_name)
      field = Field.new(name: "node", args: { id: }.freeze, inline_type: type_name)
      with_selections([field].freeze, reevaluated: false)
    end

    def root = with_selections(NONE, reevaluated: false)

    # An object loaded by its ID, with nothing selected on it since, is that ID.
    def loaded_id
      field = selections.first
      field.args[:id] if selections.size == 1 && field&.name == "node" && field.inline_type
    end

    def document(fields = selections)
      raise InvalidQueryError, "No field has been selected" if fields.empty?

      "query {\n#{render(fields, 1)}\n}"
    end

    def with_target(target)
      return self if targets.include?(target)

      self.class.new(session: @session, selections:, targets: [*targets, target].freeze, reevaluated: @reevaluated)
    end

    def execute(allow_nil: false)
      targets.sort_by(&:name).each { session.load(it) }
      fields = resolve_ids
      value = dig(run(document(fields)), fields)
      if value.nil? && !allow_nil
        raise InvalidQueryError, "Required field got a null response. Check if parent fields are valid."
      end

      value
    end

    def execute_object_list(type)
      ids = select("id").execute
      ids.map { type.new(node(it.fetch("id"), type.graphql_name)) }
    end

    def execute_node(type)
      type.new(node(execute, type.graphql_name))
    end

    # A nullable object field: nil when the API answers null, the object
    # otherwise, still to be chained on.
    def execute_optional(type)
      select("id").execute(allow_nil: true) && type.new(self)
    end

    def ==(other) = other.is_a?(QueryBuilder) && other.selections == selections

    alias eql? ==

    def hash = selections.hash

    private

    def with_selections(fields, reevaluated: @reevaluated)
      self.class.new(session: @session, selections: fields, targets:, reevaluated:)
    end

    def run(document)
      session.execute(document)
    rescue QueryError => e
      raise(Client.stale_client_error(e, targets) || e) unless targets.empty?

      raise
    end

    # The query runs later, so a caller's later mutation must not reach it.
    def snapshot(value)
      case value
      when Array then value.map { snapshot(it) }.freeze
      when String then value.frozen? ? value : value.dup.freeze
      else value
      end
    end

    def resolve_ids
      selections.map do |field|
        next field unless field.args.values.any? { object_arg?(it) }

        field.with(args: field.args.transform_values { resolve_id(it) }.freeze)
      end
    end

    def object_arg?(value)
      case value
      when Type then true
      when Array then value.any? { object_arg?(it) }
      when Input then value.to_h.values.any? { object_arg?(it) }
      else false
      end
    end

    def resolve_id(value)
      case value
      when Type then value.id
      when Array then value.map { resolve_id(it) }
      when Input then input_with_ids(value)
      else value
      end
    end

    def input_with_ids(input)
      klass = _ = input.class
      klass.new(**input.to_h.transform_values { resolve_id(it) })
    end

    def render(fields, depth)
      field = fields.fetch(0)
      rest = fields.drop(1)
      pad = INDENT * depth
      head = field.name
      unless field.args.empty?
        head += "(#{field.args.map { |name, value| "#{name}: #{self.class.literal(value)}" }.join(", ")})"
      end
      return "#{pad}#{head}" if rest.empty?

      if field.inline_type
        inner = INDENT * (depth + 1)
        "#{pad}#{head} {\n#{inner}... on #{field.inline_type} {\n#{render(rest, depth + 2)}\n#{inner}}\n#{pad}}"
      else
        "#{pad}#{head} {\n#{render(rest, depth + 1)}\n#{pad}}"
      end
    end

    def dig(data, fields)
      value = data
      fields.each do |field|
        break unless value.is_a?(Hash)

        value = value[field.name]
      end
      value
    end
  end
end
