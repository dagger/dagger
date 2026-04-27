#!/usr/bin/env ruby
# frozen_string_literal: true

$LOAD_PATH.unshift('sdk', 'sdk/dagger', 'lib')

require 'json'
require 'dagger'

# Load the user's module code
module_name = ENV.fetch('DAGGER_MODULE_NAME', 'DaggerModule')
module_file = ENV.fetch('DAGGER_MODULE_FILE', nil)

if module_file
  require_relative module_file
else
  # Find the main Ruby file in lib/
  lib_files = Dir.glob('lib/**/*.rb')
  lib_files.each { |f| require_relative f }
end

# Find the module class
module_class = Object.const_get(module_name)

def snake_to_camel(str)
  str.split('_').map(&:capitalize).join
end

def camel_to_snake(str)
  str.gsub(/([A-Z])/, '_\1').sub(/^_/, '').downcase
end

# Map a Ruby type to a GraphQL typedef query fragment
def type_to_gql(type_str)
  type_str = Dagger::TypeMapper.send(:normalize_type, type_str)

  scalar_map = {
    'String' => 'STRING_KIND',
    'Integer' => 'INTEGER_KIND',
    'Float' => 'FLOAT_KIND',
    'TrueClass' => 'BOOLEAN_KIND',
    'FalseClass' => 'BOOLEAN_KIND',
    'T::Boolean' => 'BOOLEAN_KIND',
  }

  if scalar_map.key?(type_str)
    kind = scalar_map[type_str]
    "typeDef{withKind(kind:#{kind}){id}}"
  elsif type_str.start_with?('Dagger::')
    dagger_type_name = type_str.sub('Dagger::', '')
    "typeDef{withObject(name:\"#{dagger_type_name}\"){id}}"
  elsif type_str == 'NilClass' || type_str == 'T::Private::Types::Void'
    "typeDef{withKind(kind:VOID_KIND){id}}"
  else
    "typeDef{withKind(kind:STRING_KIND){id}}"
  end
end

# Get a type def ID via raw GraphQL
def get_typedef_id(gql, type)
  query = "{#{type_to_gql(type)}}"
  result = gql.query(query)
  if result.key?('errors') && !result['errors'].empty?
    raise "GraphQL Error getting typedef: #{result['errors'].collect { |e| e['message'] }.join("\n")}"
  end
  result.dig('data', 'typeDef')&.values&.first&.dig('id')
end

# Register the module's type definitions with the Dagger engine using raw GQL
def register_module(dag, module_class, module_name)
  gql = Dagger.gqlclient

  # Discover all functions via Sorbet sigs
  functions = module_class.dagger_functions

  # Build each function with args, get its ID

  # Build each function with args, get its ID
  func_ids = []
  functions.each do |method_name, func_info|
    sig = func_info[:sig]
    method = func_info[:method]

    # Get the return type ID
    return_td_id = get_typedef_id(gql, sig.return_type)

    # Start with function(name, returnType) and chain withArg calls
    func_query = "function(name:\"#{method_name}\", returnType:\"#{return_td_id}\")"
    close_count = 0

    # Add arguments from the sig's kwarg_types (not method.parameters,
    # because Sorbet wraps methods and changes the parameter list)
    kwarg_types = sig.kwarg_types || {}
    req_kwarg_names = sig.req_kwarg_names || []

    kwarg_types.each do |param_name, param_type|
      arg_td_id = get_typedef_id(gql, param_type)

      # Mark optional if not in required kwargs
      unless req_kwarg_names.include?(param_name)
        opt_result = gql.query("{typeDef{withOptional(typeDef:\"#{arg_td_id}\", value:true){id}}}")
        arg_td_id = opt_result.dig('data', 'typeDef', 'withOptional', 'id') || arg_td_id
      end

      func_query += "{withArg(name:\"#{param_name}\", typeDef:\"#{arg_td_id}\")"
      close_count += 1
    end

    func_query += "{id}" + ("}" * close_count)

    result = gql.query("{#{func_query}}")
    if result.key?('errors') && !result['errors'].empty?
      raise "GraphQL Error creating function: #{result['errors'].collect { |e| e['message'] }.join("\n")}"
    end

    # Extract the function ID from the nested response
    func_id = extract_id(result['data'])
    func_ids << func_id
  end

  # Build the object typedef with all functions (chained withFunction calls)
  obj_query = "typeDef{withObject(name:\"#{module_name}\")"
  close_count = 1 # for withObject
  func_ids.each do |fid|
    obj_query += "{withFunction(function:\"#{fid}\")"
    close_count += 1
  end
  obj_query += "{id}" + ("}" * close_count)

  result = gql.query("{#{obj_query}}")
  if result.key?('errors') && !result['errors'].empty?
    raise "GraphQL Error creating object: #{result['errors'].collect { |e| e['message'] }.join("\n")}"
  end
  obj_td_id = extract_id(result['data'])

  # Build the module with the object (GraphQL field name is "module", not "module_")
  result = gql.query("{module{withObject(object:\"#{obj_td_id}\"){id}}}")
  if result.key?('errors') && !result['errors'].empty?
    raise "GraphQL Error creating module: #{result['errors'].collect { |e| e['message'] }.join("\n")}"
  end
  extract_id(result['data'])
