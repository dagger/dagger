module Dagger
  class Scalar < String; end

  class Enum
    attr_reader :name, :value

    def self.members = constants(false).map { const_get(it) }.grep(self).uniq

    def self.from_name(name)
      members.find { it.name == name } || raise(InvalidQueryError, "#{self.name} has no member #{name.inspect}")
    end

    def initialize(name, value)
      @name = name.dup.freeze
      @value = value.dup.freeze
      freeze
    end

    def to_s = value

    def inspect = "#<#{self.class.name} #{name}>"
  end

  module Input
    def graphql_fields
      names = self.class.graphql_names
      to_h.filter_map { |member, value| [names.fetch(member, member.to_s), value] unless value.nil? }
    end
  end

  class Type
    attr_reader :query

    def self.graphql_name = name.to_s.split("::").last.to_s

    def initialize(query)
      @query = query
    end

    def id
      return fetch_id if query.reevaluated?

      @id ||= fetch_id
    end

    def ==(other) = other.instance_of?(self.class) && other.query == query

    alias eql? ==

    def hash = [self.class, query].hash

    private

    def fetch_id = query.loaded_id || query.select("id").execute
  end
end
