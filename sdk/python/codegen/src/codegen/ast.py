import typing
from typing import Any

import graphql


def insert_stubs(introspection: Any, schema: graphql.GraphQLSchema):
    """Insert ast node stubs into the parsed schema."""
    for tp in introspection["types"]:
        if values := tp.get("enumValues"):
            schema_type = schema.get_type(tp["name"])
            schema_type = typing.cast(graphql.GraphQLEnumType, schema_type)

            value_defs = []
            for value in values:
                schema_value = schema_type.values[value["name"]]
                schema_value.ast_node = graphql.EnumValueDefinitionNode(
                    name=graphql.NameNode(value=value["name"]),
                    description=value["description"],
                    directives=parse_directives(value["directives"]),
                )
                value_defs.append(schema_value.ast_node)

            schema_type.ast_node = graphql.EnumTypeDefinitionNode(
                values=value_defs,
                directives=parse_directives(tp["directives"]),
            )

    # TODO: add support for other graphql declarations


def parse_directives(
    directives: list[dict[str, Any]],
) -> tuple[graphql.ConstDirectiveNode, ...]:
    """Parse directives from our dagger non-standard graphql directive application."""
    result = []
    for directive in directives:
        node = graphql.ConstDirectiveNode(
            name=graphql.NameNode(value=directive["name"]),
            arguments=tuple(
                graphql.ConstArgumentNode(
                    name=graphql.NameNode(value=arg["name"]),
                    value=graphql.parse_const_value(arg["value"]),
                )
                for arg in directive["args"]
            ),
        )
        result.append(node)
    return tuple(result)