end

# Extract the deepest 'id' value from a nested hash
def extract_id(data)
  return data if data.is_a?(String)
  return data['id'] if data.is_a?(Hash) && data.key?('id')
  return nil unless data.is_a?(Hash)

  data.each_value do |v|
    result = extract_id(v)
    return result if result
  end
  nil
end

# Dispatch a function call to the appropriate method on the module instance
def dispatch_function(dag, module_class, parent_name, fn_name, parent_json, input_args)
  instance = module_class.new

  # Restore state from parent_json if present
  if parent_json && !parent_json.empty?
    state = JSON.parse(parent_json)
    state.each do |key, value|
      setter = "#{key}="
      instance.send(setter, value) if instance.respond_to?(setter)
    end
  end

  # Convert the function name from camelCase/kebab-case to snake_case
  ruby_method = camel_to_snake(fn_name.gsub('-', '_'))

  # Build keyword arguments from input_args
  kwargs = {}
  input_args.each do |arg|
    arg_name = camel_to_snake(arg['name']).to_sym
    kwargs[arg_name] = convert_input_value(arg['value'])
  end

  # Call the method
  if kwargs.empty?
    result = instance.send(ruby_method)
  else
    result = instance.send(ruby_method, **kwargs)
  end

  # Serialize the result
  serialize_output(dag, result)
end

def convert_input_value(value)
  return value unless value.is_a?(String)

  # Try to parse as JSON for complex types
  begin
    parsed = JSON.parse(value)
    return parsed
  rescue JSON::ParserError
    value
  end
end

def serialize_output(dag, result)
  case result
  when String, Integer, Float, TrueClass, FalseClass
    JSON.generate(result)
  when NilClass
    JSON.generate(nil)
  when Array
    JSON.generate(result.map { |item| JSON.parse(serialize_output(dag, item)) })
  when Dagger::Node
    # For Dagger objects, we need to return their ID as a JSON string
    JSON.generate(result.id)
  else
    JSON.generate(result.to_s)
  end
end

# Get function call info using raw GraphQL (avoids generated client limitations
# with list-of-objects queries like inputArgs)
def get_function_call_info
  gql = Dagger.gqlclient
  result = gql.query('{
    currentFunctionCall {
      name
      parentName
      parent
      inputArgs {
        name
        value
      }
    }
  }')

  if result.key?('errors') && !result['errors'].empty?
    raise "GraphQL Error: #{result['errors'].collect { |e| e['message'] }.join("\n")}"
  end

  result['data']['currentFunctionCall']
end

# Send return value using raw GraphQL.
# json_value must be a valid JSON string (e.g., '"hello"' or '42').
def send_return_value(json_value)
  gql = Dagger.gqlclient
  # Escape for use inside a GraphQL string literal
  escaped = json_value.to_s.gsub('\\', '\\\\').gsub('"', '\\"').gsub("\n", '\\n')
  result = gql.query("{ currentFunctionCall { returnValue(value: \"#{escaped}\") } }")

  if result.key?('errors') && !result['errors'].empty?
    raise "GraphQL Error: #{result['errors'].collect { |e| e['message'] }.join("\n")}"
  end
end

# Main entrypoint
def main
  dag_client = dag

  fn_call_info = get_function_call_info
  parent_name = fn_call_info['parentName']
  fn_name = fn_call_info['name']
  parent_json = fn_call_info['parent']
  input_args = fn_call_info['inputArgs'] || []

  module_name = ENV.fetch('DAGGER_MODULE_NAME', 'DaggerModule')
  module_class = Object.const_get(module_name)

  result = if parent_name.empty? || parent_name.nil?
             # Registration mode: return the module type definitions as JSON-encoded ID
             mod_id = register_module(dag_client, module_class, module_name)
             JSON.generate(mod_id)
           else
             # Dispatch mode: call the function and return the JSON-encoded result
             dispatch_function(dag_client, module_class, parent_name, fn_name, parent_json, input_args)
           end

  send_return_value(result)
end

main
