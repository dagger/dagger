#!/usr/bin/env ruby

$LOAD_PATH.unshift('sdk', 'lib')

require 'rubymod'
require 'dagger'

def get_result(parent_name, parent_state, name, inputs)
  Object::const_get(parent_name).new.send(name.gsub('-', '_'), *inputs)
end

def register
  dag = Dagger.connect
  mod = dag
    .module_
    .with_object(
      object: dag
                .type_def
                .with_object(name: "Rubymod")
                .with_function(
                  function: dag
                              .function(
                                name: "container_hello",
                                return_type: dag
                                               .type_def
                                               .with_kind(kind: Dagger::TypeDefKind::ScalarKind))
                              .with_arg(
                                name: "string_arg",
                                type_def: dag.type_def.with_kind(kind: Dagger::TypeDefKind::ScalarKind))))
  mod.id
end

def dispatch
  dag = Dagger.connect

  dag
    .current_function_call
    .return_value(value: register.to_json)
end

def main
  if ARGV.empty?
    dispatch
  else
    # args = ARGV.shift
    puts get_result("Rubymod", {}, "container-hello", ARGV)
  end
end

main
