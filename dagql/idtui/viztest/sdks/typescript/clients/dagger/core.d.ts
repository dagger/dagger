import { Writable } from 'node:stream';

/**
 * Conveniently represents flow's "Maybe" type https://flow.org/en/docs/types/maybe/
 *
 * @internal
 */
declare type Maybe<T> = null | undefined | T;

/** @category Source */
interface Location$1 {
  line: number;
  column: number;
}
/**
 * A representation of source input to GraphQL. The `name` and `locationOffset` parameters are
 * optional, but they are useful for clients who store GraphQL documents in source files.
 * For example, if the GraphQL input starts at line 40 in a file named `Foo.graphql`, it might
 * be useful for `name` to be `"Foo.graphql"` and location to be `{ line: 40, column: 1 }`.
 * The `line` and `column` properties in `locationOffset` are 1-indexed.
 */
declare class Source {
  /** The GraphQL source text. */
  body: string;
  /** Name used in diagnostics for this source, such as a file path or request name. */
  name: string;
  /** One-indexed line and column where this source begins. */
  locationOffset: Location$1;
  /**
   * Creates a Source instance.
   * @param body - The GraphQL source text.
   * @param name - Name used in diagnostics for this source.
   * @param locationOffset - One-indexed line and column where this source begins.
   * @example
   * ```ts
   * import { Source } from 'graphql/language';
   *
   * const source = new Source(
   *   'type Query { greeting: String }',
   *   'schema.graphql',
   *   { line: 10, column: 1 },
   * );
   *
   * source.body; // => 'type Query { greeting: String }'
   * source.name; // => 'schema.graphql'
   * source.locationOffset; // => { line: 10, column: 1 }
   * ```
   */
  constructor(body: string, name?: string, locationOffset?: Location$1);
  /**
   * Returns the value used by `Object.prototype.toString`.
   * @returns The built-in string tag for this object.
   */
  get [Symbol.toStringTag](): string;
}

/** @category Kinds */
/** The set of allowed kind values for AST nodes. */
declare enum Kind {
  /** AST kind for name nodes. */
  NAME = 'Name',
  /** AST kind for document nodes. */
  DOCUMENT = 'Document',
  /** AST kind for operation definition nodes. */
  OPERATION_DEFINITION = 'OperationDefinition',
  /** AST kind for variable definition nodes. */
  VARIABLE_DEFINITION = 'VariableDefinition',
  /** AST kind for selection set nodes. */
  SELECTION_SET = 'SelectionSet',
  /** AST kind for field selection nodes. */
  FIELD = 'Field',
  /** AST kind for argument nodes. */
  ARGUMENT = 'Argument',
  /** AST kind for fragment spread nodes. */
  FRAGMENT_SPREAD = 'FragmentSpread',
  /** AST kind for inline fragment nodes. */
  INLINE_FRAGMENT = 'InlineFragment',
  /** AST kind for fragment definition nodes. */
  FRAGMENT_DEFINITION = 'FragmentDefinition',
  /** AST kind for variable reference nodes. */
  VARIABLE = 'Variable',
  /** AST kind for integer value nodes. */
  INT = 'IntValue',
  /** AST kind for floating-point value nodes. */
  FLOAT = 'FloatValue',
  /** AST kind for string value nodes. */
  STRING = 'StringValue',
  /** AST kind for boolean value nodes. */
  BOOLEAN = 'BooleanValue',
  /** AST kind for null value nodes. */
  NULL = 'NullValue',
  /** AST kind for enum value nodes. */
  ENUM = 'EnumValue',
  /** AST kind for list value nodes. */
  LIST = 'ListValue',
  /** AST kind for object value nodes. */
  OBJECT = 'ObjectValue',
  /** AST kind for object field nodes. */
  OBJECT_FIELD = 'ObjectField',
  /** AST kind for directive nodes. */
  DIRECTIVE = 'Directive',
  /** AST kind for named type reference nodes. */
  NAMED_TYPE = 'NamedType',
  /** AST kind for list type reference nodes. */
  LIST_TYPE = 'ListType',
  /** AST kind for non-null type reference nodes. */
  NON_NULL_TYPE = 'NonNullType',
  /** AST kind for schema definition nodes. */
  SCHEMA_DEFINITION = 'SchemaDefinition',
  /** AST kind for operation type definition nodes. */
  OPERATION_TYPE_DEFINITION = 'OperationTypeDefinition',
  /** AST kind for scalar type definition nodes. */
  SCALAR_TYPE_DEFINITION = 'ScalarTypeDefinition',
  /** AST kind for object type definition nodes. */
  OBJECT_TYPE_DEFINITION = 'ObjectTypeDefinition',
  /** AST kind for field definition nodes. */
  FIELD_DEFINITION = 'FieldDefinition',
  /** AST kind for input value definition nodes. */
  INPUT_VALUE_DEFINITION = 'InputValueDefinition',
  /** AST kind for interface type definition nodes. */
  INTERFACE_TYPE_DEFINITION = 'InterfaceTypeDefinition',
  /** AST kind for union type definition nodes. */
  UNION_TYPE_DEFINITION = 'UnionTypeDefinition',
  /** AST kind for enum type definition nodes. */
  ENUM_TYPE_DEFINITION = 'EnumTypeDefinition',
  /** AST kind for enum value definition nodes. */
  ENUM_VALUE_DEFINITION = 'EnumValueDefinition',
  /** AST kind for input object type definition nodes. */
  INPUT_OBJECT_TYPE_DEFINITION = 'InputObjectTypeDefinition',
  /** AST kind for directive definition nodes. */
  DIRECTIVE_DEFINITION = 'DirectiveDefinition',
  /** AST kind for schema extension nodes. */
  SCHEMA_EXTENSION = 'SchemaExtension',
  /** AST kind for directive extension nodes. */
  DIRECTIVE_EXTENSION = 'DirectiveExtension',
  /** AST kind for scalar type extension nodes. */
  SCALAR_TYPE_EXTENSION = 'ScalarTypeExtension',
  /** AST kind for object type extension nodes. */
  OBJECT_TYPE_EXTENSION = 'ObjectTypeExtension',
  /** AST kind for interface type extension nodes. */
  INTERFACE_TYPE_EXTENSION = 'InterfaceTypeExtension',
  /** AST kind for union type extension nodes. */
  UNION_TYPE_EXTENSION = 'UnionTypeExtension',
  /** AST kind for enum type extension nodes. */
  ENUM_TYPE_EXTENSION = 'EnumTypeExtension',
  /** AST kind for input object type extension nodes. */
  INPUT_OBJECT_TYPE_EXTENSION = 'InputObjectTypeExtension',
  /** AST kind for type coordinate nodes. */
  TYPE_COORDINATE = 'TypeCoordinate',
  /** AST kind for member coordinate nodes. */
  MEMBER_COORDINATE = 'MemberCoordinate',
  /** AST kind for argument coordinate nodes. */
  ARGUMENT_COORDINATE = 'ArgumentCoordinate',
  /** AST kind for directive coordinate nodes. */
  DIRECTIVE_COORDINATE = 'DirectiveCoordinate',
  /** AST kind for directive argument coordinate nodes. */
  DIRECTIVE_ARGUMENT_COORDINATE = 'DirectiveArgumentCoordinate',
}

/** @category Lexing */
/**
 * An exported enum describing the different kinds of tokens that the
 * lexer emits.
 */
declare enum TokenKind {
  /** Start-of-file token. */
  SOF = '<SOF>',
  /** End-of-file token. */
  EOF = '<EOF>',
  /** The `!` punctuation token. */
  BANG = '!',
  /** The `$` punctuation token. */
  DOLLAR = '$',
  /** The `&` punctuation token. */
  AMP = '&',
  /** The `(` punctuation token. */
  PAREN_L = '(',
  /** The `)` punctuation token. */
  PAREN_R = ')',
  /** The `.` punctuation token. */
  DOT = '.',
  /** The `...` spread punctuation token. */
  SPREAD = '...',
  /** The `:` punctuation token. */
  COLON = ':',
  /** The `=` punctuation token. */
  EQUALS = '=',
  /** The `@` punctuation token. */
  AT = '@',
  /** The `[` punctuation token. */
  BRACKET_L = '[',
  /** The `]` punctuation token. */
  BRACKET_R = ']',
  /** The `{` punctuation token. */
  BRACE_L = '{',
  /** The `|` punctuation token. */
  PIPE = '|',
  /** The `}` punctuation token. */
  BRACE_R = '}',
  /** A GraphQL name token or name AST node. */
  NAME = 'Name',
  /** An integer value token or AST node. */
  INT = 'Int',
  /** A floating-point value token or AST node. */
  FLOAT = 'Float',
  /** A string value token or AST node. */
  STRING = 'String',
  /** A block string value token. */
  BLOCK_STRING = 'BlockString',
  /** A comment token. */
  COMMENT = 'Comment',
}

/** @category AST */

/**
 * Contains a range of UTF-8 character offsets and token references that
 * identify the region of the source from which the AST derived.
 */
declare class Location {
  /** The character offset at which this Node begins. */
  readonly start: number;
  /** The character offset at which this Node ends. */
  readonly end: number;
  /** The Token at which this Node begins. */
  readonly startToken: Token;
  /** The Token at which this Node ends. */
  readonly endToken: Token;
  /** The Source document the AST represents. */
  readonly source: Source;
  /**
   * Creates a Location instance.
   * @param startToken - The start token.
   * @param endToken - The end token.
   * @param source - Source document used to derive error locations.
   * @example
   * ```ts
   * import { Location, Source, Token, TokenKind } from 'graphql/language';
   *
   * const source = new Source('{ hello }');
   * const startToken = new Token(TokenKind.BRACE_L, 0, 1, 1, 1);
   * const endToken = new Token(TokenKind.BRACE_R, 8, 9, 1, 9);
   * const location = new Location(startToken, endToken, source);
   *
   * location.start; // => 0
   * location.end; // => 9
   * location.source.body; // => '{ hello }'
   * ```
   */
  constructor(startToken: Token, endToken: Token, source: Source);
  /**
   * Returns the value used by `Object.prototype.toString`.
   * @returns The built-in string tag for this object.
   */
  get [Symbol.toStringTag](): string;
  /**
   * Returns a JSON representation of this location.
   * @returns The JSON-serializable representation.
   * @example
   * ```ts
   * import { parse } from 'graphql/language';
   *
   * const document = parse('{ hello }');
   * const location = document.loc?.toJSON();
   *
   * location; // => { start: 0, end: 9 }
   * ```
   */
  toJSON(): {
    start: number;
    end: number;
  };
}
/**
 * Represents a range of characters represented by a lexical token
 * within a Source.
 */
declare class Token {
  /** The kind of Token. */
  readonly kind: TokenKind;
  /** The character offset at which this Node begins. */
  readonly start: number;
  /** The character offset at which this Node ends. */
  readonly end: number;
  /** The 1-indexed line number on which this Token appears. */
  readonly line: number;
  /** The 1-indexed column number at which this Token begins. */
  readonly column: number;
  /**
   * For non-punctuation tokens, represents the interpreted value of the token.
   *
   * Note: is undefined for punctuation tokens, but typed as string for
   * convenience in the parser.
   */
  readonly value: string;
  /**
   * Tokens exist as nodes in a double-linked-list amongst all tokens
   * including ignored tokens. <SOF> is always the first node and <EOF>
   * the last.
   */
  readonly prev: Token | null;
  /** Next token in the token stream, including ignored tokens. */
  readonly next: Token | null;
  /**
   * Creates a Token instance.
   * @param kind - Token kind produced by lexical analysis.
   * @param start - Character offset where this token begins.
   * @param end - Character offset where this token ends.
   * @param line - One-indexed line number where this token begins.
   * @param column - One-indexed column number where this token begins.
   * @param value - Interpreted value for non-punctuation tokens.
   * @example
   * ```ts
   * import { Token, TokenKind } from 'graphql/language';
   *
   * const token = new Token(TokenKind.NAME, 2, 7, 1, 3, 'hello');
   *
   * token.kind; // => TokenKind.NAME
   * token.value; // => 'hello'
   * token.toJSON(); // => { kind: 'Name', value: 'hello', line: 1, column: 3 }
   * ```
   */
  constructor(
    kind: TokenKind,
    start: number,
    end: number,
    line: number,
    column: number,
    value?: string,
  );
  /**
   * Returns the value used by `Object.prototype.toString`.
   * @returns The built-in string tag for this object.
   */
  get [Symbol.toStringTag](): string;
  /**
   * Returns a JSON representation of this token.
   * @returns The JSON-serializable representation.
   * @example
   * ```ts
   * import { Lexer, Source } from 'graphql/language';
   *
   * const lexer = new Lexer(new Source('{ hello }'));
   * const token = lexer.advance().toJSON();
   *
   * token; // => { kind: '{', value: undefined, line: 1, column: 1 }
   * ```
   */
  toJSON(): {
    kind: TokenKind;
    value?: string;
    line: number;
    column: number;
  };
}
/** The list of all possible AST node types. */
declare type ASTNode =
  | NameNode
  | DocumentNode
  | OperationDefinitionNode
  | VariableDefinitionNode
  | VariableNode
  | SelectionSetNode
  | FieldNode
  | ArgumentNode
  | FragmentSpreadNode
  | InlineFragmentNode
  | FragmentDefinitionNode
  | IntValueNode
  | FloatValueNode
  | StringValueNode
  | BooleanValueNode
  | NullValueNode
  | EnumValueNode
  | ListValueNode
  | ObjectValueNode
  | ObjectFieldNode
  | DirectiveNode
  | NamedTypeNode
  | ListTypeNode
  | NonNullTypeNode
  | SchemaDefinitionNode
  | OperationTypeDefinitionNode
  | ScalarTypeDefinitionNode
  | ObjectTypeDefinitionNode
  | FieldDefinitionNode
  | InputValueDefinitionNode
  | InterfaceTypeDefinitionNode
  | UnionTypeDefinitionNode
  | EnumTypeDefinitionNode
  | EnumValueDefinitionNode
  | InputObjectTypeDefinitionNode
  | DirectiveDefinitionNode
  | SchemaExtensionNode
  | ScalarTypeExtensionNode
  | ObjectTypeExtensionNode
  | InterfaceTypeExtensionNode
  | UnionTypeExtensionNode
  | EnumTypeExtensionNode
  | InputObjectTypeExtensionNode
  | DirectiveExtensionNode
  | TypeCoordinateNode
  | MemberCoordinateNode
  | ArgumentCoordinateNode
  | DirectiveCoordinateNode
  | DirectiveArgumentCoordinateNode;
/** An identifier in a GraphQL document. */
interface NameNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.NAME;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Parsed value represented by this node. */
  readonly value: string;
}
/** The root AST node for a parsed GraphQL document. */
interface DocumentNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.DOCUMENT;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Top-level executable and type-system definitions in this document. */
  readonly definitions: ReadonlyArray<DefinitionNode>;
  /** The number of lexical tokens parsed for this document, if token counting was enabled. */
  readonly tokenCount?: number | undefined;
}
/** Any top-level definition that may appear in a GraphQL document. */
declare type DefinitionNode =
  | ExecutableDefinitionNode
  | TypeSystemDefinitionNode
  | TypeSystemExtensionNode;
/** Any executable definition that may appear in an operation document. */
declare type ExecutableDefinitionNode =
  | OperationDefinitionNode
  | FragmentDefinitionNode;
/** A query, mutation, or subscription operation definition. */
interface OperationDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.OPERATION_DEFINITION;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The operation selected for execution. */
  readonly operation: OperationTypeNode;
  /** Name node identifying this AST node. */
  readonly name?: NameNode;
  /** Variable definitions declared by this operation or fragment. */
  readonly variableDefinitions?: ReadonlyArray<VariableDefinitionNode>;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<DirectiveNode>;
  /** Selections made by this operation, field, or fragment. */
  readonly selectionSet: SelectionSetNode;
}
/**
 * The operation types supported by GraphQL executable definitions.
 * @category Kinds
 */
declare enum OperationTypeNode {
  /** A query operation. */
  QUERY = 'query',
  /** A mutation operation. */
  MUTATION = 'mutation',
  /** A subscription operation. */
  SUBSCRIPTION = 'subscription',
}

/** A variable declaration in an operation or legacy fragment definition. */
interface VariableDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.VARIABLE_DEFINITION;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The variable being defined or referenced. */
  readonly variable: VariableNode;
  /** The GraphQL type reference or runtime type for this element. */
  readonly type: TypeNode;
  /** Default value used when no explicit value is supplied. */
  readonly defaultValue?: ConstValueNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
}
/** A variable reference, such as `$id`. */
interface VariableNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.VARIABLE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
}
/** A set of fields and fragments selected from an object, interface, or union. */
interface SelectionSetNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  kind: Kind.SELECTION_SET;
  /** The source location for this AST node, if location tracking was enabled. */
  loc?: Location;
  /** Fields and fragments contained in this selection set. */
  selections: ReadonlyArray<SelectionNode>;
}
/** Any selection that may appear inside a selection set. */
declare type SelectionNode =
  | FieldNode
  | FragmentSpreadNode
  | InlineFragmentNode;
/** A field selected in an executable GraphQL document. */
interface FieldNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.FIELD;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The response-key alias for this field, if one was supplied. */
  readonly alias?: NameNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Arguments supplied to this field, directive, or coordinate. */
  readonly arguments?: ReadonlyArray<ArgumentNode>;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<DirectiveNode>;
  /** Selections made by this operation, field, or fragment. */
  readonly selectionSet?: SelectionSetNode;
}
/** An argument supplied to a field or directive. */
interface ArgumentNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.ARGUMENT;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Parsed value represented by this node. */
  readonly value: ValueNode;
}
/** An argument node whose value is guaranteed to be constant. */
interface ConstArgumentNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.ARGUMENT;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Parsed value represented by this node. */
  readonly value: ConstValueNode;
}
/** A named fragment spread, such as `...userFields`. */
interface FragmentSpreadNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.FRAGMENT_SPREAD;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<DirectiveNode>;
}
/** An inline fragment spread with an optional type condition. */
interface InlineFragmentNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.INLINE_FRAGMENT;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The type condition that limits where this fragment applies. */
  readonly typeCondition?: NamedTypeNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<DirectiveNode>;
  /** Selections made by this operation, field, or fragment. */
  readonly selectionSet: SelectionSetNode;
}
/** A reusable fragment definition declared in an executable document. */
interface FragmentDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.FRAGMENT_DEFINITION;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /**
   * Deprecated variable definitions declared by this legacy fragment
   * definition. This legacy fragment variable syntax will be removed in v17.
   * Move variable definitions to operations for spec-compliant documents; if
   * you need variables or arguments scoped to fragments, v17 has a more
   * complete experimental fragment-arguments feature.
   * @deprecated variableDefinitions will be removed in v17.0.0
   */
  readonly variableDefinitions?: ReadonlyArray<VariableDefinitionNode>;
  /** The type condition that limits where this fragment applies. */
  readonly typeCondition: NamedTypeNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<DirectiveNode>;
  /** Selections made by this operation, field, or fragment. */
  readonly selectionSet: SelectionSetNode;
}
/** Any value literal that may appear in an executable GraphQL document. */
declare type ValueNode =
  | VariableNode
  | IntValueNode
  | FloatValueNode
  | StringValueNode
  | BooleanValueNode
  | NullValueNode
  | EnumValueNode
  | ListValueNode
  | ObjectValueNode;
/** Any value literal that is guaranteed not to contain a variable reference. */
declare type ConstValueNode =
  | IntValueNode
  | FloatValueNode
  | StringValueNode
  | BooleanValueNode
  | NullValueNode
  | EnumValueNode
  | ConstListValueNode
  | ConstObjectValueNode;
/** An integer value literal. */
interface IntValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.INT;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Parsed value represented by this node. */
  readonly value: string;
}
/** A floating-point value literal. */
interface FloatValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.FLOAT;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Parsed value represented by this node. */
  readonly value: string;
}
/** A string value literal. */
interface StringValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.STRING;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Parsed value represented by this node. */
  readonly value: string;
  /** Whether this string was parsed from block string syntax. */
  readonly block?: boolean;
}
/** A boolean value literal. */
interface BooleanValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.BOOLEAN;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Parsed value represented by this node. */
  readonly value: boolean;
}
/** A null value literal. */
interface NullValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.NULL;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
}
/** An enum value literal. */
interface EnumValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.ENUM;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Parsed value represented by this node. */
  readonly value: string;
}
/** A list value literal. */
interface ListValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.LIST;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Values contained in this enum, list, or input-object definition. */
  readonly values: ReadonlyArray<ValueNode>;
}
/** A list value literal whose elements are all constant values. */
interface ConstListValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.LIST;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Values contained in this enum, list, or input-object definition. */
  readonly values: ReadonlyArray<ConstValueNode>;
}
/** An input object value literal. */
interface ObjectValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.OBJECT;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Fields declared by this object, interface, input object, or literal. */
  readonly fields: ReadonlyArray<ObjectFieldNode>;
}
/** An input object value literal whose fields are all constant values. */
interface ConstObjectValueNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.OBJECT;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Fields declared by this object, interface, input object, or literal. */
  readonly fields: ReadonlyArray<ConstObjectFieldNode>;
}
/** A field inside an input object value literal. */
interface ObjectFieldNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.OBJECT_FIELD;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Parsed value represented by this node. */
  readonly value: ValueNode;
}
/** A field inside a constant input object value literal. */
interface ConstObjectFieldNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.OBJECT_FIELD;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Parsed value represented by this node. */
  readonly value: ConstValueNode;
}
/** A directive applied to an executable or type-system location. */
interface DirectiveNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.DIRECTIVE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Arguments supplied to this field, directive, or coordinate. */
  readonly arguments?: ReadonlyArray<ArgumentNode>;
}
/** A directive whose arguments are all constant values. */
interface ConstDirectiveNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.DIRECTIVE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Arguments supplied to this field, directive, or coordinate. */
  readonly arguments?: ReadonlyArray<ConstArgumentNode>;
}
/** Any GraphQL type reference AST node. */
declare type TypeNode = NamedTypeNode | ListTypeNode | NonNullTypeNode;
/** A named type reference. */
interface NamedTypeNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.NAMED_TYPE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
}
/** A list type reference. */
interface ListTypeNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.LIST_TYPE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The GraphQL type reference or runtime type for this element. */
  readonly type: TypeNode;
}
/** A non-null type reference. */
interface NonNullTypeNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.NON_NULL_TYPE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The GraphQL type reference or runtime type for this element. */
  readonly type: NamedTypeNode | ListTypeNode;
}
/** Any type-system definition that may appear in a schema document. */
declare type TypeSystemDefinitionNode =
  | SchemaDefinitionNode
  | TypeDefinitionNode
  | DirectiveDefinitionNode;
/** A schema definition in a type-system document. */
interface SchemaDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.SCHEMA_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Root operation types declared by this schema definition or extension. */
  readonly operationTypes: ReadonlyArray<OperationTypeDefinitionNode>;
}
/** A root operation type declaration inside a schema definition or extension. */
interface OperationTypeDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.OPERATION_TYPE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The operation selected for execution. */
  readonly operation: OperationTypeNode;
  /** The GraphQL type reference or runtime type for this element. */
  readonly type: NamedTypeNode;
}
/** Any named type definition that may appear in a schema document. */
declare type TypeDefinitionNode =
  | ScalarTypeDefinitionNode
  | ObjectTypeDefinitionNode
  | InterfaceTypeDefinitionNode
  | UnionTypeDefinitionNode
  | EnumTypeDefinitionNode
  | InputObjectTypeDefinitionNode;
/** A scalar type definition in a type-system document. */
interface ScalarTypeDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.SCALAR_TYPE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
}
/** An object type definition in a type-system document. */
interface ObjectTypeDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.OBJECT_TYPE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Interfaces implemented by this object or interface type. */
  readonly interfaces?: ReadonlyArray<NamedTypeNode>;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Fields declared by this object, interface, input object, or literal. */
  readonly fields?: ReadonlyArray<FieldDefinitionNode>;
}
/** A field definition declared by an object or interface type. */
interface FieldDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.FIELD_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Arguments supplied to this field, directive, or coordinate. */
  readonly arguments?: ReadonlyArray<InputValueDefinitionNode>;
  /** The GraphQL type reference or runtime type for this element. */
  readonly type: TypeNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
}
/** An argument or input-field definition. */
interface InputValueDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.INPUT_VALUE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** The GraphQL type reference or runtime type for this element. */
  readonly type: TypeNode;
  /** Default value used when no explicit value is supplied. */
  readonly defaultValue?: ConstValueNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
}
/** An interface type definition in a type-system document. */
interface InterfaceTypeDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.INTERFACE_TYPE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Interfaces implemented by this object or interface type. */
  readonly interfaces?: ReadonlyArray<NamedTypeNode>;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Fields declared by this object, interface, input object, or literal. */
  readonly fields?: ReadonlyArray<FieldDefinitionNode>;
}
/** A union type definition in a type-system document. */
interface UnionTypeDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.UNION_TYPE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Object types that belong to this union type. */
  readonly types?: ReadonlyArray<NamedTypeNode>;
}
/** An enum type definition in a type-system document. */
interface EnumTypeDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.ENUM_TYPE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Values contained in this enum, list, or input-object definition. */
  readonly values?: ReadonlyArray<EnumValueDefinitionNode>;
}
/** An enum value definition. */
interface EnumValueDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.ENUM_VALUE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
}
/** An input object type definition in a type-system document. */
interface InputObjectTypeDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.INPUT_OBJECT_TYPE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Fields declared by this object, interface, input object, or literal. */
  readonly fields?: ReadonlyArray<InputValueDefinitionNode>;
}
/** A directive definition in a type-system document. */
interface DirectiveDefinitionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.DIRECTIVE_DEFINITION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** The optional GraphQL description associated with this definition. */
  readonly description?: StringValueNode;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Arguments supplied to this field, directive, or coordinate. */
  readonly arguments?: ReadonlyArray<InputValueDefinitionNode>;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Whether this directive may appear more than once at the same location. */
  readonly repeatable: boolean;
  /** Locations where this directive may be applied. */
  readonly locations: ReadonlyArray<NameNode>;
}
/** Any type-system extension that may appear in a schema extension document. */
declare type TypeSystemExtensionNode =
  | SchemaExtensionNode
  | TypeExtensionNode
  | DirectiveExtensionNode;
/** A schema extension in a type-system document. */
interface SchemaExtensionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.SCHEMA_EXTENSION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Root operation types declared by this schema definition or extension. */
  readonly operationTypes?: ReadonlyArray<OperationTypeDefinitionNode>;
}
/** Any named type extension that may appear in a schema extension document. */
declare type TypeExtensionNode =
  | ScalarTypeExtensionNode
  | ObjectTypeExtensionNode
  | InterfaceTypeExtensionNode
  | UnionTypeExtensionNode
  | EnumTypeExtensionNode
  | InputObjectTypeExtensionNode;
/** A scalar type extension. */
interface ScalarTypeExtensionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.SCALAR_TYPE_EXTENSION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
}
/** An object type extension. */
interface ObjectTypeExtensionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.OBJECT_TYPE_EXTENSION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Interfaces implemented by this object or interface type. */
  readonly interfaces?: ReadonlyArray<NamedTypeNode>;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Fields declared by this object, interface, input object, or literal. */
  readonly fields?: ReadonlyArray<FieldDefinitionNode>;
}
/** An interface type extension. */
interface InterfaceTypeExtensionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.INTERFACE_TYPE_EXTENSION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Interfaces implemented by this object or interface type. */
  readonly interfaces?: ReadonlyArray<NamedTypeNode>;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Fields declared by this object, interface, input object, or literal. */
  readonly fields?: ReadonlyArray<FieldDefinitionNode>;
}
/** A union type extension. */
interface UnionTypeExtensionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.UNION_TYPE_EXTENSION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Object types that belong to this union type. */
  readonly types?: ReadonlyArray<NamedTypeNode>;
}
/** An enum type extension. */
interface EnumTypeExtensionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.ENUM_TYPE_EXTENSION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Values contained in this enum, list, or input-object definition. */
  readonly values?: ReadonlyArray<EnumValueDefinitionNode>;
}
/** An input object type extension. */
interface InputObjectTypeExtensionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.INPUT_OBJECT_TYPE_EXTENSION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
  /** Fields declared by this object, interface, input object, or literal. */
  readonly fields?: ReadonlyArray<InputValueDefinitionNode>;
}
/** A directive extension. */
interface DirectiveExtensionNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.DIRECTIVE_EXTENSION;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** Directives available in this schema or applied to this AST node. */
  readonly directives?: ReadonlyArray<ConstDirectiveNode>;
}
/** A schema coordinate that refers to a named type. */
interface TypeCoordinateNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.TYPE_COORDINATE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
}
/** A schema coordinate that refers to a member of a named type. */
interface MemberCoordinateNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.MEMBER_COORDINATE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** The member name referenced by this schema coordinate. */
  readonly memberName: NameNode;
}
/** A schema coordinate that refers to a field or directive argument. */
interface ArgumentCoordinateNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.ARGUMENT_COORDINATE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** The field name referenced by this schema coordinate. */
  readonly fieldName: NameNode;
  /** The argument name referenced by this schema coordinate. */
  readonly argumentName: NameNode;
}
/** A schema coordinate that refers to a directive. */
interface DirectiveCoordinateNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.DIRECTIVE_COORDINATE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
}
/** A schema coordinate that refers to a directive argument. */
interface DirectiveArgumentCoordinateNode {
  /** The discriminator identifying the concrete AST or introspection kind. */
  readonly kind: Kind.DIRECTIVE_ARGUMENT_COORDINATE;
  /** The source location for this AST node, if location tracking was enabled. */
  readonly loc?: Location;
  /** Name node identifying this AST node. */
  readonly name: NameNode;
  /** The argument name referenced by this schema coordinate. */
  readonly argumentName: NameNode;
}

/** @category Source */

/** Represents a location in a Source. */
interface SourceLocation {
  /** One-indexed line number in the source document. */
  readonly line: number;
  /** One-indexed column number in the source document. */
  readonly column: number;
}

/** @category Errors */

/**
 * Custom extensions
 * @remarks
 * Use a unique identifier name for your extension, for example the name of
 * your library or project. Do not use a shortened identifier as this increases
 * the risk of conflicts. We recommend you add at most one extension field,
 * an object which can contain all the values you need.
 */
interface GraphQLErrorExtensions {
  [attributeName: string]: unknown;
}
/**
 * Custom formatted extensions
 * @remarks
 * Use a unique identifier name for your extension, for example the name of
 * your library or project. Do not use a shortened identifier as this increases
 * the risk of conflicts. We recommend you add at most one extension field,
 * an object which can contain all the values you need.
 */
interface GraphQLFormattedErrorExtensions {
  [attributeName: string]: unknown;
}
/** Options used to construct a GraphQLError. */
interface GraphQLErrorOptions {
  /** AST node or nodes associated with this error. */
  nodes?: ReadonlyArray<ASTNode> | ASTNode | null;
  /** Source document used to derive error locations. */
  source?: Maybe<Source>;
  /** Character offsets in the source document associated with this error. */
  positions?: Maybe<ReadonlyArray<number>>;
  /** Response path where this error occurred during execution. */
  path?: Maybe<ReadonlyArray<string | number>>;
  /** Original error that caused this GraphQLError, if one exists. */
  originalError?: Maybe<
    Error & {
      /** Extension fields associated with this value. */
      readonly extensions?: unknown;
    }
  >;
  /** Extension fields to include in the formatted result. */
  extensions?: Maybe<GraphQLErrorExtensions>;
}
/**
 * A GraphQLError describes an Error found during the parse, validate, or
 * execute phases of performing a GraphQL operation. In addition to a message
 * and stack trace, it also includes information about the locations in a
 * GraphQL document and/or execution result that correspond to the Error.
 */
declare class GraphQLError extends Error {
  /**
   * An array of `{ line, column }` locations within the source GraphQL document
   * which correspond to this error.
   *
   * Errors during validation often contain multiple locations, for example to
   * point out two things with the same name. Errors during execution include a
   * single location, the field which produced the error.
   *
   * Enumerable, and appears in the result of JSON.stringify().
   */
  readonly locations: ReadonlyArray<SourceLocation> | undefined;
  /**
   * An array describing the JSON-path into the execution response which
   * corresponds to this error. Only included for errors during execution.
   *
   * Enumerable, and appears in the result of JSON.stringify().
   */
  readonly path: ReadonlyArray<string | number> | undefined;
  /** An array of GraphQL AST Nodes corresponding to this error. */
  readonly nodes: ReadonlyArray<ASTNode> | undefined;
  /**
   * The source GraphQL document for the first location of this error.
   *
   * Note that if this Error represents more than one node, the source may not
   * represent nodes after the first node.
   */
  readonly source: Source | undefined;
  /**
   * An array of character offsets within the source GraphQL document
   * which correspond to this error.
   */
  readonly positions: ReadonlyArray<number> | undefined;
  /** Original error that caused this GraphQLError, if one exists. */
  readonly originalError: Error | undefined;
  /** Extension fields to add to the formatted error. */
  readonly extensions: GraphQLErrorExtensions;
  /**
   * Creates a GraphQLError instance.
   * @param message - Human-readable error message.
   * @param options - Error metadata such as source locations, response path, original error, and extensions.
   * This positional-arguments constructor overload is deprecated. Use the
   * `GraphQLError(message, options)` overload instead.
   * @example
   * ```ts
   * // Create an error from AST nodes and response metadata.
   * import { parse } from 'graphql/language';
   * import { GraphQLError } from 'graphql/error';
   *
   * const document = parse('{ greeting }');
   * const fieldNode = document.definitions[0].selectionSet.selections[0];
   * const error = new GraphQLError('Cannot query this field.', {
   *   nodes: fieldNode,
   *   path: ['greeting'],
   *   extensions: { code: 'FORBIDDEN' },
   * });
   *
   * error.message; // => 'Cannot query this field.'
   * error.locations; // => [{ line: 1, column: 3 }]
   * error.path; // => ['greeting']
   * error.extensions; // => { code: 'FORBIDDEN' }
   * ```
   * @example
   * ```ts
   * // This variant derives locations from source positions and preserves the original error.
   * import { Source } from 'graphql/language';
   * import { GraphQLError } from 'graphql/error';
   *
   * const source = new Source('{ greeting }');
   * const originalError = new Error('Database unavailable.');
   * const error = new GraphQLError('Resolver failed.', {
   *   source,
   *   positions: [2],
   *   path: ['greeting'],
   *   originalError,
   * });
   *
   * error.locations; // => [{ line: 1, column: 3 }]
   * error.path; // => ['greeting']
   * error.originalError; // => originalError
   * ```
   */
  constructor(message: string, options?: GraphQLErrorOptions);
  /**
   * Creates a GraphQLError instance using the legacy positional constructor.
   * This deprecated overload will be removed in v17. Prefer the
   * `GraphQLErrorOptions` object overload, which keeps optional error metadata
   * in a single options bag.
   * @param message - Human-readable error message.
   * @param nodes - AST node or nodes associated with this error.
   * @param source - Source document used to derive error locations.
   * @param positions - Character offsets in the source document associated with
   * this error.
   * @param path - Response path where this error occurred during execution.
   * @param originalError - Original error that caused this GraphQLError, if one
   * exists.
   * @param extensions - Extension fields to include in the formatted error.
   * @example
   * ```ts
   * import { Source } from 'graphql/language';
   * import { GraphQLError } from 'graphql/error';
   *
   * const source = new Source('{ greeting }');
   * const originalError = new Error('Database unavailable.');
   * const error = new GraphQLError(
   *   'Resolver failed.',
   *   undefined,
   *   source,
   *   [2],
   *   ['greeting'],
   *   originalError,
   *   { code: 'INTERNAL' },
   * );
   *
   * error.locations; // => [{ line: 1, column: 3 }]
   * error.path; // => ['greeting']
   * error.originalError; // => originalError
   * error.extensions; // => { code: 'INTERNAL' }
   * ```
   * @deprecated Please use the `GraphQLErrorOptions` constructor overload instead.
   */
  constructor(
    message: string,
    nodes?: ReadonlyArray<ASTNode> | ASTNode | null,
    source?: Maybe<Source>,
    positions?: Maybe<ReadonlyArray<number>>,
    path?: Maybe<ReadonlyArray<string | number>>,
    originalError?: Maybe<
      Error & {
        readonly extensions?: unknown;
      }
    >,
    extensions?: Maybe<GraphQLErrorExtensions>,
  );
  /**
   * Returns the value used by `Object.prototype.toString`.
   * @returns The built-in string tag for this object.
   */
  get [Symbol.toStringTag](): string;
  /**
   * Returns this error as a human-readable message with source locations.
   * @returns The formatted error string.
   * @example
   * ```ts
   * import { Source } from 'graphql/language';
   * import { GraphQLError } from 'graphql/error';
   *
   * const error = new GraphQLError('Cannot query field "name".', {
   *   source: new Source('{ name }'),
   *   positions: [2],
   * });
   *
   * error.toString(); // => 'Cannot query field "name".\n\nGraphQL request:1:3\n1 | { name }\n  |   ^'
   * ```
   */
  toString(): string;
  /**
   * Returns the JSON representation used when this object is serialized.
   * @returns The JSON-serializable representation.
   * @example
   * ```ts
   * import { GraphQLError } from 'graphql/error';
   *
   * const error = new GraphQLError('Resolver failed.', {
   *   path: ['viewer', 'name'],
   *   extensions: { code: 'INTERNAL' },
   * });
   *
   * error.toJSON(); // => { message: 'Resolver failed.', path: ['viewer', 'name'], extensions: { code: 'INTERNAL' } }
   * ```
   */
  toJSON(): GraphQLFormattedError;
}
/** See: https://spec.graphql.org/draft/#sec-Errors */
interface GraphQLFormattedError {
  /**
   * A short, human-readable summary of the problem that **SHOULD NOT** change
   * from occurrence to occurrence of the problem, except for purposes of
   * localization.
   */
  readonly message: string;
  /**
   * If an error can be associated to a particular point in the requested
   * GraphQL document, it should contain a list of locations.
   */
  readonly locations?: ReadonlyArray<SourceLocation>;
  /**
   * If an error can be associated to a particular field in the GraphQL result,
   * it _must_ contain an entry with the key `path` that details the path of
   * the response field which experienced the error. This allows clients to
   * identify whether a null result is intentional or caused by a runtime error.
   */
  readonly path?: ReadonlyArray<string | number>;
  /**
   * Reserved for implementors to extend the protocol however they see fit,
   * and hence there are no additional restrictions on its contents.
   */
  readonly extensions?: GraphQLFormattedErrorExtensions;
}

interface DocumentTypeDecoration<TResult, TVariables> {
    /**
     * This type is used to ensure that the variables you pass in to the query are assignable to Variables
     * and that the Result is assignable to whatever you pass your result to. The method is never actually
     * implemented, but the type is valid because we list it as optional
     */
    __apiType?: (variables: TVariables) => TResult;
}
interface TypedDocumentNode<TResult = {
    [key: string]: any;
}, TVariables = {
    [key: string]: any;
}> extends DocumentNode, DocumentTypeDecoration<TResult, TVariables> {
}

type RemoveIndex<T> = {
    [K in keyof T as string extends K ? never : number extends K ? never : K]: T[K];
};
type MaybeLazy<T> = T | (() => T);
type MaybePromise<T> = T | Promise<T>;

type Fetch = typeof fetch;
/**
 * 'None' will throw whenever the response contains errors
 *
 * 'Ignore' will ignore incoming errors and resolve like no errors occurred
 *
 * 'All' will return both the errors and data
 */
type ErrorPolicy = 'none' | 'ignore' | 'all';
interface JsonSerializer {
    stringify: (obj: any) => string;
    parse: (obj: string) => unknown;
}
interface AdditionalRequestOptions {
    jsonSerializer?: JsonSerializer;
    /**
     * Decide how to handle GraphQLErrors in response
     */
    errorPolicy?: ErrorPolicy;
}
type Variables = object;
interface GraphQLResponse<T = unknown> {
    data?: T;
    errors?: GraphQLError[];
    extensions?: unknown;
    status: number;
    headers: Headers;
    /**
     * The response body text. Useful for debugging non-GraphQL responses
     * (e.g., 401/403 errors that return plain JSON instead of GraphQL).
     */
    body: string;
    [key: string]: unknown;
}
interface GraphQLRequestContext<V extends Variables = Variables> {
    query: string | string[];
    variables?: V;
}
type RequestDocument = string | String | DocumentNode;
/**
 * TypedDocumentString is generated by \@graphql-codegen with `documentMode: 'string'`.
 * It's a boxed String with type metadata for result and variables types.
 * @see https://github.com/graffle-js/graffle/issues/1467
 */
type TypedDocumentString<$Result, $Variables> = String & DocumentTypeDecoration<$Result, $Variables>;
interface GraphQLClientResponse<Data> {
    status: number;
    headers: Headers;
    /**
     * The response body text. Useful for debugging non-GraphQL responses
     * (e.g., 401/403 errors that return plain JSON instead of GraphQL).
     */
    body: string;
    data: Data;
    extensions?: unknown;
    errors?: GraphQLError[];
}
type HTTPMethodInput = 'GET' | 'POST' | 'get' | 'post';
interface RequestConfig extends Omit<RequestInit, 'headers' | 'method'>, AdditionalRequestOptions {
    fetch?: Fetch;
    method?: HTTPMethodInput;
    headers?: MaybeLazy<HeadersInit>;
    requestMiddleware?: RequestMiddleware;
    responseMiddleware?: ResponseMiddleware;
    jsonSerializer?: JsonSerializer;
    excludeOperationName?: boolean;
}
type RawRequestOptions<V extends Variables = Variables> = {
    query: string;
    requestHeaders?: HeadersInit;
    signal?: RequestInit['signal'];
} & (V extends Record<any, never> ? {
    variables?: V;
} : keyof RemoveIndex<V> extends never ? {
    variables?: V;
} : {
    variables: V;
});
type RequestOptions<V extends Variables = Variables, T = unknown> = {
    document: RequestDocument | TypedDocumentNode<T, V> | TypedDocumentString<T, V>;
    requestHeaders?: HeadersInit;
    signal?: RequestInit['signal'];
} & (V extends Record<any, never> ? {
    variables?: V;
} : keyof RemoveIndex<V> extends never ? {
    variables?: V;
} : {
    variables: V;
});
type ResponseMiddleware = (response: GraphQLClientResponse<unknown> | ClientError | Error, request: RequestInitExtended) => MaybePromise<void>;
type RequestMiddleware<V extends Variables = Variables> = (request: RequestInitExtended<V>) => RequestInitExtended | Promise<RequestInitExtended>;
type RequestInitExtended<V extends Variables = Variables> = RequestInit & {
    url: string;
    operationName?: string;
    variables?: V;
};
type VariablesAndRequestHeadersArgs<V extends Variables> = V extends Record<any, never> ? [variables?: V, requestHeaders?: HeadersInit] : keyof RemoveIndex<V> extends never ? [variables?: V, requestHeaders?: HeadersInit] : [variables: V, requestHeaders?: HeadersInit];

declare class ClientError extends Error {
    response: GraphQLResponse;
    request: GraphQLRequestContext;
    constructor(response: GraphQLResponse, request: GraphQLRequestContext);
    private static extractMessage;
}

type BatchRequestDocument<V extends Variables = Variables> = {
    document: RequestDocument;
    variables?: V;
};
interface BatchRequestsOptions<V extends Variables = Variables> {
    documents: BatchRequestDocument<V>[];
    requestHeaders?: HeadersInit;
    signal?: RequestInit['signal'];
}
type BatchResult = [Result, ...Result[]];
interface Result<Data extends object = object> {
    data: Data;
}

/**
 * GraphQL Client.
 */
declare class GraphQLClient {
    private url;
    readonly requestConfig: RequestConfig;
    constructor(url: string, requestConfig?: RequestConfig);
    /**
     * Send a GraphQL query to the server.
     */
    rawRequest: RawRequestMethod;
    /**
     * Send a GraphQL document to the server.
     */
    request<T = any, V extends Variables = Variables>(document: RequestDocument | TypedDocumentNode<T, V> | TypedDocumentString<T, V>, ...variablesAndRequestHeaders: VariablesAndRequestHeadersArgs<V>): Promise<T>;
    request<T = any, V extends Variables = Variables>(options: RequestOptions<V, T>): Promise<T>;
    /**
     * Send GraphQL documents in batch to the server.
     */
    batchRequests<$BatchResult extends BatchResult, $Variables extends Variables = Variables>(documents: BatchRequestDocument<$Variables>[], requestHeaders?: HeadersInit): Promise<$BatchResult>;
    batchRequests<$BatchResult extends BatchResult, $Variables extends Variables = Variables>(options: BatchRequestsOptions<$Variables>): Promise<$BatchResult>;
    setHeaders(headers: HeadersInit): this;
    /**
     * Attach a header to the client. All subsequent requests will have this header.
     */
    setHeader(key: string, value: string): this;
    /**
     * Change the client endpoint. All subsequent requests will send to this endpoint.
     */
    setEndpoint(value: string): this;
}
interface RawRequestMethod {
    <T = any, V extends Variables = Variables>(query: string, variables?: V, requestHeaders?: HeadersInit): Promise<GraphQLClientResponse<T>>;
    <T, V extends Variables = Variables>(options: RawRequestOptions<V>): Promise<GraphQLClientResponse<T>>;
}

interface ExceptionWithCode {
    code: string | number;
    name?: string;
    message?: string;
    stack?: string;
}
interface ExceptionWithMessage {
    code?: string | number;
    message: string;
    name?: string;
    stack?: string;
}
interface ExceptionWithName {
    code?: string | number;
    message?: string;
    name: string;
    stack?: string;
}
/**
 * Defines Exception.
 *
 * string or an object with one of (message or name or code) and optional stack
 *
 * @since 1.0.0
 */
type Exception = ExceptionWithCode | ExceptionWithMessage | ExceptionWithName | string;

/**
 * Defines High-Resolution Time.
 *
 * The first number, HrTime[0], is UNIX Epoch time in seconds since 00:00:00 UTC on 1 January 1970.
 * The second number, HrTime[1], represents the partial second elapsed since Unix Epoch time represented by first number in nanoseconds.
 * For example, 2021-01-01T12:30:10.150Z in UNIX Epoch time in milliseconds is represented as 1609504210150.
 * The first number is calculated by converting and truncating the Epoch time in milliseconds to seconds:
 * HrTime[0] = Math.trunc(1609504210150 / 1000) = 1609504210.
 * The second number is calculated by converting the digits after the decimal point of the subtraction, (1609504210150 / 1000) - HrTime[0], to nanoseconds:
 * HrTime[1] = Number((1609504210.150 - HrTime[0]).toFixed(9)) * 1e9 = 150000000.
 * This is represented in HrTime format as [1609504210, 150000000].
 *
 * @since 1.0.0
 */
type HrTime = [number, number];
/**
 * Defines TimeInput.
 *
 * hrtime, epoch milliseconds, performance.now() or Date
 *
 * @since 1.0.0
 */
type TimeInput = HrTime | number | Date;

/**
 * Attributes is a map from string to attribute values.
 *
 * Note: only the own enumerable keys are counted as valid attribute keys.
 *
 * @since 1.3.0
 */
interface Attributes {
    [attributeKey: string]: AttributeValue | undefined;
}
/**
 * Attribute values may be any non-nullish primitive value except an object.
 *
 * null or undefined attribute values are invalid and will result in undefined behavior.
 *
 * @since 1.3.0
 */
type AttributeValue = string | number | boolean | Array<null | undefined | string> | Array<null | undefined | number> | Array<null | undefined | boolean>;

/**
 * @deprecated please use {@link Attributes}
 * @since 1.0.0
 */
type SpanAttributes = Attributes;
/**
 * @deprecated please use {@link AttributeValue}
 * @since 1.0.0
 */
type SpanAttributeValue = AttributeValue;

/**
 * @since 1.0.0
 */
interface TraceState {
    /**
     * Create a new TraceState which inherits from this TraceState and has the
     * given key set.
     * The new entry will always be added in the front of the list of states.
     *
     * @param key key of the TraceState entry.
     * @param value value of the TraceState entry.
     */
    set(key: string, value: string): TraceState;
    /**
     * Return a new TraceState which inherits from this TraceState but does not
     * contain the given key.
     *
     * @param key the key for the TraceState entry to be removed.
     */
    unset(key: string): TraceState;
    /**
     * Returns the value to which the specified key is mapped, or `undefined` if
     * this map contains no mapping for the key.
     *
     * @param key with which the specified value is to be associated.
     * @returns the value to which the specified key is mapped, or `undefined` if
     *     this map contains no mapping for the key.
     */
    get(key: string): string | undefined;
    /**
     * Serializes the TraceState to a `list` as defined below. The `list` is a
     * series of `list-members` separated by commas `,`, and a list-member is a
     * key/value pair separated by an equals sign `=`. Spaces and horizontal tabs
     * surrounding `list-members` are ignored. There can be a maximum of 32
     * `list-members` in a `list`.
     *
     * @returns the serialized string.
     */
    serialize(): string;
}

/**
 * A SpanContext represents the portion of a {@link Span} which must be
 * serialized and propagated along side of a {@link Baggage}.
 *
 * @since 1.0.0
 */
interface SpanContext {
    /**
     * The ID of the trace that this span belongs to. It is worldwide unique
     * with practically sufficient probability by being made as 16 randomly
     * generated bytes, encoded as a 32 lowercase hex characters corresponding to
     * 128 bits.
     */
    traceId: string;
    /**
     * The ID of the Span. It is globally unique with practically sufficient
     * probability by being made as 8 randomly generated bytes, encoded as a 16
     * lowercase hex characters corresponding to 64 bits.
     */
    spanId: string;
    /**
     * Only true if the SpanContext was propagated from a remote parent.
     */
    isRemote?: boolean;
    /**
     * Trace flags to propagate.
     *
     * It is represented as 1 byte (bitmap). Bit to represent whether trace is
     * sampled or not. When set, the least significant bit documents that the
     * caller may have recorded trace data. A caller who does not record trace
     * data out-of-band leaves this flag unset.
     *
     * see {@link TraceFlags} for valid flag values.
     */
    traceFlags: number;
    /**
     * Tracing-system-specific info to propagate.
     *
     * The tracestate field value is a `list` as defined below. The `list` is a
     * series of `list-members` separated by commas `,`, and a list-member is a
     * key/value pair separated by an equals sign `=`. Spaces and horizontal tabs
     * surrounding `list-members` are ignored. There can be a maximum of 32
     * `list-members` in a `list`.
     * More Info: https://www.w3.org/TR/trace-context/#tracestate-field
     *
     * Examples:
     *     Single tracing system (generic format):
     *         tracestate: rojo=00f067aa0ba902b7
     *     Multiple tracing systems (with different formatting):
     *         tracestate: rojo=00f067aa0ba902b7,congo=t61rcWkgMzE
     */
    traceState?: TraceState;
}

/**
 * @since 1.0.0
 */
interface SpanStatus {
    /** The status code of this message. */
    code: SpanStatusCode;
    /** A developer-facing error message. */
    message?: string;
}
/**
 * An enumeration of status codes.
 *
 * @since 1.0.0
 */
declare enum SpanStatusCode {
    /**
     * The default status.
     */
    UNSET = 0,
    /**
     * The operation has been validated by an Application developer or
     * Operator to have completed successfully.
     */
    OK = 1,
    /**
     * The operation contains an error.
     */
    ERROR = 2
}

/**
 * A pointer from the current {@link Span} to another span in the same trace or
 * in a different trace.
 * Few examples of Link usage.
 * 1. Batch Processing: A batch of elements may contain elements associated
 *    with one or more traces/spans. Since there can only be one parent
 *    SpanContext, Link is used to keep reference to SpanContext of all
 *    elements in the batch.
 * 2. Public Endpoint: A SpanContext in incoming client request on a public
 *    endpoint is untrusted from service provider perspective. In such case it
 *    is advisable to start a new trace with appropriate sampling decision.
 *    However, it is desirable to associate incoming SpanContext to new trace
 *    initiated on service provider side so two traces (from Client and from
 *    Service Provider) can be correlated.
 *
 * @since 1.0.0
 */
interface Link {
    /** The {@link SpanContext} of a linked span. */
    context: SpanContext;
    /** A set of {@link SpanAttributes} on the link. */
    attributes?: SpanAttributes;
    /** Count of attributes of the link that were dropped due to collection limits */
    droppedAttributesCount?: number;
}

/**
 * An interface that represents a span. A span represents a single operation
 * within a trace. Examples of span might include remote procedure calls or a
 * in-process function calls to sub-components. A Trace has a single, top-level
 * "root" Span that in turn may have zero or more child Spans, which in turn
 * may have children.
 *
 * Spans are created by the {@link Tracer.startSpan} method.
 *
 * @since 1.0.0
 */
interface Span {
    /**
     * Returns the {@link SpanContext} object associated with this Span.
     *
     * Get an immutable, serializable identifier for this span that can be used
     * to create new child spans. Returned SpanContext is usable even after the
     * span ends.
     *
     * @returns the SpanContext object associated with this Span.
     */
    spanContext(): SpanContext;
    /**
     * Sets an attribute to the span.
     *
     * Sets a single Attribute with the key and value passed as arguments.
     *
     * @param key the key for this attribute.
     * @param value the value for this attribute. Setting a value null or
     *              undefined is invalid and will result in undefined behavior.
     */
    setAttribute(key: string, value: SpanAttributeValue): this;
    /**
     * Sets attributes to the span.
     *
     * @param attributes the attributes that will be added.
     *                   null or undefined attribute values
     *                   are invalid and will result in undefined behavior.
     */
    setAttributes(attributes: SpanAttributes): this;
    /**
     * Adds an event to the Span.
     *
     * @param name the name of the event.
     * @param [attributesOrStartTime] the attributes that will be added; these are
     *     associated with this event. Can be also a start time
     *     if type is {@type TimeInput} and 3rd param is undefined
     * @param [startTime] start time of the event.
     */
    addEvent(name: string, attributesOrStartTime?: SpanAttributes | TimeInput, startTime?: TimeInput): this;
    /**
     * Adds a single link to the span.
     *
     * Links added after the creation will not affect the sampling decision.
     * It is preferred span links be added at span creation.
     *
     * @param link the link to add.
     */
    addLink(link: Link): this;
    /**
     * Adds multiple links to the span.
     *
     * Links added after the creation will not affect the sampling decision.
     * It is preferred span links be added at span creation.
     *
     * @param links the links to add.
     */
    addLinks(links: Link[]): this;
    /**
     * Sets the status of the span.
     *
     * By default, a span has status {@link SpanStatusCode.UNSET}.
     * Calling this method overrides that default.
     *
     * The status codes have a total order: `OK > ERROR > UNSET`.
     *
     * - Once {@link SpanStatusCode.OK} is set, any further attempts to change
     *   the status are ignored.
     * - Any attempt to set {@link SpanStatusCode.UNSET} is always ignored.
     *
     * The `message` field is only used when {@link SpanStatusCode.ERROR} is set.
     * For all other status codes, `message` is ignored.
     *
     * @param status The {@link SpanStatus} to set.
     */
    setStatus(status: SpanStatus): this;
    /**
     * Updates the Span name.
     *
     * This will override the name provided via {@link Tracer.startSpan}.
     *
     * Upon this update, any sampling behavior based on Span name will depend on
     * the implementation.
     *
     * @param name the Span name.
     */
    updateName(name: string): this;
    /**
     * Marks the end of Span execution.
     *
     * Call to End of a Span MUST not have any effects on child spans. Those may
     * still be running and can be ended later.
     *
     * Do not return `this`. The Span generally should not be used after it
     * is ended so chaining is not desired in this context.
     *
     * @param [endTime] the time to set as Span's end time. If not provided,
     *     use the current time as the span's end time.
     */
    end(endTime?: TimeInput): void;
    /**
     * Returns the flag whether this span will be recorded.
     *
     * @returns true if this Span is active and recording information like events
     *     with the `AddEvent` operation and attributes using `setAttributes`.
     */
    isRecording(): boolean;
    /**
     * Sets exception as a span event
     * @param exception the exception the only accepted values are string or Error
     * @param [time] the time to set as Span's event time. If not provided,
     *     use the current time.
     */
    recordException(exception: Exception, time?: TimeInput): void;
}

/**
 * Tracer encapsulates the OpenTelemetry Tracer.
 */
declare class Tracer {
    private tracer;
    constructor(name: string);
    startSpan(name: string, attributes?: Attributes): Span;
    /**
     * Execute the functions with a custom span with the given name using startActiveSpan.
     * The function executed will use the parent context of the function (it can be another span
     * or the main function).
     *
     * @param name The name of the span
     * @param fn The functions to execute
     *
     * startActiveSpan returns the result of the executed functions.
     *
     * The span is automatically ended when the function is done.
     * The span is automatically marked as an error if the function throws an error.
     *
     *
     * @example
     * ```
     * return getTracer().startActiveSpan(name, async () => {
     *   return this.containerEcho("test").stdout()
     * })
     * ```
     */
    startActiveSpan<T>(name: string, fn: (span: Span) => Promise<T>, attributes?: Attributes): Promise<T>;
}

/**
 * Return a tracer to use with Dagger.
 *
 * The tracer is automatically initialized if not already done.
 * As a conveniance function, you can use `withTracingSpan` that automatically close
 * the span at the end of the function.
 *
 * You can add a custom name to the tracer based on your application.
 */
declare function getTracer(name?: string): Tracer;

declare const ERROR_CODES: {
    /**
     * {@link GraphQLRequestError}
     */
    readonly GraphQLRequestError: "D100";
    /**
     * {@link UnknownDaggerError}
     */
    readonly UnknownDaggerError: "D101";
    /**
     * {@link TooManyNestedObjectsError}
     */
    readonly TooManyNestedObjectsError: "D102";
    /**
     * {@link EngineSessionConnectParamsParseError}
     */
    readonly EngineSessionConnectParamsParseError: "D103";
    /**
     * {@link EngineSessionConnectionTimeoutError}
     */
    readonly EngineSessionConnectionTimeoutError: "D104";
    /**
     * {@link EngineSessionError}
     */
    readonly EngineSessionError: "D105";
    /**
     * {@link InitEngineSessionBinaryError}
     */
    readonly InitEngineSessionBinaryError: "D106";
    /**
     * {@link DockerImageRefValidationError}
     */
    readonly DockerImageRefValidationError: "D107";
    /**
     * {@link NotAwaitedRequestError}
     */
    readonly NotAwaitedRequestError: "D108";
    /**
     * (@link ExecError}
     */
    readonly ExecError: "D109";
    /**
     * {@link IntrospectionError}
     */
    readonly IntrospectionError: "D110";
};
type ErrorCodesType = typeof ERROR_CODES;
type ErrorNames = keyof ErrorCodesType;
type ErrorCodes = ErrorCodesType[ErrorNames];

interface DaggerSDKErrorOptions {
    cause?: Error;
}
/**
 * The base error. Every other error inherits this error.
 */
declare abstract class DaggerSDKError extends Error {
    /**
     * The name of the dagger error.
     */
    abstract readonly name: ErrorNames;
    /**
     * The dagger specific error code.
     * Use this to identify dagger errors programmatically.
     */
    abstract readonly code: ErrorCodes;
    /**
     * The original error, which caused the DaggerSDKError.
     */
    cause?: Error;
    protected constructor(message: string, options?: DaggerSDKErrorOptions);
    /**
     * @hidden
     */
    get [Symbol.toStringTag](): "GraphQLRequestError" | "UnknownDaggerError" | "TooManyNestedObjectsError" | "EngineSessionConnectParamsParseError" | "EngineSessionConnectionTimeoutError" | "EngineSessionError" | "InitEngineSessionBinaryError" | "DockerImageRefValidationError" | "NotAwaitedRequestError" | "ExecError" | "IntrospectionError";
    /**
     * Pretty prints the error
     */
    printStackTrace(): void;
}

/**
 *  This error is thrown if the dagger SDK does not identify the error and just wraps the cause.
 */
declare class UnknownDaggerError extends DaggerSDKError {
    name: "UnknownDaggerError";
    code: "D101";
    /**
     * @hidden
     */
    constructor(message: string, options: DaggerSDKErrorOptions);
}

interface DockerImageRefValidationErrorOptions extends DaggerSDKErrorOptions {
    ref: string;
}
/**
 *  This error is thrown if the passed image reference does not pass validation and is not compliant with the
 *  DockerImage constructor.
 */
declare class DockerImageRefValidationError extends DaggerSDKError {
    name: "DockerImageRefValidationError";
    code: "D107";
    /**
     *  The docker image reference, which caused the error.
     */
    ref: string;
    /**
     *  @hidden
     */
    constructor(message: string, options: DockerImageRefValidationErrorOptions);
}

interface EngineSessionConnectParamsParseErrorOptions extends DaggerSDKErrorOptions {
    parsedLine: string;
}
/**
 * This error is thrown if the EngineSession does not manage to parse the required connection parameters from the session binary
 */
declare class EngineSessionConnectParamsParseError extends DaggerSDKError {
    name: "EngineSessionConnectParamsParseError";
    code: "D103";
    /**
     *  the line, which caused the error during parsing, if the error was caused because of parsing.
     */
    parsedLine: string;
    /**
     * @hidden
     */
    constructor(message: string, options: EngineSessionConnectParamsParseErrorOptions);
}

interface ExecErrorOptions extends DaggerSDKErrorOptions {
    cmd: string[];
    exitCode: number;
    stdout: string;
    stderr: string;
    extensions?: GraphQLErrorExtensions;
}
/**
 *  API error from an exec operation in a pipeline.
 */
declare class ExecError extends DaggerSDKError {
    name: "ExecError";
    code: "D109";
    /**
     *  The command that caused the error.
     */
    cmd: string[];
    /**
     *  The exit code of the command.
     */
    exitCode: number;
    /**
     * The stdout of the command.
     */
    stdout: string;
    /**
     * The stderr of the command.
     */
    stderr: string;
    /**
     * GraphQL error extensions
     */
    extensions?: GraphQLErrorExtensions;
    /**
     *  @hidden
     */
    constructor(message: string, options: ExecErrorOptions);
}

interface GraphQLRequestErrorOptions extends DaggerSDKErrorOptions {
    error: ClientError;
}
/**
 *  This error originates from the dagger engine. It means that some error was thrown and sent back via GraphQL.
 */
declare class GraphQLRequestError extends DaggerSDKError {
    name: "GraphQLRequestError";
    code: "D100";
    /**
     *  The query and variables, which caused the error.
     */
    requestContext: ClientError["request"];
    /**
     *  the GraphQL response containing the error.
     */
    response: ClientError["response"];
    /**
     *  The GraphQL error extentions.
     */
    extensions?: GraphQLErrorExtensions;
    /**
     *  @hidden
     */
    constructor(message: string, options: GraphQLRequestErrorOptions);
}

/**
 *  This error is thrown if the dagger binary cannot be copied from the dagger docker image and copied to the local host.
 */
declare class InitEngineSessionBinaryError extends DaggerSDKError {
    name: "InitEngineSessionBinaryError";
    code: "D106";
    /**
     *  @hidden
     */
    constructor(message: string, options?: DaggerSDKErrorOptions);
}

interface TooManyNestedObjectsErrorOptions extends DaggerSDKErrorOptions {
    response: unknown;
}
/**
 *  Dagger only expects one response value from the engine. If the engine returns more than one value this error is thrown.
 */
declare class TooManyNestedObjectsError extends DaggerSDKError {
    name: "TooManyNestedObjectsError";
    code: "D102";
    /**
     *  the response containing more than one value.
     */
    response: unknown;
    /**
     * @hidden
     */
    constructor(message: string, options: TooManyNestedObjectsErrorOptions);
}

type EngineSessionErrorOptions = DaggerSDKErrorOptions;
/**
 * This error is thrown if the EngineSession does not manage to parse the required port successfully because a EOF is read before any valid port.
 * This usually happens if no connection can be established.
 */
declare class EngineSessionError extends DaggerSDKError {
    name: "EngineSessionError";
    code: "D105";
    /**
     * @hidden
     */
    constructor(message: string, options?: EngineSessionErrorOptions);
}

interface EngineSessionConnectionTimeoutErrorOptions extends DaggerSDKErrorOptions {
    timeOutDuration: number;
}
/**
 * This error is thrown if the EngineSession does not manage to parse the required port successfully because the sessions connection timed out.
 */
declare class EngineSessionConnectionTimeoutError extends DaggerSDKError {
    name: "EngineSessionConnectionTimeoutError";
    code: "D104";
    /**
     * The duration until the timeout occurred in ms.
     */
    timeOutDuration: number;
    /**
     * @hidden
     */
    constructor(message: string, options: EngineSessionConnectionTimeoutErrorOptions);
}

/**
 * This error is thrown when the compute function isn't awaited.
 */
declare class NotAwaitedRequestError extends DaggerSDKError {
    name: "NotAwaitedRequestError";
    code: "D108";
    /**
     * @hidden
     */
    constructor(message: string, options?: DaggerSDKErrorOptions);
}

declare class FunctionNotFound extends DaggerSDKError {
    name: "ExecError";
    code: "D109";
    constructor(message: string, options?: DaggerSDKErrorOptions);
}

declare class IntrospectionError extends DaggerSDKError {
    name: "IntrospectionError";
    code: "D110";
    constructor(message: string, options?: DaggerSDKErrorOptions);
}

/**
 * Wraps the GraphQL client to allow lazy initialization and setting
 * the GQL client of the global Dagger client instance (`dag`).
 */
declare class Connection {
    private _gqlClient?;
    constructor(_gqlClient?: GraphQLClient | undefined);
    private _served;
    resetClient(): void;
    setGQLClient(gqlClient: GraphQLClient): void;
    getGQLClient(): GraphQLClient;
    /**
     * Run `serve` the first time `key` is seen in this session and remember the
     * result, so a generated client can ensure its module is served before its
     * first query without serving it again on every call.
     */
    ensureServed(key: string, serve: () => Promise<void>): Promise<void>;
}
declare const globalConnection: Connection;

type QueryTree = {
    operation: string;
    args?: Record<string, unknown>;
    inlineType?: string;
};
type Metadata = {
    [key: string]: {
        is_enum?: boolean;
        value_to_name?: (value: any) => string;
    };
};
/**
 * Convert querytree into a Graphql query then compute it
 * @param q | QueryTree[]
 * @param client | GraphQLClient
 * @returns
 */
declare function computeQuery<T>(q: QueryTree[], client: GraphQLClient): Promise<T>;

/**
 * A module a generated client serves into the session before its first query.
 * `key` memoizes the serve per session (the module's ref or path); `run`
 * performs it, through a context that carries no serve of its own so it cannot
 * recurse.
 */
type ServeSpec = {
    key: string;
    run: () => Promise<void>;
};
declare class Context {
    private _queryTree;
    private _connection;
    private _serve?;
    constructor(_queryTree?: QueryTree[], _connection?: Connection, _serve?: ServeSpec | undefined);
    getGQLClient(): GraphQLClient;
    copy(): Context;
    select(operation: string, args?: Record<string, unknown>): Context;
    /**
     * Select via node(id:) with an inline fragment on the given type.
     * Produces: node(id: "...") { ... on TypeName { children } }
     */
    selectNode(id: string, typeName: string): Context;
    /**
     * Return a copy of this context that serves `spec`'s module before the first
     * query on it (or on any context derived from it) runs. Used by a generated
     * module client to bind its own module to its `dag`.
     */
    withServe(spec: ServeSpec): Context;
    execute<T>(): Promise<T>;
}
/**
 * Common base class for every generated API class (Client, Container, and
 * dependency-contributed types).
 *
 * It lives here in the SDK runtime rather than in the generated client.gen.ts
 * so that per-dependency generated files (e.g. hello.gen.ts) can `extends
 * BaseClient` without importing a value from client.gen.ts — client.gen.ts
 * `export *`s those dep files, so a value import would create an ESM cycle.
 * client.gen.ts re-exports BaseClient to keep `import { BaseClient } from
 * "./client.gen.js"` working for existing consumers.
 */
declare class BaseClient {
    protected _ctx: Context;
    /**
     * @hidden
     */
    constructor(_ctx?: Context);
}

/**
 * ConnectOpts defines option used to connect to an engine.
 */
interface ConnectOpts {
    /**
     * Use to overwrite Dagger workdir
     * @defaultValue process.cwd()
     */
    Workdir?: string;
    /**
     * Opt into loading workspace modules for this connection.
     * By default, only the core API is exposed.
     */
    LoadWorkspaceModules?: boolean;
    /**
       * Enable logs output
       * @example
       * LogOutput
       * ```ts
       * connect(async (client: Client) => {
      const source = await client.host().workdir().id()
      ...
      }, {LogOutput: process.stdout})
       ```
       */
    LogOutput?: Writable;
}

/**
 * withSession establishes a Dagger session, points the global client at it for
 * the duration of `fct`, and tears it down again.
 *
 * This is the session layer's whole job: no tracing, no bindings. `connection`
 * is this plus the tracer's lifetime, and it lives above both.
 */
declare function withSession(fct: () => Promise<void>, cfg?: ConnectOpts): Promise<void>;

/**
 * Execute the callback with a GraphQL client connected to the Dagger engine.
 * It automatically provisions the engine if needed.
 */
declare function withGQLClient<T>(connectOpts: ConnectOpts, cb: (gqlClient: GraphQLClient) => Promise<T>): Promise<T>;

/**
 * Hold a process-wide singleton on `globalThis` rather than in module scope.
 *
 * Module scope is not process scope. Once the SDK is several packages, a
 * resolver is free to install two copies of the session package — conflicting
 * ranges, a failed hoist, an `npm link` — and each copy evaluates its own
 * module-level `new`. The symptom is silent and points nowhere near
 * node_modules: decorators register into one registry while the dispatcher
 * reads another, or `connect()` populates a connection `dag` cannot see.
 *
 * `Symbol.for` resolves through the cross-realm symbol registry, so duplicate
 * copies of this very file still agree on the slot and share one instance.
 *
 * The first copy to evaluate wins, which means the shape stored here is a
 * compatibility surface between versions that may run side by side: only ever
 * add to it.
 */
declare function shared<T>(key: string, create: () => T): T;

/**
 * Declare a number as float in the Dagger API.
 */
type float = number;

/**
 * How acronyms and terms are written where a word starts with a capital.
 */
declare enum AcronymStyle {
    /**
     * HttpClient, Ipv6Address, GitHubRepo
     */
    Capitalized = "CAPITALIZED",
    /**
     * HTTPClient, IPv6Address, GitHubRepo
     */
    Uppercase = "UPPERCASE"
}
/**
 * Utility function to convert a AcronymStyle value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function AcronymStyleValueToName(value: AcronymStyle): string;
/**
 * Utility function to convert a AcronymStyle name to its value so
 * it can be properly used inside the module runtime.
 */
declare function AcronymStyleNameToValue(name: string): AcronymStyle;
type AddressContainerOpts = {
    /**
     * Resolve the address's image tag live, ignoring the workspace lockfile: neither read a pinned value nor record one.
     *
     * A DAG address is unaffected: its module evaluates as usual.
     */
    noLock?: boolean;
};
type AddressDirectoryOpts = {
    exclude?: string[];
    include?: string[];
    gitignore?: boolean;
    noCache?: boolean;
    /**
     * Resolve the address's git ref live, ignoring the workspace lockfile: neither read a pinned value nor record one.
     *
     * A DAG address is unaffected: its module evaluates as usual.
     */
    noLock?: boolean;
};
type AddressFileOpts = {
    exclude?: string[];
    include?: string[];
    gitignore?: boolean;
    noCache?: boolean;
    /**
     * Resolve the address's git ref live, ignoring the workspace lockfile: neither read a pinned value nor record one.
     *
     * A DAG address is unaffected: its module evaluates as usual.
     */
    noLock?: boolean;
};
type AddressGitRefOpts = {
    /**
     * Resolve the address's git ref live, ignoring the workspace lockfile: neither read a pinned value nor record one.
     *
     * A DAG address is unaffected: its module evaluates as usual.
     */
    noLock?: boolean;
};
type AgentNotifyOpts = {
    /**
     * The lifecycle states that fire an event. IDLE events carry the turn's final reply; FAILED events carry the loop error.
     */
    on?: AgentState[];
};
type AgentPauseOpts = {
    /**
     * Preempt the in-flight step instead of letting it finish. All completed steps are kept and the interrupted turn stays open: messages it consumed remain pending, while unconsumed mailbox messages are discarded. Resume continues the turn from the last committed step. On an idle, never-started, or failed agent there is nothing to preempt, so this is a plain pause.
     */
    interrupt?: boolean;
};
type AgentSendOpts = {
    /**
     * The ref of a message in the SENDER's own mailbox this send answers (e.g. "#3", from its attribution header). The recipient sees the two paired, and awaiters of the replied-to message resolve with this reply immediately instead of at the sender's turn end.
     */
    replyTo?: string;
    /**
     * Ordered TEXT, IMAGE, AUDIO, or DOCUMENT user content blocks. File inputs are resolved and all content is validated before enqueueing. Pass an empty message for media-only sends.
     */
    content?: LLMContentBlockInput[];
};
type AgentStopOpts = {
    /**
     * Cancel the loop immediately instead of letting an in-flight step finish. Either way the completed steps are preserved in the snapshot.
     */
    kill?: boolean;
};
/**
 * EXPERIMENTAL: Agent APIs are likely to change.
 *
 * How a message landed in an agent's evaluation.
 */
declare enum AgentMessageDelivery {
    /**
     * The message is queued: the agent is paused or failed, and a resume will drain it.
     */
    Queued = "QUEUED",
    /**
     * The message opened a new turn: the agent was idle or newly started.
     */
    Started = "STARTED",
    /**
     * The message was absorbed into the in-flight turn at a step boundary, steering it.
     */
    Steered = "STEERED"
}
/**
 * Utility function to convert a AgentMessageDelivery value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function AgentMessageDeliveryValueToName(value: AgentMessageDelivery): string;
/**
 * Utility function to convert a AgentMessageDelivery name to its value so
 * it can be properly used inside the module runtime.
 */
declare function AgentMessageDeliveryNameToValue(name: string): AgentMessageDelivery;
/**
 * EXPERIMENTAL: Agent APIs are likely to change.
 *
 * Computed lifecycle state of an agent.
 */
declare enum AgentState {
    /**
     * The loop failed; snapshot holds the completed prefix. Resume retries.
     */
    Failed = "FAILED",
    /**
     * Mailbox empty, turn complete; blocked in receive.
     */
    Idle = "IDLE",
    /**
     * Mailbox accepting but not draining, until resume.
     */
    Paused = "PAUSED",
    /**
     * A model request or tool evaluation is in flight.
     */
    Running = "RUNNING",
    /**
     * Runtime released; snapshot remains readable.
     */
    Stopped = "STOPPED",
    /**
     * Blocked on input from the user.
     */
    WaitingInput = "WAITING_INPUT"
}
/**
 * Utility function to convert a AgentState value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function AgentStateValueToName(value: AgentState): string;
/**
 * Utility function to convert a AgentState name to its value so
 * it can be properly used inside the module runtime.
 */
declare function AgentStateNameToValue(name: string): AgentState;
type ArtifactUriOpts = {
    /**
     * Prefix the workspace's Git address and commit: dag://<workspace>@<commit>:<path>. Fails if the artifact has no workspace, or its workspace has no Git address.
     */
    absolute?: boolean;
    /**
     * Include the dimension keys as a query. Without them, the address is a path selector.
     */
    dimensionKeys?: boolean;
    /**
     * Include the artifact type in the scheme: dag+container://.
     */
    typeAssertion?: boolean;
};
type ArtifactValueOpts = {
    /**
     * Field arguments as a JSON object.
     */
    arguments: JSON;
};
declare enum ArtifactDimensionKind {
    Collection = "COLLECTION",
    Module = "MODULE",
    Type = "TYPE"
}
/**
 * Utility function to convert a ArtifactDimensionKind value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function ArtifactDimensionKindValueToName(value: ArtifactDimensionKind): string;
/**
 * Utility function to convert a ArtifactDimensionKind name to its value so
 * it can be properly used inside the module runtime.
 */
declare function ArtifactDimensionKindNameToValue(name: string): ArtifactDimensionKind;
type ArtifactsFilterDirectivesOpts = {
    /**
     * Remove the matching artifacts instead.
     */
    exclude?: boolean;
};
type ArtifactsFilterParentDirectivesOpts = {
    /**
     * Remove the matching artifacts instead.
     */
    exclude?: boolean;
};
type ArtifactsFilterParentTypesOpts = {
    /**
     * Remove the matching artifacts instead.
     */
    exclude?: boolean;
};
type ArtifactsFilterTypesOpts = {
    /**
     * Remove the matching artifacts instead.
     */
    exclude?: boolean;
};
type ArtifactsPathDefinitionsOpts = {
    /**
     * Prefix each address with the workspace's Git address and commit.
     */
    absolute?: boolean;
    /**
     * Include the artifact type in each address scheme.
     */
    typeAssertion?: boolean;
    /**
     * Project paths to the items of this collection dimension. Preserve parent dimensions and remove descendant dimensions.
     */
    dimension?: string;
};
type BuildArg = {
    /**
     * The build argument name.
     */
    name: string;
    /**
     * The build argument value.
     */
    value: string;
};
/**
 * Arbitrary binary data, represented as a base64-encoded string.
 */
type Bytes = string & {
    __Bytes: never;
};
/**
 * Sharing mode of the cache volume.
 */
declare enum CacheSharingMode {
    /**
     * Shares the cache volume amongst many build pipelines, but will serialize the writes
     */
    Locked = "LOCKED",
    /**
     * Keeps a cache volume for a single build pipeline
     */
    Private = "PRIVATE",
    /**
     * Shares the cache volume amongst many build pipelines
     */
    Shared = "SHARED"
}
/**
 * Utility function to convert a CacheSharingMode value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function CacheSharingModeValueToName(value: CacheSharingMode): string;
/**
 * Utility function to convert a CacheSharingMode name to its value so
 * it can be properly used inside the module runtime.
 */
declare function CacheSharingModeNameToValue(name: string): CacheSharingMode;
/**
 * A convention for joining words into an identifier.
 */
declare enum Casing {
    /**
     * httpClient
     */
    Camel = "CAMEL",
    /**
     * httpclient (output only: drops word boundaries)
     */
    Flat = "FLAT",
    /**
     * http-client
     */
    Kebab = "KEBAB",
    /**
     * HTTPClient
     */
    Pascal = "PASCAL",
    /**
     * HTTP_CLIENT
     */
    ScreamingSnake = "SCREAMING_SNAKE",
    /**
     * http_client
     */
    Snake = "SNAKE"
}
/**
 * Utility function to convert a Casing value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function CasingValueToName(value: Casing): string;
/**
 * Utility function to convert a Casing name to its value so
 * it can be properly used inside the module runtime.
 */
declare function CasingNameToValue(name: string): Casing;
type ChangesetFilterOpts = {
    /**
     * Only include changes at paths matching these patterns. Empty includes all paths.
     */
    include?: string[];
    /**
     * Exclude changes at paths matching these patterns.
     */
    exclude?: string[];
};
type ChangesetWithChangesetOpts = {
    /**
     * What to do on a merge conflict
     */
    onConflict?: ChangesetMergeConflict;
};
type ChangesetWithChangesetsOpts = {
    /**
     * What to do on a merge conflict
     */
    onConflict?: ChangesetsMergeConflict;
};
/**
 * Strategy to use when merging changesets with conflicting changes.
 */
declare enum ChangesetMergeConflict {
    /**
     * Attempt the merge and fail if git merge fails due to conflicts
     */
    Fail = "FAIL",
    /**
     * Fail before attempting merge if file-level conflicts are detected
     */
    FailEarly = "FAIL_EARLY",
    /**
     * Let git create conflict markers in files. For modify/delete conflicts, keeps the modified version. Fails on binary conflicts.
     */
    LeaveConflictMarkers = "LEAVE_CONFLICT_MARKERS",
    /**
     * The conflict is resolved by applying the version of the calling changeset
     */
    PreferOurs = "PREFER_OURS",
    /**
     * The conflict is resolved by applying the version of the other changeset
     */
    PreferTheirs = "PREFER_THEIRS"
}
/**
 * Utility function to convert a ChangesetMergeConflict value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function ChangesetMergeConflictValueToName(value: ChangesetMergeConflict): string;
/**
 * Utility function to convert a ChangesetMergeConflict name to its value so
 * it can be properly used inside the module runtime.
 */
declare function ChangesetMergeConflictNameToValue(name: string): ChangesetMergeConflict;
/**
 * Strategy to use when merging multiple changesets with git octopus merge.
 */
declare enum ChangesetsMergeConflict {
    /**
     * Attempt the octopus merge and fail if git merge fails due to conflicts
     */
    Fail = "FAIL",
    /**
     * Fail before attempting merge if file-level conflicts are detected between any changesets
     */
    FailEarly = "FAIL_EARLY"
}
/**
 * Utility function to convert a ChangesetsMergeConflict value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function ChangesetsMergeConflictValueToName(value: ChangesetsMergeConflict): string;
/**
 * Utility function to convert a ChangesetsMergeConflict name to its value so
 * it can be properly used inside the module runtime.
 */
declare function ChangesetsMergeConflictNameToValue(name: string): ChangesetsMergeConflict;
type ContainerAsServiceOpts = {
    /**
     * Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
     *
     * If empty, the container's default command is used.
     */
    args?: string[];
    /**
     * If the container has an entrypoint, prepend it to the args.
     */
    useEntrypoint?: boolean;
    /**
     * Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     */
    disableDaggerInDagger?: boolean;
    /**
     * @deprecated Commands can access Dagger by default. Use "disableDaggerInDagger" to opt out.
     */
    experimentalPrivilegedNesting?: boolean;
    /**
     * Connect Dagger clients started by the command to the current engine as new sessions, instead of as clients of the current session. Each connection gets its own session, released when that client closes.
     *
     * The command reaches the engine through DAGGER_ENGINE, so SDKs run a Dagger CLI: set _EXPERIMENTAL_DAGGER_CLI_BIN to one in the container, or let the SDK download one.
     *
     * Cannot be combined with "disableDaggerInDagger".
     */
    daggerInDaggerNewSession?: boolean;
    /**
     * Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     */
    insecureRootCapabilities?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
    /**
     * If set, skip the automatic init process injected into containers by default.
     *
     * This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
     */
    noInit?: boolean;
};
type ContainerAsTarballOpts = {
    /**
     * Identifiers for other platform specific containers.
     *
     * Used for multi-platform images.
     */
    platformVariants?: Container[];
    /**
     * Force each layer of the image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     */
    forcedCompression?: ImageLayerCompression;
    /**
     * Use the specified media types for the image's layers.
     *
     * Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
     */
    mediaTypes?: ImageMediaTypes;
    /**
     * Clamp the image's timestamps to this time, so that rebuilding unchanged files produces identical layers.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199). File timestamps in every layer and the image's creation times that are later than this are set to it, as with BuildKit's rewrite-timestamp and SOURCE_DATE_EPOCH. Every layer is rewritten, so this costs time on each export.
     */
    sourceDateEpoch?: number;
};
type ContainerDirectoryOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
    /**
     * Repair symlinks that directory selection would break. Resolve targets across the root filesystem and directory or file mounts. Preserve working relative links and links that already have no target. Cache, volume, tmpfs, secret, and socket mounts cause an error if selection, a resolved link, or mount target resolution needs their contents.
     */
    resolveBrokenSymlinks?: boolean;
};
type ContainerExistsOpts = {
    /**
     * If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").
     */
    expectedType?: ExistsType;
    /**
     * If specified, do not follow symlinks.
     */
    doNotFollowSymlinks?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerExportOpts = {
    /**
     * Identifiers for other platform specific containers.
     *
     * Used for multi-platform image.
     */
    platformVariants?: Container[];
    /**
     * Force each layer of the exported image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     */
    forcedCompression?: ImageLayerCompression;
    /**
     * Use the specified media types for the exported image's layers.
     *
     * Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
     */
    mediaTypes?: ImageMediaTypes;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
    /**
     * Clamp the image's timestamps to this time, so that rebuilding unchanged files produces identical layers.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199). File timestamps in every layer and the image's creation times that are later than this are set to it, as with BuildKit's rewrite-timestamp and SOURCE_DATE_EPOCH. Every layer is rewritten, so this costs time on each export.
     */
    sourceDateEpoch?: number;
};
type ContainerExportImageOpts = {
    /**
     * Identifiers for other platform specific containers.
     *
     * Used for multi-platform image.
     */
    platformVariants?: Container[];
    /**
     * Force each layer of the exported image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     */
    forcedCompression?: ImageLayerCompression;
    /**
     * Use the specified media types for the exported image's layers.
     *
     * Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
     */
    mediaTypes?: ImageMediaTypes;
    /**
     * Clamp the image's timestamps to this time, so that rebuilding unchanged files produces identical layers.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199). File timestamps in every layer and the image's creation times that are later than this are set to it, as with BuildKit's rewrite-timestamp and SOURCE_DATE_EPOCH. Every layer is rewritten, so this costs time on each export.
     */
    sourceDateEpoch?: number;
};
type ContainerFileOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    expand?: boolean;
};
type ContainerFromOpts = {
    /**
     * Version query used to select an image tag. The address must not contain a tag or digest.
     */
    version?: string;
    /**
     * Service to use as the registry endpoint for the image address.
     *
     * The service will be started only for this pull.
     */
    registryService?: Service;
    /**
     * Protocol to use for registry communication.
     *
     * Defaults to "HTTPS". Use "HTTP" only for plain HTTP registries.
     */
    protocol?: RegistryProtocol;
    /**
     * Allow HTTPS registry communication without verifying the server certificate.
     */
    insecureSkipTlsVerify?: boolean;
    /**
     * Ignore the workspace lockfile for this lookup.
     */
    noLock?: boolean;
};
type ContainerImportOpts = {
    /**
     * Identifies the tag to import from the archive, if the archive bundles multiple tags.
     */
    tag?: string;
};
type ContainerLayerOpts = {
    /**
     * Force each layer of the image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     */
    forcedCompression?: ImageLayerCompression;
    /**
     * Media types to use for image layers. Defaults to OCI.
     */
    mediaTypes?: ImageMediaTypes;
};
type ContainerManifestOpts = {
    /**
     * Force each layer of the image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     */
    forcedCompression?: ImageLayerCompression;
    /**
     * Media types to use for image layers. Defaults to OCI.
     */
    mediaTypes?: ImageMediaTypes;
};
type ContainerPublishOpts = {
    /**
     * Identifiers for other platform specific containers.
     *
     * Used for multi-platform image.
     */
    platformVariants?: Container[];
    /**
     * Force each layer of the published image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     */
    forcedCompression?: ImageLayerCompression;
    /**
     * Use the specified media types for the published image's layers.
     *
     * Defaults to "OCI", which is compatible with most recent registries, but "Docker" may be needed for older registries without OCI support.
     */
    mediaTypes?: ImageMediaTypes;
    /**
     * Service to use as the registry endpoint for the image address.
     *
     * The service will be started only for this push.
     */
    registryService?: Service;
    /**
     * Protocol to use for registry communication.
     *
     * Defaults to "HTTPS". Use "HTTP" only for plain HTTP registries.
     */
    protocol?: RegistryProtocol;
    /**
     * Allow HTTPS registry communication without verifying the server certificate.
     */
    insecureSkipTlsVerify?: boolean;
    /**
     * Clamp the image's timestamps to this time, so that rebuilding unchanged files produces identical layers.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199). File timestamps in every layer and the image's creation times that are later than this are set to it, as with BuildKit's rewrite-timestamp and SOURCE_DATE_EPOCH. Every layer is rewritten, so this costs time on each export.
     */
    sourceDateEpoch?: number;
};
type ContainerShellOpts = {
    /**
     * Return the batch command instead of the interactive command.
     */
    batch?: boolean;
};
type ContainerStatOpts = {
    /**
     * If specified, do not follow symlinks.
     */
    doNotFollowSymlinks?: boolean;
};
type ContainerTerminalOpts = {
    /**
     * If set, override the container's default terminal command and invoke these command arguments instead.
     */
    cmd?: string[];
    /**
     * Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     */
    disableDaggerInDagger?: boolean;
    /**
     * @deprecated Commands can access Dagger by default. Use "disableDaggerInDagger" to opt out.
     */
    experimentalPrivilegedNesting?: boolean;
    /**
     * Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     */
    insecureRootCapabilities?: boolean;
};
type ContainerUpOpts = {
    /**
     * Bind each tunnel port to a random port on the host.
     */
    random?: boolean;
    /**
     * List of frontend/backend port mappings to forward.
     *
     * Frontend is the port accepting traffic on the host, backend is the service port.
     */
    ports?: PortForward[];
    /**
     * Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
     *
     * If empty, the container's default command is used.
     */
    args?: string[];
    /**
     * If the container has an entrypoint, prepend it to the args.
     */
    useEntrypoint?: boolean;
    /**
     * Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     */
    disableDaggerInDagger?: boolean;
    /**
     * @deprecated Commands can access Dagger by default. Use "disableDaggerInDagger" to opt out.
     */
    experimentalPrivilegedNesting?: boolean;
    /**
     * Connect Dagger clients started by the command to the current engine as new sessions, instead of as clients of the current session. Each connection gets its own session, released when that client closes.
     *
     * The command reaches the engine through DAGGER_ENGINE, so SDKs run a Dagger CLI: set _EXPERIMENTAL_DAGGER_CLI_BIN to one in the container, or let the SDK download one.
     *
     * Cannot be combined with "disableDaggerInDagger".
     */
    daggerInDaggerNewSession?: boolean;
    /**
     * Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     */
    insecureRootCapabilities?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
    /**
     * If set, skip the automatic init process injected into containers by default.
     *
     * This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
     */
    noInit?: boolean;
};
type ContainerWithDefaultTerminalCmdOpts = {
    /**
     * Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     */
    disableDaggerInDagger?: boolean;
    /**
     * @deprecated Commands can access Dagger by default. Use "disableDaggerInDagger" to opt out.
     */
    experimentalPrivilegedNesting?: boolean;
    /**
     * Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     */
    insecureRootCapabilities?: boolean;
};
type ContainerWithDirectoryOpts = {
    /**
     * Patterns to exclude in the written directory (e.g. ["node_modules/**", ".gitignore", ".git/"]).
     */
    exclude?: string[];
    /**
     * Patterns to include in the written directory (e.g. ["*.go", "go.mod", "go.sum"]).
     */
    include?: string[];
    /**
     * Apply .gitignore rules when writing the directory.
     */
    gitignore?: boolean;
    /**
     * A user:group to set for the directory and its contents.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Set the owner to the container's current user.
     */
    inheritOwner?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
    permissions?: number;
};
type ContainerWithDockerHealthcheckOpts = {
    /**
     * When true, command must be a single element, which is run using the container's shell
     */
    shell?: boolean;
    /**
     * Interval between running healthcheck. Example: "30s"
     */
    interval?: string;
    /**
     * Healthcheck timeout. Example: "3s"
     */
    timeout?: string;
    /**
     * StartPeriod allows for failures during this initial startup period which do not count towards maximum number of retries. Example: "0s"
     */
    startPeriod?: string;
    /**
     * StartInterval configures the duration between checks during the startup phase. Example: "5s"
     */
    startInterval?: string;
    /**
     * The maximum number of consecutive failures before the container is marked as unhealthy. Example: "3"
     */
    retries?: number;
};
type ContainerWithEntrypointOpts = {
    /**
     * Don't reset the default arguments when setting the entrypoint. By default it is reset, since entrypoint and default args are often tightly coupled.
     */
    keepDefaultArgs?: boolean;
};
type ContainerWithEnvVariableOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value according to the current environment variables defined in the container (e.g. "/opt/bin:$PATH").
     */
    expand?: boolean;
};
type ContainerWithExecOpts = {
    /**
     * Apply the OCI entrypoint, if present, by prepending it to the args. Ignored by default.
     */
    useEntrypoint?: boolean;
    /**
     * Content to write to the command's standard input. Example: "Hello world")
     */
    stdin?: string;
    /**
     * Redirect the command's standard input from a file in the container. Example: "./stdin.txt"
     */
    redirectStdin?: string;
    /**
     * Redirect the command's standard output to a file in the container. The redirected output is not logged. Example: "./stdout.txt"
     */
    redirectStdout?: string;
    /**
     * Redirect the command's standard error to a file in the container. The redirected output is not logged. Example: "./stderr.txt"
     */
    redirectStderr?: string;
    /**
     * Exit codes this command is allowed to exit with without error
     */
    expect?: ReturnType;
    /**
     * Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     */
    disableDaggerInDagger?: boolean;
    /**
     * @deprecated Commands can access Dagger by default. Use "disableDaggerInDagger" to opt out.
     */
    experimentalPrivilegedNesting?: boolean;
    /**
     * Connect Dagger clients started by the command to the current engine as new sessions, instead of as clients of the current session. Each connection gets its own session, released when that client closes.
     *
     * The command reaches the engine through DAGGER_ENGINE, so SDKs run a Dagger CLI: set _EXPERIMENTAL_DAGGER_CLI_BIN to one in the container, or let the SDK download one.
     *
     * Cannot be combined with "disableDaggerInDagger".
     */
    daggerInDaggerNewSession?: boolean;
    /**
     * Execute the command with all root capabilities. Like --privileged in Docker
     *
     * DANGER: this grants the command full access to the host system. Only use when 1) you trust the command being executed and 2) you specifically need this level of access.
     */
    insecureRootCapabilities?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
    /**
     * Skip the automatic init process injected into containers by default.
     *
     * Only use this if you specifically need the command to be pid 1 in the container. Otherwise it may result in unexpected behavior. If you're not sure, you don't need this.
     */
    noInit?: boolean;
    /**
     * Let the command, and the processes it starts, use the Linux kernel's transparent huge pages. Large-memory programs such as compilers can run faster with them, at some cost in memory.
     *
     * Without this, a command gets huge pages only for memory its own program asks huge pages for, as on a host set to "madvise". That only matters on hosts with Linux 6.18 or later whose transparent huge pages are set to "always" (/sys/kernel/mm/transparent_hugepage/enabled, or a size under /sys/kernel/mm/transparent_hugepage/hugepages-*). With the usual default settings, this changes nothing. On older kernels, commands get what the host's settings give them, whether they set this or not.
     *
     * It isn't part of the exec's cache key: an exec that differs from a cached one only in this setting reuses the cached result, whichever setting produced it.
     */
    transparentHugePages?: boolean;
};
type ContainerWithExposedPortOpts = {
    /**
     * Network protocol. Example: "tcp"
     */
    protocol?: NetworkProtocol;
    /**
     * Port description. Example: "payment API endpoint"
     */
    description?: string;
    /**
     * Skip the health check when run as a service.
     */
    experimentalSkipHealthcheck?: boolean;
};
type ContainerWithFileOpts = {
    /**
     * Permissions of the new file. Example: 0600
     */
    permissions?: number;
    /**
     * A user:group to set for the file.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Set the owner to the container's current user.
     */
    inheritOwner?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    expand?: boolean;
};
type ContainerWithFilesOpts = {
    /**
     * Permission given to the copied files (e.g., 0600).
     */
    permissions?: number;
    /**
     * A user:group to set for the files.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Set the owner to the container's current user.
     */
    inheritOwner?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    expand?: boolean;
};
type ContainerWithMountedCacheOpts = {
    /**
     * Identifier of the directory to use as the cache volume's root.
     */
    source?: Directory;
    /**
     * Sharing mode of the cache volume.
     */
    sharing?: CacheSharingMode;
    /**
     * A user:group to set for the mounted cache directory.
     *
     * Note that this changes the ownership of the specified mount along with the initial filesystem provided by source (if any). It does not have any effect if/when the cache has already been created.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Set the owner to the container's current user.
     */
    inheritOwner?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerWithMountedDirectoryOpts = {
    /**
     * A user:group to set for the mounted directory and its contents.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Set the owner to the container's current user.
     */
    inheritOwner?: boolean;
    /**
     * Mount the directory read-only.
     */
    readOnly?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerWithMountedFileOpts = {
    /**
     * A user or user:group to set for the mounted file.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Set the owner to the container's current user.
     */
    inheritOwner?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    expand?: boolean;
    /**
     * Mount the file read-only.
     */
    readOnly?: boolean;
};
type ContainerWithMountedFilesOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/lib").
     */
    expand?: boolean;
};
type ContainerWithMountedSecretOpts = {
    /**
     * A user:group to set for the mounted secret.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Set the owner to the container's current user.
     */
    inheritOwner?: boolean;
    /**
     * Permission given to the mounted secret (e.g., 0600).
     *
     * This option requires an owner to be set to be active.
     */
    mode?: number;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerWithMountedTempOpts = {
    /**
     * Size of the temporary directory in bytes.
     */
    size?: number;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerWithMountedVolumeOpts = {
    /**
     * Mount the volume read-only.
     */
    readOnly?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerWithNewFileOpts = {
    /**
     * Permissions of the new file. Example: 0600
     */
    permissions?: number;
    /**
     * A user:group to set for the file.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Set the owner to the container's current user.
     */
    inheritOwner?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    expand?: boolean;
};
type ContainerWithRunOpts = {
    /**
     * Override the batch shell arguments. Example: ["bash", "-c"].
     */
    shell?: string[];
    /**
     * Override whether the shell is denied Dagger API access. Omit to use the configured shell setting.
     */
    disableDaggerInDagger?: boolean;
    /**
     * @deprecated Commands can access Dagger by default. Use "disableDaggerInDagger" to opt out.
     */
    experimentalPrivilegedNesting?: boolean;
    /**
     * Override whether the shell has all root capabilities.
     */
    insecureRootCapabilities?: boolean;
};
type ContainerWithShellOpts = {
    /**
     * Command arguments for batch use. The script is appended as one argument. Defaults to interactive followed by "-c".
     */
    batch?: string[];
    /**
     * Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     */
    disableDaggerInDagger?: boolean;
    /**
     * @deprecated Commands can access Dagger by default. Use "disableDaggerInDagger" to opt out.
     */
    experimentalPrivilegedNesting?: boolean;
    /**
     * Give the shell all root capabilities. Use only with trusted commands.
     */
    insecureRootCapabilities?: boolean;
};
type ContainerWithSymlinkOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    expand?: boolean;
};
type ContainerWithUnixSocketOpts = {
    /**
     * A user:group to set for the mounted socket.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Set the owner to the container's current user.
     */
    inheritOwner?: boolean;
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerWithWorkdirOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerWithoutDirectoryOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerWithoutEntrypointOpts = {
    /**
     * Don't remove the default arguments when unsetting the entrypoint.
     */
    keepDefaultArgs?: boolean;
};
type ContainerWithoutExposedPortOpts = {
    /**
     * Port protocol to unexpose
     */
    protocol?: NetworkProtocol;
};
type ContainerWithoutFileOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    expand?: boolean;
};
type ContainerWithoutFilesOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of paths according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    expand?: boolean;
};
type ContainerWithoutMountOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type ContainerWithoutUnixSocketOpts = {
    /**
     * Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    expand?: boolean;
};
type CurrentModuleWorkdirOpts = {
    /**
     * Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
     */
    exclude?: string[];
    /**
     * Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
     */
    include?: string[];
    /**
     * Apply .gitignore filter rules inside the directory
     */
    gitignore?: boolean;
};
/**
 * The type of change for a diff stat entry.
 */
declare enum DiffStatKind {
    /**
     * A file or directory was added.
     */
    Added = "ADDED",
    /**
     * A file was modified.
     */
    Modified = "MODIFIED",
    /**
     * A file or directory was removed.
     */
    Removed = "REMOVED",
    /**
     * A file was renamed.
     */
    Renamed = "RENAMED"
}
/**
 * Utility function to convert a DiffStatKind value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function DiffStatKindValueToName(value: DiffStatKind): string;
/**
 * Utility function to convert a DiffStatKind name to its value so
 * it can be properly used inside the module runtime.
 */
declare function DiffStatKindNameToValue(name: string): DiffStatKind;
type DirectoryAsModuleOpts = {
    /**
     * An optional subpath of the directory which contains the module's configuration file.
     *
     * If not set, the module source code is loaded from the root of the directory.
     */
    sourceRootPath?: string;
};
type DirectoryAsModuleSourceOpts = {
    /**
     * An optional subpath of the directory which contains the module's configuration file.
     *
     * If not set, the module source code is loaded from the root of the directory.
     */
    sourceRootPath?: string;
};
type DirectoryAsWorkspaceOpts = {
    /**
     * Current working directory inside the workspace root. Defaults to the workspace root.
     */
    cwd?: string;
};
type DirectoryDirectoryOpts = {
    /**
     * Repair symlinks that directory selection would break. Copy targets from this directory into the result. Preserve working relative links and links that already have no target.
     */
    resolveBrokenSymlinks?: boolean;
};
type DirectoryDockerBuildOpts = {
    /**
     * Path to the Dockerfile to use (e.g., "frontend.Dockerfile").
     */
    dockerfile?: string;
    /**
     * The platform to build.
     */
    platform?: Platform;
    /**
     * Build arguments to use in the build.
     */
    buildArgs?: BuildArg[];
    /**
     * Target build stage to build.
     */
    target?: string;
    /**
     * Secrets to pass to the build.
     *
     * They will be mounted at /run/secrets/[secret-name].
     */
    secrets?: Secret[];
    /**
     * If set, skip the automatic init process injected into containers created by RUN statements.
     *
     * This should only be used if the user requires that their exec processes be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
     */
    noInit?: boolean;
    /**
     * A socket to use for SSH authentication during the build
     *
     * (e.g., for Dockerfile RUN --mount=type=ssh instructions).
     *
     * Typically obtained via host.unixSocket() pointing to the SSH_AUTH_SOCK.
     */
    ssh?: Socket;
};
type DirectoryEntriesOpts = {
    /**
     * Location of the directory to look at (e.g., "/src").
     */
    path?: string;
};
type DirectoryExistsOpts = {
    /**
     * If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").
     */
    expectedType?: ExistsType;
    /**
     * If specified, do not follow symlinks.
     */
    doNotFollowSymlinks?: boolean;
};
type DirectoryExportOpts = {
    /**
     * If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.
     */
    wipe?: boolean;
};
type DirectoryFilterOpts = {
    /**
     * If set, paths matching one of these glob patterns is excluded from the new snapshot. Example: ["node_modules/", ".git*", ".env"]
     */
    exclude?: string[];
    /**
     * If set, only paths matching one of these glob patterns is included in the new snapshot. Example: (e.g., ["app/", "package.*"]).
     */
    include?: string[];
    /**
     * If set, apply .gitignore rules when filtering the directory.
     */
    gitignore?: boolean;
};
type DirectorySearchOpts = {
    /**
     * Directory or file paths to search
     */
    paths?: string[];
    /**
     * Glob patterns to match (e.g., "*.md")
     */
    globs?: string[];
    /**
     * The text to match.
     */
    pattern: string;
    /**
     * Interpret the pattern as a literal string instead of a regular expression.
     */
    literal?: boolean;
    /**
     * Enable searching across multiple lines.
     */
    multiline?: boolean;
    /**
     * Allow the . pattern to match newlines in multiline mode.
     */
    dotall?: boolean;
    /**
     * Enable case-insensitive matching.
     */
    insensitive?: boolean;
    /**
     * Honor .gitignore, .ignore, and .rgignore files.
     */
    skipIgnored?: boolean;
    /**
     * Skip hidden files (files starting with .).
     */
    skipHidden?: boolean;
    /**
     * Only return matching files, not lines and content
     */
    filesOnly?: boolean;
    /**
     * Limit the number of results to return
     */
    limit?: number;
};
type DirectoryStatOpts = {
    /**
     * If specified, do not follow symlinks.
     */
    doNotFollowSymlinks?: boolean;
};
type DirectoryTerminalOpts = {
    /**
     * If set, override the default container used for the terminal.
     */
    container?: Container;
    /**
     * If set, override the container's default terminal command and invoke these command arguments instead.
     */
    cmd?: string[];
    /**
     * Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     */
    disableDaggerInDagger?: boolean;
    /**
     * @deprecated Commands can access Dagger by default. Use "disableDaggerInDagger" to opt out.
     */
    experimentalPrivilegedNesting?: boolean;
    /**
     * Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     */
    insecureRootCapabilities?: boolean;
};
type DirectoryWithDirectoryOpts = {
    /**
     * Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
     */
    exclude?: string[];
    /**
     * Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
     */
    include?: string[];
    /**
     * Apply .gitignore filter rules inside the directory
     */
    gitignore?: boolean;
    /**
     * A user:group to set for the copied directory and its contents.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
    /**
     * Permission given to the copied directory and contents (e.g., 0755).
     */
    permissions?: number;
};
type DirectoryWithFileOpts = {
    /**
     * Permission given to the copied file (e.g., 0600).
     */
    permissions?: number;
    /**
     * A user:group to set for the copied directory and its contents.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
};
type DirectoryWithFilesOpts = {
    /**
     * Permission given to the copied files (e.g., 0600).
     */
    permissions?: number;
};
type DirectoryWithNewDirectoryOpts = {
    /**
     * Permission granted to the created directory (e.g., 0777).
     */
    permissions?: number;
};
type DirectoryWithNewFileOpts = {
    /**
     * Permissions of the new file. Example: 0600
     */
    permissions?: number;
};
type DirectoryWithPatchOpts = {
    /**
     * How to handle hunks that no longer apply to the target content: fail (default), or apply what fits and leave git-style conflict markers where it doesn't.
     */
    onConflict?: PatchConflict;
};
type DirectoryWithPatchFileOpts = {
    /**
     * How to handle hunks that no longer apply to the target content: fail (default), or apply what fits and leave git-style conflict markers where it doesn't.
     */
    onConflict?: PatchConflict;
};
type EngineCacheEntrySetOpts = {
    key?: string;
};
type EngineCachePruneOpts = {
    /**
     * Use enabled engine-wide default disk and structural policies. If no default disk policy is enabled, the disk stage falls back to pruning all releasable disk-cache entries. If false, explicit options select stages; with no options, all releasable disk-cache entries are pruned.
     */
    useDefaultPolicy?: boolean;
    /**
     * Override the maximum disk space to keep before pruning (e.g. "200GB" or "80%").
     */
    maxUsedSpace?: string;
    /**
     * Override the minimum disk space to retain during pruning (e.g. "500GB" or "10%").
     */
    reservedSpace?: string;
    /**
     * Override the minimum free disk space target during pruning (e.g. "20GB" or "20%").
     */
    minFreeSpace?: string;
    /**
     * Override the target disk space to keep after pruning (e.g. "200GB" or "50%").
     */
    targetSpace?: string;
    /**
     * Override the maximum structural metadata estimate in absolute bytes. Explicit values must be positive; the configured/default value is used when omitted.
     */
    maxEstimatedBytes?: number;
    /**
     * Override the structural metadata estimate to target in absolute bytes. Explicit values must be positive and lower than the resolved maximum; the configured/default value is used when omitted.
     */
    targetEstimatedBytes?: number;
};
type EnvFileGetOpts = {
    /**
     * Return the value exactly as written to the file. No quote removal or variable expansion
     */
    raw?: boolean;
};
type EnvFileVariablesOpts = {
    /**
     * Return values exactly as written to the file. No quote removal or variable expansion
     */
    raw?: boolean;
};
/**
 * File type.
 */
declare enum ExistsType {
    /**
     * Tests path is a directory
     */
    DirectoryType = "DIRECTORY_TYPE",
    /**
     * Tests path is a regular file
     */
    RegularType = "REGULAR_TYPE",
    /**
     * Tests path is a symlink
     */
    SymlinkType = "SYMLINK_TYPE"
}
/**
 * Utility function to convert a ExistsType value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function ExistsTypeValueToName(value: ExistsType): string;
/**
 * Utility function to convert a ExistsType name to its value so
 * it can be properly used inside the module runtime.
 */
declare function ExistsTypeNameToValue(name: string): ExistsType;
type FileAsEnvFileOpts = {
    /**
     * Replace "${VAR}" or "$VAR" with the value of other vars
     *
     * @deprecated Variable expansion is now enabled by default
     */
    expand?: boolean;
};
type FileContentsOpts = {
    /**
     * Start reading after this line
     */
    offsetLines?: number;
    /**
     * Maximum number of lines to read
     */
    limitLines?: number;
};
type FileDigestOpts = {
    /**
     * If true, exclude metadata from the digest.
     */
    excludeMetadata?: boolean;
};
type FileExportOpts = {
    /**
     * If allowParentDirPath is true, the path argument can be a directory path, in which case the file will be created in that directory.
     */
    allowParentDirPath?: boolean;
};
type FileExtractOpts = {
    /**
     * Remove this many leading elements from each entry's path, as tar --strip-components does.
     *
     * Entries with no more elements than this are skipped.
     */
    stripComponents?: number;
    /**
     * If set, only entries matching one of these glob patterns, or in a directory that does, are extracted (e.g., ["src/", "*.go"]).
     *
     * Patterns match entry paths after stripComponents is applied.
     */
    include?: string[];
    /**
     * If set, entries matching one of these glob patterns, or in a directory that does, are not extracted (e.g., ["**\/testdata", "**\/*_test.go"]).
     *
     * Patterns match entry paths after stripComponents is applied.
     */
    exclude?: string[];
};
type FileSearchOpts = {
    /**
     * Interpret the pattern as a literal string instead of a regular expression.
     */
    literal?: boolean;
    /**
     * Enable searching across multiple lines.
     */
    multiline?: boolean;
    /**
     * Allow the . pattern to match newlines in multiline mode.
     */
    dotall?: boolean;
    /**
     * Enable case-insensitive matching.
     */
    insensitive?: boolean;
    /**
     * Honor .gitignore, .ignore, and .rgignore files.
     */
    skipIgnored?: boolean;
    /**
     * Skip hidden files (files starting with .).
     */
    skipHidden?: boolean;
    /**
     * Only return matching files, not lines and content
     */
    filesOnly?: boolean;
    /**
     * Limit the number of results to return
     */
    limit?: number;
    paths?: string[];
    globs?: string[];
};
type FileSyncOpts = {
    /**
     * Also key the file by its content, so that what consumes it is cached across any two recipes that produce the same file. This hashes the file unless it already has a content key.
     */
    keyByContent?: boolean;
};
type FileWithReplacedOpts = {
    /**
     * Replace all occurrences of the pattern.
     */
    all?: boolean;
    /**
     * Replace the first match starting from the specified line.
     */
    firstFrom?: number;
};
/**
 * File type.
 */
declare enum FileType {
    /**
     * directory file type
     */
    Directory = "DIRECTORY",
    /**
     * directory file type
     */
    DirectoryType = "DIRECTORY",
    /**
     * regular file type
     */
    Regular = "REGULAR",
    /**
     * regular file type
     */
    RegularType = "REGULAR",
    /**
     * symlink file type
     */
    Symlink = "SYMLINK",
    /**
     * symlink file type
     */
    SymlinkType = "SYMLINK",
    /**
     * unknown file type
     */
    Unknown = "UNKNOWN"
}
/**
 * Utility function to convert a FileType value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function FileTypeValueToName(value: FileType): string;
/**
 * Utility function to convert a FileType name to its value so
 * it can be properly used inside the module runtime.
 */
declare function FileTypeNameToValue(name: string): FileType;
type FunctionWithArgOpts = {
    /**
     * A doc string for the argument, if any
     */
    description?: string;
    /**
     * A default value to use for this argument if not explicitly set by the caller, if any
     */
    defaultValue?: JSON;
    /**
     * If the argument is a Directory or File type, default to load path from context directory, relative to root directory.
     */
    defaultPath?: string;
    /**
     * Patterns to ignore when loading the contextual argument value.
     */
    ignore?: string[];
    /**
     * The source map for the argument definition.
     */
    sourceMap?: SourceMap;
    /**
     * If deprecated, the reason or migration path.
     */
    deprecated?: string;
    defaultAddress?: string;
};
type FunctionWithCachePolicyOpts = {
    /**
     * The TTL for the cache policy, if applicable. Provided as a duration string, e.g. "5m", "1h30s".
     */
    timeToLive?: string;
};
type FunctionWithDeprecatedOpts = {
    /**
     * Reason or migration path describing the deprecation.
     */
    reason?: string;
};
/**
 * The behavior configured for function result caching.
 */
declare enum FunctionCachePolicy {
    Default = "DEFAULT",
    Never = "NEVER",
    PerSession = "PER_SESSION"
}
/**
 * Utility function to convert a FunctionCachePolicy value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function FunctionCachePolicyValueToName(value: FunctionCachePolicy): string;
/**
 * Utility function to convert a FunctionCachePolicy name to its value so
 * it can be properly used inside the module runtime.
 */
declare function FunctionCachePolicyNameToValue(name: string): FunctionCachePolicy;
type GitCommitAncestorReleaseTagOpts = {
    /**
     * Include pre-release tags when choosing the latest tag.
     */
    includePreRelease?: boolean;
};
type GitCommitChangesOpts = {
    /**
     * Use this commit as the comparison base instead of the first parent. The comparison commit may belong to an unrelated history or repository.
     */
    against?: GitCommit;
};
type GitCommitReleaseTagOpts = {
    /**
     * Include pre-release tags when choosing the latest tag.
     */
    includePreRelease?: boolean;
};
type GitCommitTreeOpts = {
    /**
     * Set to true to discard .git directory.
     */
    discardGitDir?: boolean;
    /**
     * The depth of the tree to fetch.
     */
    depth?: number;
    /**
     * Set to true to populate tag refs in the local checkout .git.
     */
    includeTags?: boolean;
};
/**
 * How a Git push updated the remote ref.
 */
declare enum GitPushDisposition {
    /**
     * The remote ref was created.
     */
    Created = "CREATED",
    /**
     * The remote ref was fast-forwarded.
     */
    FastForward = "FAST_FORWARD",
    /**
     * The remote ref was replaced under an explicit lease.
     */
    Forced = "FORCED",
    /**
     * The remote ref already pointed to this commit.
     */
    UpToDate = "UP_TO_DATE"
}
/**
 * Utility function to convert a GitPushDisposition value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function GitPushDispositionValueToName(value: GitPushDisposition): string;
/**
 * Utility function to convert a GitPushDisposition name to its value so
 * it can be properly used inside the module runtime.
 */
declare function GitPushDispositionNameToValue(name: string): GitPushDisposition;
type GitRefAsWorkspaceOpts = {
    /**
     * Current working directory inside the workspace root. Defaults to the workspace root.
     */
    cwd?: string;
};
type GitRefLogOpts = {
    /**
     * Maximum number of commits to return.
     */
    limit?: number;
    /**
     * Only include commits touching these paths, relative to the root of the repository.
     */
    paths?: string[];
    /**
     * Exclude commits reachable from this ref, i.e. only list commits added on top of it.
     */
    base?: GitRef;
};
type GitRefPushOpts = {
    /**
     * Destination remote repository. Defaults to the origin remote's push routing, or the source's repository URL when none is registered. Required when the source has no remote URL.
     */
    to?: GitRepository;
    /**
     * Name of a registered remote to push to (see GitRepository.withRemote). Defaults to origin. The remote's push URLs, or its URL, become the destination; more than one push URL requires an explicit to instead.
     */
    remote?: string;
    /**
     * Destination branch; a refs/ prefix is used verbatim. Defaults to this ref's branch name. Required for detached and non-branch refs.
     */
    branch?: string;
    /**
     * Optional lease: a full lowercase object ID allows replacement only if the remote ref still has that value. Checked even for up-to-date pushes. Empty or omitted uses normal non-force rules, creating the ref if it does not exist.
     */
    expectedRemoteSha?: string;
};
type GitRefTreeOpts = {
    /**
     * Set to true to discard .git directory.
     */
    discardGitDir?: boolean;
    /**
     * The depth of the tree to fetch.
     */
    depth?: number;
    /**
     * Set to true to populate tag refs in the local checkout .git.
     */
    includeTags?: boolean;
};
type GitRefWithCommitOpts = {
    /**
     * Committer name. Defaults to authorName.
     */
    committerName?: string;
    /**
     * Committer email. Defaults to authorEmail.
     */
    committerEmail?: string;
    /**
     * RFC3339 committer date. Defaults to date.
     */
    committerDate?: string;
    /**
     * Allow a commit whose tree matches its parent, including when the supplied edits are already present. Defaults to false.
     */
    allowEmpty?: boolean;
    /**
     * Add a Signed-off-by trailer using the commit author's name and email.
     */
    signoff?: boolean;
};
type GitRepositoryAsWorkspaceOpts = {
    /**
     * Current working directory inside the workspace root. Defaults to the workspace root.
     */
    cwd?: string;
};
type GitRepositoryBranchOpts = {
    /**
     * Ignore the workspace lockfile for this lookup.
     */
    noLock?: boolean;
};
type GitRepositoryBranchesOpts = {
    /**
     * Glob patterns (e.g., "refs/tags/v*").
     */
    patterns?: string[];
};
type GitRepositoryBundleOpts = {
    /**
     * A Git ref whose reachable objects are omitted and recorded as a prerequisite.
     */
    base?: GitRef;
};
type GitRepositoryHeadOpts = {
    /**
     * Ignore the workspace lockfile for this lookup.
     */
    noLock?: boolean;
};
type GitRepositoryLatestOpts = {
    /**
     * Version query used to select the greatest matching release ref.
     */
    version?: string;
    /**
     * Ignore the workspace lockfile for this lookup.
     */
    noLock?: boolean;
};
type GitRepositoryRefOpts = {
    /**
     * Ignore the workspace lockfile for this lookup.
     */
    noLock?: boolean;
};
type GitRepositoryTagOpts = {
    /**
     * Ignore the workspace lockfile for this lookup.
     */
    noLock?: boolean;
};
type GitRepositoryTagsOpts = {
    /**
     * Glob patterns (e.g., "refs/tags/v*").
     */
    patterns?: string[];
};
type GitRepositoryWithBundleOpts = {
    /**
     * An optional remote ref hint for fetching a prerequisite when the remote does not allow fetches by object ID.
     */
    prerequisiteRef?: string;
};
type GitRepositoryWithRemoteOpts = {
    /**
     * Push destination, when pushes go somewhere other than url. Empty uses url.
     */
    pushUrl?: string;
};
type HostDirectoryOpts = {
    /**
     * Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
     */
    exclude?: string[];
    /**
     * Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
     */
    include?: string[];
    /**
     * If true, the directory will always be reloaded from the host.
     */
    noCache?: boolean;
    /**
     * Apply .gitignore filter rules inside the directory
     */
    gitignore?: boolean;
};
type HostFileOpts = {
    /**
     * If true, the file will always be reloaded from the host.
     */
    noCache?: boolean;
};
type HostFindUpOpts = {
    noCache?: boolean;
};
type HostServiceOpts = {
    /**
     * Upstream host to forward traffic to.
     */
    host?: string;
};
type HostTunnelOpts = {
    /**
     * Map each service port to the same port on the host, as if the service were running natively.
     *
     * Note: enabling may result in port conflicts.
     */
    native?: boolean;
    /**
     * Configure explicit port forwarding rules for the tunnel.
     *
     * If a port's frontend is unspecified or 0, a random port will be chosen by the host.
     *
     * If no ports are given, all of the service's ports are forwarded. If native is true, each port maps to the same port on the host. If native is false, each port maps to a random port chosen by the host.
     *
     * If ports are given and native is true, the ports are additive.
     */
    ports?: PortForward[];
};
/**
 * A unique identifier for an object.
 */
type ID = string & {
    __ID: never;
};
type IdentifierFormatOpts = {
    /**
     * How to write acronyms and terms where a word starts with a capital.
     */
    acronyms?: AcronymStyle;
};
/**
 * The kind of a word in an identifier.
 */
declare enum IdentifierWordKind {
    /**
     * An acronym, from the dictionary or a run of capitals.
     */
    Acronym = "ACRONYM",
    /**
     * A dictionary term with a fixed mixed-case spelling (GitHub, IPv6).
     */
    Term = "TERM",
    /**
     * An ordinary word.
     */
    Word = "WORD"
}
/**
 * Utility function to convert a IdentifierWordKind value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function IdentifierWordKindValueToName(value: IdentifierWordKind): string;
/**
 * Utility function to convert a IdentifierWordKind name to its value so
 * it can be properly used inside the module runtime.
 */
declare function IdentifierWordKindNameToValue(name: string): IdentifierWordKind;
/**
 * Compression algorithm to use for image layers.
 */
declare enum ImageLayerCompression {
    Estargz = "ESTARGZ",
    Gzip = "GZIP",
    Uncompressed = "UNCOMPRESSED",
    Zstd = "ZSTD"
}
/**
 * Utility function to convert a ImageLayerCompression value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function ImageLayerCompressionValueToName(value: ImageLayerCompression): string;
/**
 * Utility function to convert a ImageLayerCompression name to its value so
 * it can be properly used inside the module runtime.
 */
declare function ImageLayerCompressionNameToValue(name: string): ImageLayerCompression;
/**
 * Mediatypes to use in published or exported image metadata.
 */
declare enum ImageMediaTypes {
    Docker = "DOCKER",
    Oci = "OCI"
}
/**
 * Utility function to convert a ImageMediaTypes value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function ImageMediaTypesValueToName(value: ImageMediaTypes): string;
/**
 * Utility function to convert a ImageMediaTypes name to its value so
 * it can be properly used inside the module runtime.
 */
declare function ImageMediaTypesNameToValue(name: string): ImageMediaTypes;
/**
 * An arbitrary JSON-encoded value.
 */
type JSON = string & {
    __JSON: never;
};
type JSONValueContentsOpts = {
    /**
     * Pretty-print
     */
    pretty?: boolean;
    /**
     * Optional line prefix
     */
    indent?: string;
};
type LLMArtifactsOpts = {
    /**
     * Only include artifacts matching these path patterns, as with Workspace.artifacts. A path selects that path and its children.
     */
    include?: string[];
};
type LLMLoopOpts = {
    /**
     * Cap the number of steps. The loop fails if the cap is reached before the model ends its turn.
     */
    maxSteps?: number;
    /**
     * Cap the model's output tokens on each step. Defaults to the model's maximum.
     */
    maxTokens?: number;
};
type LLMSpawnOpts = {
    /**
     * Expertise to compose into the new agent's conversation. Agent arguments receive the new agent, which cannot be used until composition completes; if composition fails, the agent is left STOPPED.
     */
    expertise?: Expertise[];
    /**
     * Display label for the agent — telemetry and error messages; carries no identity. Defaults to a short name derived from the conversation.
     */
    name?: string;
    /**
     * The runtime handle to restore the instance under, as published on its loop span as dagger.io/agent.id. Omit to mint a fresh instance.
     */
    handle?: string;
    /**
     * The lifecycle state to create the agent in, as facts on the entry: IDLE is ready to be prompted, PAUSED parks it, FAILED holds an error a resume retries past, STOPPED preserves a dormant snapshot that send or resume can relaunch.
     *
     * RUNNING and WAITING_INPUT are refused: they describe a loop, and a restored loop died with the session that published it — restore such an agent as IDLE, its interrupted turn's input still pending on the conversation.
     */
    state?: AgentState;
    /**
     * Recorded parent handle when restoring an agent. Lineage does not install a notification subscription. Requires a supplied handle. The parent must already be restored in this session and cannot be the agent itself or its descendant.
     */
    parentHandle?: string;
    /**
     * The loop error to create the agent with, for state FAILED. Refused with any other state.
     */
    error?: string;
};
type LLMStepOpts = {
    /**
     * Cap the model's output tokens for this step. Defaults to the model's maximum.
     */
    maxTokens?: number;
};
type LLMWithContentOpts = {
    /**
     * The message's recorded provenance.
     */
    origin?: LLMMessageOriginInput;
};
type LLMWithContentFileOpts = {
    /**
     * The media MIME type; inferred from the file's contents when omitted.
     */
    mimeType?: string;
};
type LLMWithModelOpts = {
    /**
     * The provider serving the model, e.g. "openai". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.
     */
    provider?: string;
};
type LLMWithPromptOpts = {
    /**
     * The message's recorded provenance, when it arrived through an agent mailbox rather than from the user. Rendered to the model as an attribution header at request-build time.
     */
    origin?: LLMMessageOriginInput;
};
type LLMWithResponseOpts = {
    /**
     * Uncached input tokens sent
     */
    inputTokens?: number;
    /**
     * Tokens received from the model, including text and tool calls
     */
    outputTokens?: number;
    /**
     * Cached input tokens read
     */
    cachedTokenReads?: number;
    /**
     * Cached input tokens written
     */
    cachedTokenWrites?: number;
    /**
     * Total tokens consumed by this response
     */
    totalTokens?: number;
};
type LLMWithToolResultOpts = {
    /**
     * Ordered text and media returned by the tool
     */
    blocks?: LLMContentBlockInput[];
};
type LLMWithToolsOpts = {
    /**
     * Method names to exclude from the toolset (e.g. constructors, entrypoints).
     */
    except?: string[];
    /**
     * Version of this binding's state contract. Recomposition preserves compatible state when the version is unchanged and resets to the newly bound object's defaults when it differs. Change this when the state layout changes incompatibly. Same-type tool returns retain the version. Module identity and ownership checks still apply.
     */
    version?: number;
};
type LLMContentWithFileOpts = {
    /**
     * The media MIME type, e.g. "image/png". Inferred from the file's contents when omitted.
     */
    mimeType?: string;
};
type LLMContentBlockInput = {
    /**
     * The arguments to pass to the tool (for TOOL_CALL kind).
     */
    arguments?: JSON;
    /**
     * The unique ID of a tool call (for TOOL_CALL or TOOL_RESULT kinds).
     */
    callId?: string;
    /**
     * Ordered TEXT or media blocks returned by a tool.
     */
    content?: LLMContentBlockInput[];
    /**
     * Media bytes. Supply exactly one of data or file for media.
     */
    data?: Bytes;
    /**
     * Whether the tool call resulted in an error (for TOOL_RESULT kind).
     */
    errored?: boolean;
    /**
     * A media file to resolve to inline bytes.
     */
    file?: File;
    /**
     * The kind of content block.
     */
    kind: LLMContentBlockKind;
    /**
     * Media MIME type; required for inline data, inferred for a file.
     */
    mimeType?: string;
    /**
     * Provider-specific opaque data (e.g. Anthropic thinking signature).
     */
    signature?: string;
    /**
     * Text content (for TEXT, THINKING, or TOOL_RESULT kinds).
     */
    text?: string;
    /**
     * The name of the tool to call (for TOOL_CALL kind).
     */
    toolName?: string;
};
/**
 * The kind of content in a message block.
 */
declare enum LLMContentBlockKind {
    /**
     * Inline audio.
     */
    Audio = "AUDIO",
    /**
     * An inline PDF document.
     */
    Document = "DOCUMENT",
    /**
     * An inline image.
     */
    Image = "IMAGE",
    /**
     * Plain text content.
     */
    Text = "TEXT",
    /**
     * Model thinking/reasoning content (e.g. Anthropic extended thinking).
     */
    Thinking = "THINKING",
    /**
     * A tool/function call from the model.
     */
    ToolCall = "TOOL_CALL",
    /**
     * A tool/function result.
     */
    ToolResult = "TOOL_RESULT"
}
/**
 * Utility function to convert a LLMContentBlockKind value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function LLMContentBlockKindValueToName(value: LLMContentBlockKind): string;
/**
 * Utility function to convert a LLMContentBlockKind name to its value so
 * it can be properly used inside the module runtime.
 */
declare function LLMContentBlockKindNameToValue(name: string): LLMContentBlockKind;
type LLMMessageOriginInput = {
    /**
     * The display name of the sending or observed agent.
     */
    agentName?: string;
    /**
     * Who put this message on the record.
     */
    kind: LLMMessageOriginKind;
    /**
     * The message's short ref within the receiving agent's runtime, e.g. "#3".
     */
    ref?: string;
    /**
     * The ref of the message this one answers, if any.
     */
    replyTo?: string;
};
/**
 * EXPERIMENTAL: Agent APIs are likely to change.
 *
 * Who put a message on the conversation record.
 */
declare enum LLMMessageOriginKind {
    /**
     * Another agent: the message was sent from within that agent's turn.
     */
    Agent = "AGENT",
    /**
     * The engine, reporting a subscribed agent's lifecycle transition.
     */
    Event = "EVENT",
    /**
     * The user: a prompt submitted by a client rather than sent by an agent.
     */
    User = "USER"
}
/**
 * Utility function to convert a LLMMessageOriginKind value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function LLMMessageOriginKindValueToName(value: LLMMessageOriginKind): string;
/**
 * Utility function to convert a LLMMessageOriginKind name to its value so
 * it can be properly used inside the module runtime.
 */
declare function LLMMessageOriginKindNameToValue(name: string): LLMMessageOriginKind;
/**
 * The role that generated a message.
 */
declare enum LLMMessageRole {
    /**
     * A reply from the model.
     */
    Assistant = "ASSISTANT",
    /**
     * A system prompt.
     */
    System = "SYSTEM",
    /**
     * A user prompt or tool response.
     */
    User = "USER"
}
/**
 * Utility function to convert a LLMMessageRole value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function LLMMessageRoleValueToName(value: LLMMessageRole): string;
/**
 * Utility function to convert a LLMMessageRole name to its value so
 * it can be properly used inside the module runtime.
 */
declare function LLMMessageRoleNameToValue(name: string): LLMMessageRole;
type ModuleServeOpts = {
    /**
     * Expose the dependencies of this module to the client
     */
    includeDependencies?: boolean;
    /**
     * Install the module as the workspace main, promoting its main-object methods onto the Query root
     */
    main?: boolean;
    /**
     * Compatibility alias for main.
     *
     * @deprecated Use main.
     */
    entrypoint?: boolean;
};
type ModuleSourceClientSchemaIntrospectionJsonOpts = {
    /**
     * The engine version of the client the schema is generated for, e.g. the engineVersion of the module that depends on this one: core is described as that client sees it, capped at this engine's version.
     *
     * This module is still loaded at the engine version it declares, and so named by its own naming rules, as the engine serves it to such a client. Defaults to this module's own engine version.
     *
     * Use this rather than withEngineVersion, which makes this module declare another version and so changes its names.
     */
    clientVersion?: string;
};
/**
 * Experimental features of a module
 */
declare enum ModuleSourceExperimentalFeature {
    /**
     * Self calls
     */
    SelfCalls = "SELF_CALLS"
}
/**
 * Utility function to convert a ModuleSourceExperimentalFeature value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function ModuleSourceExperimentalFeatureValueToName(value: ModuleSourceExperimentalFeature): string;
/**
 * Utility function to convert a ModuleSourceExperimentalFeature name to its value so
 * it can be properly used inside the module runtime.
 */
declare function ModuleSourceExperimentalFeatureNameToValue(name: string): ModuleSourceExperimentalFeature;
/**
 * The kind of module source.
 */
declare enum ModuleSourceKind {
    Dir = "DIR_SOURCE",
    DirSource = "DIR_SOURCE",
    Git = "GIT_SOURCE",
    GitSource = "GIT_SOURCE",
    Local = "LOCAL_SOURCE",
    LocalSource = "LOCAL_SOURCE"
}
/**
 * Utility function to convert a ModuleSourceKind value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function ModuleSourceKindValueToName(value: ModuleSourceKind): string;
/**
 * Utility function to convert a ModuleSourceKind name to its value so
 * it can be properly used inside the module runtime.
 */
declare function ModuleSourceKindNameToValue(name: string): ModuleSourceKind;
type NamingFormatIdentifiersOpts = {
    /**
     * How to write acronyms and terms where a word starts with a capital.
     */
    acronyms?: AcronymStyle;
};
type NamingModuleOpts = {
    /**
     * The module's original name, when it's loaded under another (as a dependency alias): its main object and namespaced type names are spelled after it. Defaults to name.
     */
    originalName?: string;
};
/**
 * Transport layer network protocol associated to a port.
 */
declare enum NetworkProtocol {
    Tcp = "TCP",
    Udp = "UDP"
}
/**
 * Utility function to convert a NetworkProtocol value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function NetworkProtocolValueToName(value: NetworkProtocol): string;
/**
 * Utility function to convert a NetworkProtocol name to its value so
 * it can be properly used inside the module runtime.
 */
declare function NetworkProtocolNameToValue(name: string): NetworkProtocol;
/**
 * How to handle patch hunks that no longer apply to the target content.
 */
declare enum PatchConflict {
    /**
     * Fail the operation if any part of the patch does not apply.
     */
    Fail = "FAIL",
    /**
     * Apply the hunks that fit and insert conflict markers where hunks no longer match, instead of failing.
     */
    LeaveConflictMarkers = "LEAVE_CONFLICT_MARKERS"
}
/**
 * Utility function to convert a PatchConflict value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function PatchConflictValueToName(value: PatchConflict): string;
/**
 * Utility function to convert a PatchConflict name to its value so
 * it can be properly used inside the module runtime.
 */
declare function PatchConflictNameToValue(name: string): PatchConflict;
type PipelineLabel = {
    /**
     * Label name.
     */
    name: string;
    /**
     * Label value.
     */
    value: string;
};
/**
 * The platform config OS and architecture in a Container.
 *
 * The format is [os]/[platform]/[version] (e.g., "darwin/arm64/v7", "windows/amd64", "linux/arm64").
 */
type Platform = string & {
    __Platform: never;
};
type PortForward = {
    /**
     * Destination port for traffic.
     */
    backend: number;
    /**
     * Port to expose to clients. If unspecified, a default will be chosen.
     */
    frontend?: number;
    /**
     * Transport layer protocol to use for traffic.
     */
    protocol?: NetworkProtocol;
};
type ClientBlobOpts = {
    /**
     * Permissions of the new file. Example: 0600
     */
    permissions?: number;
};
type ClientCacheVolumeOpts = {
    /**
     * Identifier of the directory to use as the cache volume's root.
     */
    source?: Directory;
    /**
     * Sharing mode of the cache volume.
     */
    sharing?: CacheSharingMode;
    /**
     * A user:group to set for the cache volume root.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    owner?: string;
};
type ClientContainerOpts = {
    /**
     * Platform to initialize the container with. Defaults to the native platform of the current engine
     */
    platform?: Platform;
};
type ClientCurrentTypeDefsOpts = {
    /**
     * Return the full referenced typedef closure instead of only top-level served typedefs.
     */
    returnAllTypes?: boolean;
    /**
     * Strip core API functions from the Query type, leaving only module-sourced functions (constructors, workspace main proxies, etc.).
     *
     * Core types (Container, Directory, etc.) are kept so return types and method chaining still work.
     */
    hideCore?: boolean;
};
type ClientEngineVolumeOpts = {
    /**
     * Optional existing subdirectory within the volume payload to mount.
     */
    subdir?: string;
};
type ClientEnvFileOpts = {
    /**
     * Replace "${VAR}" or "$VAR" with the value of other vars
     *
     * @deprecated Variable expansion is now enabled by default
     */
    expand?: boolean;
};
type ClientFileOpts = {
    /**
     * Permissions of the new file. Example: 0600
     */
    permissions?: number;
};
type ClientGitOpts = {
    /**
     * DEPRECATED: Set to true to keep .git directory.
     *
     * @deprecated Set to true to keep .git directory.
     */
    keepGitDir?: boolean;
    /**
     * Set SSH known hosts
     */
    sshKnownHosts?: string;
    /**
     * Set SSH auth socket
     */
    sshAuthSocket?: Socket;
    /**
     * Username used to populate the password during basic HTTP Authorization
     */
    httpAuthUsername?: string;
    /**
     * Secret used to populate the password during basic HTTP Authorization
     */
    httpAuthToken?: Secret;
    /**
     * Secret used to populate the Authorization HTTP header
     */
    httpAuthHeader?: Secret;
    /**
     * A service which must be started before the repo is fetched.
     */
    experimentalServiceHost?: Service;
};
type ClientHttpOpts = {
    /**
     * File name to use for the file. Defaults to the last part of the URL.
     */
    name?: string;
    /**
     * Permissions to set on the file.
     */
    permissions?: number;
    /**
     * Expected digest of the downloaded content (e.g., "sha256:...").
     */
    checksum?: string;
    /**
     * Secret used to populate the Authorization HTTP header
     */
    authHeader?: Secret;
    /**
     * A service which must be started before the URL is fetched.
     */
    experimentalServiceHost?: Service;
};
type ClientLLMOpts = {
    /**
     * The model to converse with, e.g. "claude-sonnet-4-5" or "gpt-5.4". Defaults to the configured default model.
     */
    model?: string;
    /**
     * The provider serving the model, e.g. "openai". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.
     */
    provider?: string;
};
type ClientModuleSourceOpts = {
    /**
     * Version query for a Git module source.
     */
    version?: string;
    /**
     * The pinned version of the module source
     */
    refPin?: string;
    /**
     * If true, do not attempt to find a module config file in a parent directory of the provided path. Only relevant for local module sources.
     */
    disableFindUp?: boolean;
    /**
     * If true, do not error out if the provided ref string is a local path and does not exist yet. Useful when initializing new modules in directories that don't exist yet.
     */
    allowNotExists?: boolean;
    /**
     * If set, error out if the ref string is not of the provided requireKind.
     */
    requireKind?: ModuleSourceKind;
};
type ClientNamingOpts = {
    /**
     * The engine version to answer for, e.g. the engine version a module declares. Defaults to the caller's. Normalized as a module's is: a version past the engine's is capped at the engine's own.
     */
    version?: string;
};
type ClientSecretOpts = {
    /**
     * If set, the given string will be used as the cache key for this secret. This means that any secrets with the same cache key will be considered equivalent in terms of cache lookups, even if they have different URIs or plaintext values.
     *
     * For example, two secrets with the same cache key provided as secret env vars to other wise equivalent containers will result in the container withExecs hitting the cache for each other.
     *
     * If not set, the cache key for the secret will be derived from its plaintext value as looked up when the secret is constructed.
     */
    cacheKey?: string;
};
type ClientServeModuleOpts = {
    /**
     * The pinned version of a remote module address.
     */
    refPin?: string;
};
type ClientSshfsVolumeOpts = {
    /**
     * known_hosts material used to verify the remote host key. Required unless insecureSkipHostKeyCheck is true.
     */
    knownHosts?: Secret;
    /**
     * Optional cache equivalence key. If set, volumes with the same cacheKey may be considered equivalent for cache lookups, still subject to their resource dependencies.
     */
    cacheKey?: string;
    /**
     * Disable SSH host key verification. This is insecure and must be explicitly opted into.
     */
    insecureSkipHostKeyCheck?: boolean;
    /**
     * Service to use as the SSHFS network endpoint while verifying the original host key.
     */
    experimentalServiceHost?: Service;
    /**
     * Resolve symlinks on the remote host, so they appear as the files and directories they point to. Without it, absolute symlinks and symlinks containing ".." can't be read. With it, creating a symlink in the volume fails.
     */
    followSymlinks?: boolean;
};
/**
 * Transport protocol to use for registry operations.
 */
declare enum RegistryProtocol {
    Http = "HTTP",
    Https = "HTTPS"
}
/**
 * Utility function to convert a RegistryProtocol value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function RegistryProtocolValueToName(value: RegistryProtocol): string;
/**
 * Utility function to convert a RegistryProtocol name to its value so
 * it can be properly used inside the module runtime.
 */
declare function RegistryProtocolNameToValue(name: string): RegistryProtocol;
/**
 * Expected return type of an execution
 */
declare enum ReturnType {
    /**
     * Any execution (exit codes 0-127 and 192-255)
     */
    Any = "ANY",
    /**
     * A failed execution (exit codes 1-127 and 192-255)
     */
    Failure = "FAILURE",
    /**
     * A successful execution (exit code 0)
     */
    Success = "SUCCESS"
}
/**
 * Utility function to convert a ReturnType value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function ReturnTypeValueToName(value: ReturnType): string;
/**
 * Utility function to convert a ReturnType name to its value so
 * it can be properly used inside the module runtime.
 */
declare function ReturnTypeNameToValue(name: string): ReturnType;
type ServiceEndpointOpts = {
    /**
     * The exposed port number for the endpoint
     */
    port?: number;
    /**
     * Return a URL with the given scheme, eg. http for http://
     */
    scheme?: string;
};
type ServicePortsOpts = {
    /**
     * Return only container ports declared before startup. Other service types return an empty list.
     */
    declared?: boolean;
};
type ServiceStopOpts = {
    /**
     * Immediately kill the service without waiting for a graceful exit
     */
    kill?: boolean;
};
type ServiceTerminalOpts = {
    cmd?: string[];
};
type ServiceUpOpts = {
    /**
     * List of frontend/backend port mappings to forward.
     *
     * Frontend is the port accepting traffic on the host, backend is the service port.
     */
    ports?: PortForward[];
    /**
     * Bind each tunnel port to a random port on the host.
     */
    random?: boolean;
};
type TypeDefWithEnumOpts = {
    /**
     * A doc string for the enum, if any
     */
    description?: string;
    /**
     * The source map for the enum definition.
     */
    sourceMap?: SourceMap;
};
type TypeDefWithEnumMemberOpts = {
    /**
     * The value of the member in the enum
     */
    value?: string;
    /**
     * A doc string for the member, if any
     */
    description?: string;
    /**
     * The source map for the enum member definition.
     */
    sourceMap?: SourceMap;
    /**
     * If deprecated, the reason or migration path.
     */
    deprecated?: string;
};
type TypeDefWithEnumValueOpts = {
    /**
     * A doc string for the value, if any
     */
    description?: string;
    /**
     * The source map for the enum value definition.
     */
    sourceMap?: SourceMap;
    /**
     * If deprecated, the reason or migration path.
     */
    deprecated?: string;
};
type TypeDefWithFieldOpts = {
    /**
     * A doc string for the field, if any
     */
    description?: string;
    /**
     * The source map for the field definition.
     */
    sourceMap?: SourceMap;
    /**
     * If deprecated, the reason or migration path.
     */
    deprecated?: string;
};
type TypeDefWithInterfaceOpts = {
    description?: string;
    sourceMap?: SourceMap;
};
type TypeDefWithObjectOpts = {
    description?: string;
    sourceMap?: SourceMap;
    deprecated?: string;
};
type TypeDefWithScalarOpts = {
    description?: string;
};
/**
 * Distinguishes the different kinds of TypeDefs.
 */
declare enum TypeDefKind {
    /**
     * A boolean value.
     */
    Boolean = "BOOLEAN_KIND",
    /**
     * A boolean value.
     */
    BooleanKind = "BOOLEAN_KIND",
    /**
     * A GraphQL enum type and its values
     *
     * Always paired with an EnumTypeDef.
     */
    Enum = "ENUM_KIND",
    /**
     * A GraphQL enum type and its values
     *
     * Always paired with an EnumTypeDef.
     */
    EnumKind = "ENUM_KIND",
    /**
     * A float value.
     */
    Float = "FLOAT_KIND",
    /**
     * A float value.
     */
    FloatKind = "FLOAT_KIND",
    /**
     * A graphql input type, used only when representing the core API via TypeDefs.
     */
    Input = "INPUT_KIND",
    /**
     * A graphql input type, used only when representing the core API via TypeDefs.
     */
    InputKind = "INPUT_KIND",
    /**
     * An integer value.
     */
    Integer = "INTEGER_KIND",
    /**
     * An integer value.
     */
    IntegerKind = "INTEGER_KIND",
    /**
     * Always paired with an InterfaceTypeDef.
     *
     * A named type of functions that can be matched+implemented by other objects+interfaces.
     */
    Interface = "INTERFACE_KIND",
    /**
     * Always paired with an InterfaceTypeDef.
     *
     * A named type of functions that can be matched+implemented by other objects+interfaces.
     */
    InterfaceKind = "INTERFACE_KIND",
    /**
     * Always paired with a ListTypeDef.
     *
     * A list of values all having the same type.
     */
    List = "LIST_KIND",
    /**
     * Always paired with a ListTypeDef.
     *
     * A list of values all having the same type.
     */
    ListKind = "LIST_KIND",
    /**
     * Always paired with an ObjectTypeDef.
     *
     * A named type defined in the GraphQL schema, with fields and functions.
     */
    Object = "OBJECT_KIND",
    /**
     * Always paired with an ObjectTypeDef.
     *
     * A named type defined in the GraphQL schema, with fields and functions.
     */
    ObjectKind = "OBJECT_KIND",
    /**
     * A scalar value of any basic kind.
     */
    Scalar = "SCALAR_KIND",
    /**
     * A scalar value of any basic kind.
     */
    ScalarKind = "SCALAR_KIND",
    /**
     * A string value.
     */
    String = "STRING_KIND",
    /**
     * A string value.
     */
    StringKind = "STRING_KIND",
    /**
     * A special kind used to signify that no value is returned.
     *
     * This is used for functions that have no return value. The outer TypeDef specifying this Kind is always Optional, as the Void is never actually represented.
     */
    Void = "VOID_KIND",
    /**
     * A special kind used to signify that no value is returned.
     *
     * This is used for functions that have no return value. The outer TypeDef specifying this Kind is always Optional, as the Void is never actually represented.
     */
    VoidKind = "VOID_KIND"
}
/**
 * Utility function to convert a TypeDefKind value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function TypeDefKindValueToName(value: TypeDefKind): string;
/**
 * Utility function to convert a TypeDefKind name to its value so
 * it can be properly used inside the module runtime.
 */
declare function TypeDefKindNameToValue(name: string): TypeDefKind;
/**
 * The absence of a value.
 *
 * A Null Void is used as a placeholder for resolvers that do not return anything.
 */
type Void = string & {
    __Void: never;
};
type WorkspaceArtifactsOpts = {
    /**
     * Only include artifacts matching these path patterns, as with checks and services. A path selects that path and its children.
     */
    include?: string[];
};
type WorkspaceChangesOpts = {
    /**
     * An earlier workspace state to compare against.
     */
    from?: Workspace;
};
type WorkspaceCompareCommitsFromOpts = {
    /**
     * Full lowercase commit hashes or unambiguous lowercase hex prefixes (4-40 characters) to select, in any order. Prefixes resolve against the frozen source's Git objects and are recorded as full hashes; duplicate selections after resolution are rejected. Empty selects all new source commits. Selected commits must be within the source's latest 10000 commits.
     */
    commits?: string[];
    /**
     * Maximum commits in either differing history, from 1 to 1000. Exceeding the limit fails; nothing is silently omitted.
     */
    maxCommits?: number;
};
type WorkspaceConfigReadOpts = {
    /**
     * Dotted key path (e.g. modules.greeter.source). Empty for full config.
     */
    key?: string;
    /**
     * Include the selected environment, user overrides, and legacy workspace settings.
     */
    effective?: boolean;
};
type WorkspaceDirectoryOpts = {
    /**
     * Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
     */
    exclude?: string[];
    /**
     * Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
     */
    include?: string[];
    /**
     * Apply .gitignore filter rules inside the directory.
     */
    gitignore?: boolean;
};
type WorkspaceExportOpts = {
    /**
     * Destination checkout path on the calling client. Relative paths start at the client's working directory. Omit to use the calling client's current local workspace root.
     */
    path?: string;
    /**
     * Earlier workspace state to compare against. For Git integration, live inputs are snapshotted at export time; use a snapshot to retain the baseline of a previous export.
     */
    from?: Workspace;
};
type WorkspaceFindRootsOpts = {
    /**
     * Directory to start from. Relative paths resolve from the workspace cwd.
     */
    start?: string;
    /**
     * File basenames that mark a project root (e.g. ["go.mod"] or ["deno.json", "deno.jsonc"]).
     */
    markers: string[];
    /**
     * Glob patterns pruning the walk below start (e.g. ["**\/node_modules/**"]).
     */
    exclude?: string[];
};
type WorkspaceFindUpOpts = {
    /**
     * Path to start the search from. Relative paths resolve from the workspace cwd; absolute paths resolve from the workspace root.
     */
    from?: string;
};
type WorkspaceMigrateOpts = {
    /**
     * Additional local modules to migrate. Relative paths start at the workspace cwd; absolute paths start at the workspace root.
     */
    modules?: string[];
};
type WorkspaceMigrateModuleOpts = {
    /**
     * Module directory. Relative paths start at the workspace cwd; absolute paths start at the workspace root.
     */
    path?: string;
};
type WorkspaceSearchOpts = {
    /**
     * Directory or file paths to search
     */
    paths?: string[];
    /**
     * Glob patterns to match (e.g., "*.md")
     */
    globs?: string[];
    /**
     * The text to match.
     */
    pattern: string;
    /**
     * Interpret the pattern as a literal string instead of a regular expression.
     */
    literal?: boolean;
    /**
     * Enable searching across multiple lines.
     */
    multiline?: boolean;
    /**
     * Allow the . pattern to match newlines in multiline mode.
     */
    dotall?: boolean;
    /**
     * Enable case-insensitive matching.
     */
    insensitive?: boolean;
    /**
     * Honor .gitignore, .ignore, and .rgignore files.
     */
    skipIgnored?: boolean;
    /**
     * Skip hidden files (files starting with .).
     */
    skipHidden?: boolean;
    /**
     * Only return matching files, not lines and content
     */
    filesOnly?: boolean;
    /**
     * Limit the number of results to return
     */
    limit?: number;
};
type WorkspaceWithClientOpts = {
    /**
     * Optional SDK name. Inspect all installed SDKs when omitted.
     */
    sdk?: string;
    /**
     * Explicit SDK-module constructor setting overrides for this scope. Requires an explicit SDK name.
     */
    settings?: JSON;
};
type WorkspaceWithCommitOpts = {
    /**
     * Author and committer name. Defaults to git config user.name in the calling client's working directory, otherwise Dagger.
     */
    authorName?: string;
    /**
     * Author and committer email. Defaults to git config user.email in the calling client's working directory, otherwise dagger@localhost.
     */
    authorEmail?: string;
    /**
     * Add a Signed-off-by trailer using the commit author's name and email.
     */
    signoff?: boolean;
};
type WorkspaceWithCommitsFromOpts = {
    /**
     * Full lowercase commit hashes or unambiguous lowercase hex prefixes (4-40 characters) to select, in any order. Prefixes resolve against the frozen source's Git objects and are recorded as full hashes; duplicate selections after resolution are rejected. Empty selects all new source commits. Selected commits must be within the source's latest 10000 commits.
     */
    commits?: string[];
    /**
     * Maximum commits in either differing history, from 1 to 1000. Exceeding the limit fails; nothing is silently omitted.
     */
    maxCommits?: number;
};
type WorkspaceWithConfigEnvOpts = {
    /**
     * Write to the workspace config directory at the workspace cwd.
     */
    here?: boolean;
};
type WorkspaceWithConfigValueOpts = {
    /**
     * List value to set. Elements are stored verbatim, with no auto-detection. Mutually exclusive with value.
     */
    values?: string[];
    /**
     * Write to the workspace config directory at the workspace cwd.
     */
    here?: boolean;
};
type WorkspaceWithFileOpts = {
    /**
     * Permissions of the added file. Defaults to the source file permissions.
     */
    permissions?: number;
};
type WorkspaceWithInitModuleOpts = {
    /**
     * Module name. The engine infers it from path, the active config file, or the workspace root when omitted.
     */
    name?: string;
    /**
     * Module path relative to the workspace cwd, or an absolute workspace path. Defaults to .dagger/modules/<name> beside the active workspace config.
     */
    path?: string;
    /**
     * Install the module. When omitted, install only if path is omitted.
     */
    install?: boolean;
    /**
     * Select this module as the workspace main and install it. False prevents automatic selection. When omitted, select only if both path and name are omitted and the module is installed.
     */
    main?: boolean;
    /**
     * Compatibility alias for main.
     *
     * @deprecated Use main.
     */
    entrypoint?: boolean;
    /**
     * Explicit SDK-module constructor setting overrides for this scope.
     */
    settings?: JSON;
};
type WorkspaceWithModuleOpts = {
    /**
     * Override name for the installed module entry.
     */
    name?: string;
    /**
     * Write to the workspace config directory at the workspace cwd.
     */
    here?: boolean;
};
type WorkspaceWithNewFileOpts = {
    /**
     * Permissions of the new file.
     */
    permissions?: number;
};
type WorkspaceWithPatchFileOpts = {
    /**
     * How to handle hunks that no longer apply to the target content: fail (default), or apply what fits and leave git-style conflict markers where it doesn't.
     */
    onConflict?: PatchConflict;
};
type WorkspaceWithResetOpts = {
    /**
     * Discard uncommitted changes, resetting the working tree to the commit.
     */
    hard?: boolean;
};
type WorkspaceWithSdkOpts = {
    /**
     * Override name for the installed SDK entry.
     */
    name?: string;
    /**
     * Write to the workspace config directory at the workspace cwd.
     */
    here?: boolean;
    /**
     * Optional override for the SDK name conventionally derived from the installed module name.
     */
    asSdkName?: string;
};
type WorkspaceWithUpdatedClientsOpts = {
    /**
     * Recorded client targets to update. All targets in the selected scopes are updated when omitted.
     */
    modules?: string[];
    /**
     * Select clients in every scope instead of only the scopes containing the workspace cwd.
     */
    all?: boolean;
    /**
     * Optional SDK name. All installed SDK modules are selected when omitted.
     */
    sdk?: string;
};
type WorkspaceWithUpdatedLockOpts = {
    /**
     * Do not regenerate SDK client scopes.
     */
    noGenerate?: boolean;
};
type WorkspaceWithUpdatedModulesOpts = {
    /**
     * Installed module names or sources. A version suffix sets a new request. An empty list refreshes all installed modules.
     */
    names?: string[];
    /**
     * New version request for exactly one selected module. Cannot be combined with a version suffix.
     */
    version?: string;
    /**
     * New source for exactly one selected module. Resolved like an install source. Cannot be combined with a version or a version suffix.
     */
    source?: string;
};
type WorkspaceWithoutClientOpts = {
    /**
     * Optional SDK name. Search all installed SDKs when omitted.
     */
    sdk?: string;
};
type WorkspaceWithoutConfigEnvOpts = {
    /**
     * Write to the workspace config directory at the workspace cwd.
     */
    here?: boolean;
};
type WorkspaceWithoutConfigValueOpts = {
    /**
     * Write to the workspace config directory at the workspace cwd.
     */
    here?: boolean;
};
type WorkspaceWithoutModuleOpts = {
    /**
     * Write to the workspace config directory at the workspace cwd.
     */
    here?: boolean;
};
type WorkspaceWithoutSdkOpts = {
    /**
     * Write to the workspace config directory at the workspace cwd.
     */
    here?: boolean;
};
/**
 * Why a source commit cannot be pulled.
 */
declare enum WorkspaceCommitPickReason {
    /**
     * The patch conflicts with committed content.
     */
    Content = "CONTENT",
    /**
     * The commit touches uncommitted paths in the receiving workspace.
     */
    Dirty = "DIRTY",
    /**
     * No conflict.
     */
    None = "NONE"
}
/**
 * Utility function to convert a WorkspaceCommitPickReason value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function WorkspaceCommitPickReasonValueToName(value: WorkspaceCommitPickReason): string;
/**
 * Utility function to convert a WorkspaceCommitPickReason name to its value so
 * it can be properly used inside the module runtime.
 */
declare function WorkspaceCommitPickReasonNameToValue(name: string): WorkspaceCommitPickReason;
/**
 * Whether a source commit can be pulled.
 */
declare enum WorkspaceCommitPickStatus {
    /**
     * The commit cannot be applied; see reason and conflictPaths.
     */
    Conflict = "CONFLICT",
    /**
     * The commit can be applied.
     */
    Pickable = "PICKABLE",
    /**
     * The commit is already present by hash or cherry-pick origin.
     */
    Picked = "PICKED",
    /**
     * The patch is already present, or applying it would be empty.
     */
    Redundant = "REDUNDANT"
}
/**
 * Utility function to convert a WorkspaceCommitPickStatus value to its name so
 * it can be uses as argument to call a exposed function.
 */
declare function WorkspaceCommitPickStatusValueToName(value: WorkspaceCommitPickStatus): string;
/**
 * Utility function to convert a WorkspaceCommitPickStatus name to its value so
 * it can be properly used inside the module runtime.
 */
declare function WorkspaceCommitPickStatusNameToValue(name: string): WorkspaceCommitPickStatus;
type __DirectiveArgsOpts = {
    includeDeprecated?: boolean;
};
type __FieldArgsOpts = {
    includeDeprecated?: boolean;
};
type __TypeEnumValuesOpts = {
    includeDeprecated?: boolean;
};
type __TypeFieldsOpts = {
    includeDeprecated?: boolean;
};
type __TypeInputFieldsOpts = {
    includeDeprecated?: boolean;
};
/**
 * A standardized address to load containers, directories, secrets, and other object types. Address format depends on the type, and is validated at type selection.
 */
declare class Address extends BaseClient {
    private readonly _id?;
    private readonly _value?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _value?: string);
    /**
     * A unique identifier for this Address.
     */
    id: () => Promise<ID>;
    /**
     * Load a container from the address.
     * @param opts.noLock Resolve the address's image tag live, ignoring the workspace lockfile: neither read a pinned value nor record one.
     *
     * A DAG address is unaffected: its module evaluates as usual.
     */
    container: (opts?: AddressContainerOpts) => Container;
    /**
     * Load a directory from the address.
     * @param opts.noLock Resolve the address's git ref live, ignoring the workspace lockfile: neither read a pinned value nor record one.
     *
     * A DAG address is unaffected: its module evaluates as usual.
     */
    directory: (opts?: AddressDirectoryOpts) => Directory;
    /**
     * Load a file from the address.
     * @param opts.noLock Resolve the address's git ref live, ignoring the workspace lockfile: neither read a pinned value nor record one.
     *
     * A DAG address is unaffected: its module evaluates as usual.
     */
    file: (opts?: AddressFileOpts) => File;
    /**
     * Load a git ref (branch, tag or commit) from the address.
     * @param opts.noLock Resolve the address's git ref live, ignoring the workspace lockfile: neither read a pinned value nor record one.
     *
     * A DAG address is unaffected: its module evaluates as usual.
     */
    gitRef: (opts?: AddressGitRefOpts) => GitRef;
    /**
     * Load a git repository from the address.
     */
    gitRepository: () => GitRepository;
    /**
     * Load a secret from the address.
     */
    secret: () => Secret;
    /**
     * Load a service from the address.
     */
    service: () => Service;
    /**
     * Load a local socket from the address.
     */
    socket: () => Socket;
    /**
     * The address value
     */
    value: () => Promise<string>;
    /**
     * Load a volume from the address.
     */
    volume: () => Volume;
    /**
     * Load a workspace from a module reference.
     */
    workspace: () => Workspace;
}
/**
 * EXPERIMENTAL: Agent APIs are likely to change.
 *
 * A conversation loop running as an addressable, long-lived entity within the session. The conversation itself remains observable at any time as an immutable LLM value.
 */
declare class Agent extends BaseClient {
    private readonly _id?;
    private readonly _error?;
    private readonly _handle?;
    private readonly _name?;
    private readonly _notify?;
    private readonly _pause?;
    private readonly _reseed?;
    private readonly _resume?;
    private readonly _send?;
    private readonly _state?;
    private readonly _stop?;
    private readonly _wait?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _error?: string, _handle?: string, _name?: string, _notify?: ID, _pause?: ID, _reseed?: ID, _resume?: ID, _send?: ID, _state?: AgentState, _stop?: ID, _wait?: ID);
    /**
     * A unique identifier for this Agent.
     */
    id: () => Promise<ID>;
    /**
     * Why the loop failed, for a FAILED agent; empty otherwise.
     *
     * The snapshot holds the completed prefix — send or resume retries from it.
     * @experimental
     */
    error: () => Promise<string>;
    /**
     * The opaque runtime handle minted by the spawn that created this agent.
     *
     * It is the same value the agent's loop span publishes as dagger.io/agent.id, so a client can correlate the agent with what it discovers in the trace. Two spawns of an identical composition have different handles; a display name is shared freely.
     * @experimental
     */
    handle: () => Promise<string>;
    /**
     * Look up a previously sent message by its ref.
     *
     * This is the lookup send pins its result's identity through: the returned handle's ID is an honest, replayable chain, addressable from any request in the session (the cancel-and-request-again contract).
     *
     * Fails if the agent has no runtime entry in this session, or no record of the given ref.
     * @param ref The message's short ref within this agent's runtime, e.g. "#3": the token its attribution header shows and a reply's replyTo names. A bare ordinal ("3") is accepted too.
     * @experimental
     */
    message: (ref: string) => AgentMessage;
    /**
     * Display label for the agent; carries no identity.
     * @experimental
     */
    name: () => Promise<string>;
    /**
     * Subscribe another agent to this agent's lifecycle: each transition into one of the given states enqueues an event message to the subscriber — steering its open turn, or waking it if idle, like any other message.
     *
     * This is how a supervisor hears every completion and failure without polling or blocking: subscribe at spawn time, keep working, and events arrive as attributed messages.
     *
     * Events never relaunch a stopped subscriber, and an already-reached state fires immediately at subscribe time, so a fast agent settling before the subscription lands is not missed.
     *
     * A restored agent that nothing has sent to, started, or resumed yet is the exception: its state was reached in the session it was restored from, so subscribing to it announces nothing until it next transitions. This is how a restore reinstalls recorded subscriptions without waking their subscribers.
     *
     * Idempotent per subscriber; re-subscribing replaces the state set.
     * @param subscriber The agent to deliver event messages to. You must hold its handle: subscriptions are capability-based like everything else.
     * @param opts.on The lifecycle states that fire an event. IDLE events carry the turn's final reply; FAILED events carry the loop error.
     * @experimental
     */
    notify: (subscriber: Agent, opts?: AgentNotifyOpts) => Promise<Agent>;
    /**
     * The agent that spawned this agent, or null for a root agent.
     * @experimental
     */
    parent: () => Promise<Agent | null>;
    /**
     * Stop draining the mailbox once the in-flight step completes, or immediately with interrupt.
     *
     * Pause takes priority over pending work: a mid-turn pause suspends the turn, which resume continues. Messages sent while paused enqueue with QUEUED delivery until a resume.
     *
     * Pausing a never-started agent leaves it paused for its eventual resume; pausing a failed agent is allowed (resume decides the retry); pausing a stopped agent fails.
     * @param opts.interrupt Preempt the in-flight step instead of letting it finish. All completed steps are kept and the interrupted turn stays open: messages it consumed remain pending, while unconsumed mailbox messages are discarded. Resume continues the turn from the last committed step. On an idle, never-started, or failed agent there is nothing to preempt, so this is a plain pause.
     * @experimental
     */
    pause: (opts?: AgentPauseOpts) => Promise<Agent>;
    /**
     * Replace this instance's committed conversation with the given one, keeping the entry: identity, mailbox, and lifecycle state are untouched. A paused suspended turn is abandoned and its consumed messages are resolved before replacement.
     *
     * This is the continuity verb. Compaction, a workspace rebind, a model change, or rewinding an interrupted prompt produce a new conversation value for the SAME agent; reseed swaps it in place, where a stop-and-respawn would mint a successor instance and split the agent across two roster entries. It is the client-facing form of what a continuation tool already does mid-turn: the agent adopts a new conversation without changing who it is.
     *
     * The next turn continues from the reseeded conversation, and queued messages drain onto it. A FAILED agent keeps its error — resume retries from the new conversation.
     *
     * Fails if the instance has no runtime entry in this session (only a spawned or re-hydrated instance holds a conversation to replace), if a step is in flight, or if the agent is stopped.
     * @param conversation The conversation that becomes the agent's committed history, replacing the current one.
     * @experimental
     */
    reseed: (conversation: LLM) => Promise<Agent>;
    /**
     * Resume draining the mailbox: a suspended turn continues from the last committed step, and queued messages drain.
     *
     * Resuming a never-started agent starts its evaluation loop, detached from the calling request: it steps the conversation while input is pending, then idles awaiting further lifecycle operations. Resuming a FAILED agent retries its pending step. Resuming a STOPPED agent relaunches the same instance from its last committed snapshot.
     *
     * No-op on a running or idle agent.
     * @experimental
     */
    resume: () => Promise<Agent>;
    /**
     * The conversation the agent was spawned from: the tools, workspace and message history its loop started with.
     *
     * Fixed by the spawn; stepping does not move it (see snapshot for the latest committed step). Its workspace is the baseline the agent's own changes are measured from.
     *
     * For an agent restored under a handle, this is the conversation it was restored from.
     * @experimental
     */
    seed: () => LLM;
    /**
     * Enqueue a message, on the record: it is consumed at a step boundary, appends to the agent's history, and steers the running turn or opens a new one.
     *
     * Never blocks, never drops; concurrent sends queue in order.
     *
     * The returned message is pinned through the message lookup field, so its handle is re-addressable from any request in the session: cancel a response request and request it again freely.
     *
     * Sending to a never-started agent starts it (signal-with-start). Sending to a stopped agent restarts the same instance from its last committed snapshot. Sending to a paused or failed agent enqueues with QUEUED delivery, to be drained by a resume.
     * @param message The message text, appended to the agent's history as a prompt when a turn consumes it. When content is supplied, nonempty text precedes those blocks in the same user message.
     * @param opts.replyTo The ref of a message in the SENDER's own mailbox this send answers (e.g. "#3", from its attribution header). The recipient sees the two paired, and awaiters of the replied-to message resolve with this reply immediately instead of at the sender's turn end.
     * @param opts.content Ordered TEXT, IMAGE, AUDIO, or DOCUMENT user content blocks. File inputs are resolved and all content is validated before enqueueing. Pass an empty message for media-only sends.
     * @experimental
     */
    send: (message: string, opts?: AgentSendOpts) => Promise<ID>;
    /**
     * The conversation as of the last committed step: immutable, branchable, persistable.
     *
     * The seed conversation if the agent never stepped.
     *
     * Branching from it does not affect the agent.
     * @experimental
     */
    snapshot: () => LLM;
    /**
     * Computed lifecycle state; never stored.
     *
     * An agent that was never started reports IDLE: its mailbox is empty and no turn is open.
     * @experimental
     */
    state: () => Promise<AgentState>;
    /**
     * Release the agent's runtime. The tombstone (state, snapshot) stays readable for the rest of the session.
     * @param opts.kill Cancel the loop immediately instead of letting an in-flight step finish. Either way the completed steps are preserved in the snapshot.
     * @experimental
     */
    stop: (opts?: AgentStopOpts) => Promise<Agent>;
    /**
     * Block until the agent settles: IDLE, FAILED, or STOPPED. Read which from state afterwards.
     *
     * Unlike waiting for one exact state, this cannot hang merely because the agent settled in a different outcome.
     * @experimental
     */
    wait: () => Promise<Agent>;
}
/**
 * EXPERIMENTAL: Agent APIs are likely to change.
 *
 * A message delivered to an agent's mailbox.
 */
declare class AgentMessage extends BaseClient {
    private readonly _id?;
    private readonly _delivery?;
    private readonly _ref?;
    private readonly _response?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _delivery?: AgentMessageDelivery, _ref?: string, _response?: string);
    /**
     * A unique identifier for this AgentMessage.
     */
    id: () => Promise<ID>;
    /**
     * How the message conclusively landed: opened a new turn (STARTED), was absorbed into the running turn at a step boundary (STEERED), or queued behind it (QUEUED).
     *
     * Blocks until provider or native lifecycle evidence is conclusive. Once recorded, the result or cancellation error is immutable.
     * @experimental
     */
    delivery: () => Promise<AgentMessageDelivery>;
    /**
     * The message's short ref within the receiving agent's runtime, e.g. "#3".
     *
     * This is the deterministic token the recipient's attribution header shows and a reply's replyTo names — quote it when telling the recipient what to answer.
     * @experimental
     */
    ref: () => Promise<string>;
    /**
     * Block until this message is answered, and return the answer: an explicit reply (a send whose replyTo names this message), or the final reply of the turn that consumed it, whichever comes first.
     *
     * Idempotent: cancel and request the response again freely; concurrent waiters share the result.
     *
     * Fails if the agent stops before the message resolves. On a failed agent it projects the failure — but the message stays pending, so after a resume consumes it, requesting the response again returns the real reply.
     *
     * Refused when called from inside an agent turn whose wait would deadlock: turns should not block on other agents — send without awaiting, and the reply arrives as a message.
     * @experimental
     */
    response: () => Promise<string>;
}
/**
 * One workspace value with a complete path and all required dimension keys. Reading metadata does not evaluate the value. Different addresses remain distinct even if they return the same object.
 */
declare class Artifact extends BaseClient {
    private readonly _id?;
    private readonly _description?;
    private readonly _loadError?;
    private readonly _moduleName?;
    private readonly _uri?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _description?: string, _loadError?: string, _moduleName?: string, _uri?: string);
    /**
     * A unique identifier for this Artifact.
     */
    id: () => Promise<ID>;
    /**
     * The arguments accepted by the artifact field.
     */
    arguments_: () => Promise<FunctionArg[]>;
    /**
     * The description of the field that supplies this artifact.
     */
    description: () => Promise<string>;
    /**
     * The module name, collection keys, and full path key in the artifact type dimension.
     */
    dimensionKeys: () => Promise<ArtifactDimensionKey[]>;
    /**
     * The directives carried by this artifact.
     */
    directives: () => Promise<string[]>;
    /**
     * A module load failure, or an empty string if discovery succeeded.
     */
    loadError: () => Promise<string>;
    /**
     * The installed module name.
     */
    moduleName: () => Promise<string>;
    /**
     * Ordered, literal fields to follow. Workspace main targets use their shorthand.
     */
    path: () => Promise<string[]>;
    /**
     * The artifact's DAG address, such as dag://engine-dev/playground.
     * @param opts.absolute Prefix the workspace's Git address and commit: dag://<workspace>@<commit>:<path>. Fails if the artifact has no workspace, or its workspace has no Git address.
     * @param opts.dimensionKeys Include the dimension keys as a query. Without them, the address is a path selector.
     * @param opts.typeAssertion Include the artifact type in the scheme: dag+container://.
     */
    uri: (opts?: ArtifactUriOpts) => Promise<string>;
    /**
     * Evaluate the target in the workspace that supplied this artifact.
     * @param opts.arguments Field arguments as a JSON object.
     */
    value: (opts?: ArtifactValueOpts) => Node;
    /**
     * The workspace this artifact was discovered in; its address is relative to it. Null for an artifact without a workspace, such as one of an LLM's bound tool objects (see LLM.artifacts).
     */
    workspace: () => Promise<Workspace | null>;
}
declare class ArtifactDimension extends BaseClient {
    private readonly _id?;
    private readonly _collectionType?;
    private readonly _identifier?;
    private readonly _itemType?;
    private readonly _keyDescription?;
    private readonly _keyName?;
    private readonly _kind?;
    private readonly _name?;
    private readonly _qualifiedName?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _collectionType?: string, _identifier?: string, _itemType?: string, _keyDescription?: string, _keyName?: string, _kind?: ArtifactDimensionKind, _name?: string, _qualifiedName?: string);
    /**
     * A unique identifier for this ArtifactDimension.
     */
    id: () => Promise<ID>;
    /**
     * The collection type, or null for a static dimension.
     */
    collectionType: () => Promise<string>;
    /**
     * Stable identifier: collection schema path, type:TypeName for an artifact type, or module.
     */
    identifier: () => Promise<string>;
    /**
     * The collection item or artifact type name, or empty for the module dimension.
     */
    itemType: () => Promise<string>;
    /**
     * The collection key argument description, or empty for a static dimension.
     */
    keyDescription: () => Promise<string>;
    /**
     * The collection key argument name, or name for a static dimension.
     */
    keyName: () => Promise<string>;
    /**
     * How this dimension gets its keys.
     */
    kind: () => Promise<ArtifactDimensionKind>;
    /**
     * Short name used to select this dimension.
     */
    name: () => Promise<string>;
    /**
     * Qualified name used when the short name is ambiguous.
     */
    qualifiedName: () => Promise<string>;
}
declare class ArtifactDimensionKey extends BaseClient {
    private readonly _id?;
    private readonly _dimension?;
    private readonly _key?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _dimension?: string, _key?: string);
    /**
     * A unique identifier for this ArtifactDimensionKey.
     */
    id: () => Promise<ID>;
    /**
     * The dimension identifier, fixed across the workspace schema.
     */
    dimension: () => Promise<string>;
    /**
     * The dimension item's key.
     */
    key: () => Promise<string>;
}
/**
 * A schema path and its dimensions. The path can exist even when its collections have no runtime items.
 */
declare class ArtifactPath extends BaseClient {
    private readonly _id?;
    private readonly _description?;
    private readonly _loadError?;
    private readonly _moduleName?;
    private readonly _uri?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _description?: string, _loadError?: string, _moduleName?: string, _uri?: string);
    /**
     * A unique identifier for this ArtifactPath.
     */
    id: () => Promise<ID>;
    /**
     * The description of the field at this path.
     */
    description: () => Promise<string>;
    /**
     * The dimension identifiers required by this path.
     */
    dimensions: () => Promise<string[]>;
    /**
     * A module load failure for this path, or an empty string.
     */
    loadError: () => Promise<string>;
    /**
     * The installed module name.
     */
    moduleName: () => Promise<string>;
    /**
     * The DAG address of this path, without dimension keys.
     */
    uri: () => Promise<string>;
}
/**
 * A selection of workspace artifacts and their immutable collection view. Collection key filters replace collection references with subsets. Static filters change only membership. Listed types, dimensions, and keys use OR; chained filters use AND. Empty alternatives match nothing; unknown names match nothing but module load failures. Filters never change addresses or dimension identifiers. Module keys match a module's configured or kebab-case name. A module that failed to load has no schema, so its load failure stands in for whatever it would hold: a filter keeps it when the module could hold something the filter selects, and an exclusion drops it only by its own address, such as <module>/** or ?module=<module>.
 */
declare class Artifacts extends BaseClient {
    private readonly _id?;
    private readonly _uri?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _uri?: string);
    /**
     * A unique identifier for this Artifacts.
     */
    id: () => Promise<ID>;
    /**
     * Convert the selection to Changesets. Fail if any artifact is not a Changeset; a module load failure fails with its load error. Does not apply command filters.
     */
    asChangesets: () => Promise<Changeset[]>;
    /**
     * Convert the selection to Checks. Fail if any artifact is not a Check. A module load failure is a Check that fails with its load error. Does not apply command filters or run the checks.
     */
    asChecks: () => Promise<Check[]>;
    /**
     * Convert the selection to expertise without running the functions. Each must take one LLM! argument and return LLM!; any other arguments must be optional or auto-filled. Agent arguments receive the agent that owns the conversation. A module load failure fails with its load error.
     */
    asExpertise: () => Promise<Expertise[]>;
    /**
     * Convert the selection to Generators without running them. Fail if any artifact is not a Generator. Module load failures are skipped: generating can repair them.
     */
    asGenerators: () => Promise<Generator[]>;
    /**
     * Convert the selection to Services. Fail if any artifact is not a Service; a module load failure fails with its load error. Does not apply command filters or start the services.
     */
    asServices: () => Promise<Service[]>;
    /**
     * List dimensions on the selected schema paths, including empty collections. Does not read runtime values.
     */
    dimensionDefinitions: () => Promise<ArtifactDimension[]>;
    /**
     * List collection items represented in this selection for the given dimension. Preserve parent keys and remove duplicate item addresses. Does not evaluate item values.
     */
    dimensionItems: (dimension: string) => Promise<Artifact[]>;
    /**
     * List keys represented in this selection for the given dimension, sorted with no duplicates.
     */
    dimensionKeys: (dimension: string) => Promise<string[]>;
    /**
     * List dimension identifiers represented in this selection, sorted with no duplicates.
     */
    dimensions: () => Promise<string[]>;
    /**
     * Keep artifacts with any listed key in this dimension. Replace collection references with matching subsets. Skip absent keys and leave the source view unchanged.
     */
    filterDimensionKeys: (dimension: string, keys: string[]) => Artifacts;
    /**
     * Keep artifacts selected through any listed dimension.
     */
    filterDimensions: (dimensions: string[]) => Artifacts;
    /**
     * Keep artifacts with any listed directive. Does not filter by type or workspace settings.
     * @param opts.exclude Remove the matching artifacts instead.
     */
    filterDirectives: (directives: string[], opts?: ArtifactsFilterDirectivesOpts) => Artifacts;
    /**
     * Keep artifacts whose immediate parent has any listed directive. Artifacts without a parent do not match.
     * @param opts.exclude Remove the matching artifacts instead.
     */
    filterParentDirectives: (directives: string[], opts?: ArtifactsFilterParentDirectivesOpts) => Artifacts;
    /**
     * Keep artifacts whose immediate parent has any listed object type. Artifacts without a typed parent do not match.
     * @param opts.exclude Remove the matching artifacts instead.
     */
    filterParentTypes: (types: string[], opts?: ArtifactsFilterParentTypesOpts) => Artifacts;
    /**
     * Match one complete, ordered field sequence exactly.
     */
    filterPath: (path: string[]) => Artifacts;
    /**
     * Keep paths that match a glob pattern. A literal path matches exactly. Both module-qualified and workspace main paths match.
     */
    filterPathPattern: (pattern: string) => Artifacts;
    /**
     * Keep artifacts of any listed concrete GraphQL type.
     * @param opts.exclude Remove the matching artifacts instead.
     */
    filterTypes: (types: string[], opts?: ArtifactsFilterTypesOpts) => Artifacts;
    /**
     * Apply a DAG address as one filter: the chain of path, type, and dimension-key filters it encodes.
     *
     * The scheme is optional. The path may be a pattern; an empty path selects all artifacts.
     *
     * A module that failed to load keeps its load failure when the module could hold what the address selects, as with every filter (see Artifacts).
     * @param uri A DAG address: [dag[+<type>]://][<path>][?<dimension>=<key>&...]
     */
    filterUri: (uri: string) => Artifacts;
    /**
     * List artifact descriptions with their collection view. Discover collection keys without evaluating leaf values.
     */
    items: () => Promise<Artifact[]>;
    /**
     * List the modules represented in this selection without evaluating artifact values.
     */
    modules: () => Promise<Module_[]>;
    /**
     * Require exactly one artifact; fail if there are zero or multiple matches. Several matches are listed, one address per line.
     */
    one: () => Artifact;
    /**
     * List selected schema paths, including empty collections. Does not read runtime values. Applies type keys and collection presence; collection key values require items.
     * @param opts.absolute Prefix each address with the workspace's Git address and commit.
     * @param opts.typeAssertion Include the artifact type in each address scheme.
     * @param opts.dimension Project paths to the items of this collection dimension. Preserve parent dimensions and remove descendant dimensions.
     */
    pathDefinitions: (opts?: ArtifactsPathDefinitionsOpts) => Promise<ArtifactPath[]>;
    /**
     * List concrete type definitions represented in this selection, sorted by name with no duplicates.
     */
    types: () => Promise<TypeDef[]>;
    /**
     * The DAG address that selects this whole selection: filterUri(uri) selects the same set.
     */
    uri: () => Promise<string>;
    /**
     * Remove artifacts selected by a DAG address. Retain the collection view.
     */
    withoutUri: (uri: string) => Artifacts;
    /**
     * Call the provided function with current Artifacts.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Artifacts) => Artifacts) => Artifacts;
}
/**
 * A directory whose contents persist across runs.
 */
declare class CacheVolume extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this CacheVolume.
     */
    id: () => Promise<ID>;
}
/**
 * A comparison between two directories representing changes that can be applied.
 */
declare class Changeset extends BaseClient {
    private readonly _id?;
    private readonly _export?;
    private readonly _isEmpty?;
    private readonly _sync?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _export?: string, _isEmpty?: boolean, _sync?: ID);
    /**
     * A unique identifier for this Changeset.
     */
    id: () => Promise<ID>;
    /**
     * Files and directories that were added in the newer directory.
     */
    addedPaths: () => Promise<string[]>;
    /**
     * The newer/upper snapshot.
     */
    after: () => Directory;
    /**
     * Return a Git-compatible patch of the changes
     */
    asPatch: () => File;
    /**
     * The older/lower snapshot to compare against.
     */
    before: () => Directory;
    /**
     * Structured per-path diff statistics (kind and line counts) for this changeset.
     */
    diffStats: () => Promise<DiffStat[]>;
    /**
     * Applies the diff represented by this changeset to a path on the host.
     * @param path Location of the copied directory (e.g., "logs/").
     */
    export: (path: string) => Promise<string>;
    /**
     * Select changes matching the supplied glob patterns, preserving their original baseline.
     *
     * Includes additions, modifications, and deletions. Selecting only one side of a rename yields an addition or deletion.
     * @param opts.include Only include changes at paths matching these patterns. Empty includes all paths.
     * @param opts.exclude Exclude changes at paths matching these patterns.
     */
    filter: (opts?: ChangesetFilterOpts) => Changeset;
    /**
     * Returns true if the changeset is empty (i.e. there are no changes).
     */
    isEmpty: () => Promise<boolean>;
    /**
     * Return a snapshot containing only the created and modified files
     */
    layer: () => Directory;
    /**
     * Files and directories that existed before and were updated in the newer directory.
     */
    modifiedPaths: () => Promise<string[]>;
    /**
     * Files and directories that were removed. Directories are indicated by a trailing slash, and their child paths are not included.
     */
    removedPaths: () => Promise<string[]>;
    /**
     * Force evaluation in the engine.
     */
    sync: () => Promise<Changeset>;
    /**
     * Add changes to an existing changeset
     *
     * By default the operation will fail in case of conflicts, for instance a file modified in both changesets. The behavior can be adjusted using onConflict argument
     * @param changes Changes to merge into the actual changeset
     * @param opts.onConflict What to do on a merge conflict
     */
    withChangeset: (changes: Changeset, opts?: ChangesetWithChangesetOpts) => Changeset;
    /**
     * Add changes from multiple changesets using git octopus merge strategy
     *
     * This is more efficient than chaining multiple withChangeset calls when merging many changesets.
     *
     * Only FAIL and FAIL_EARLY conflict strategies are supported (octopus merge cannot use -X ours/theirs).
     * @param changes List of changesets to merge into the actual changeset
     * @param opts.onConflict What to do on a merge conflict
     */
    withChangesets: (changes: Changeset[], opts?: ChangesetWithChangesetsOpts) => Changeset;
    /**
     * Call the provided function with current Changeset.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Changeset) => Changeset) => Changeset;
}
/**
 * One deferred check. Reading pass, error, or sync runs it.
 */
declare class Check extends BaseClient {
    private readonly _id?;
    private readonly _assertion?;
    private readonly _pass?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _assertion?: string, _pass?: boolean);
    /**
     * A unique identifier for this Check.
     */
    id: () => Promise<ID>;
    /**
     * The assertion that is false when this check fails.
     */
    assertion: () => Promise<string>;
    /**
     * Run the check and return its failure, if any.
     */
    error: () => Promise<Error$1 | null>;
    /**
     * Run the check and return whether it passes.
     */
    pass: () => Promise<boolean>;
    /**
     * An optional report produced by the check.
     */
    report: () => Promise<Directory | null>;
    /**
     * Run the check and retain its result.
     */
    sync: () => Check;
    /**
     * Call the provided function with current Check.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Check) => Check) => Check;
}
/**
 * An internal persistent filesync mirror.
 */
declare class ClientFilesyncMirror extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this ClientFilesyncMirror.
     */
    id: () => Promise<ID>;
}
/**
 * Dagger Cloud configuration and state
 */
declare class Cloud extends BaseClient {
    private readonly _id?;
    private readonly _traceUrl?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _traceUrl?: string);
    /**
     * A unique identifier for this Cloud.
     */
    id: () => Promise<ID>;
    /**
     * The trace URL for the current session
     */
    traceUrl: () => Promise<string>;
}
declare class CollectionDelta extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this CollectionDelta.
     */
    id: () => Promise<ID>;
    /**
     * Current keys absent from the original collection, in current order.
     */
    addedKeys: () => Promise<string[]>;
    /**
     * Original keys absent from the current collection, in original order.
     */
    removedKeys: () => Promise<string[]>;
}
declare class CollectionTypeDef extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this CollectionTypeDef.
     */
    id: () => Promise<ID>;
    /**
     * The type of collection keys.
     */
    keyType: () => TypeDef;
    /**
     * The object type returned by get.
     */
    valueType: () => TypeDef;
}
/**
 * A command's arguments and execution settings.
 */
declare class Command extends BaseClient {
    private readonly _id?;
    private readonly _insecureRootCapabilities?;
    private readonly _privilegedNesting?;
    private readonly _workdir?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _insecureRootCapabilities?: boolean, _privilegedNesting?: boolean, _workdir?: string);
    /**
     * A unique identifier for this Command.
     */
    id: () => Promise<ID>;
    /**
     * The command arguments.
     */
    args: () => Promise<string[]>;
    /**
     * Environment variable overrides. Other variables come from the container.
     */
    env: () => Promise<EnvVariable[]>;
    /**
     * Whether the command has all root capabilities.
     */
    insecureRootCapabilities: () => Promise<boolean>;
    /**
     * Whether the command has access to Dagger.
     */
    privilegedNesting: () => Promise<boolean>;
    /**
     * Working directory override. If unset, use the container's working directory.
     */
    workdir: () => Promise<string>;
}
/**
 * An OCI-compatible container, also known as a Docker container.
 */
declare class Container extends BaseClient {
    private readonly _id?;
    private readonly _combinedOutput?;
    private readonly _envVariable?;
    private readonly _exists?;
    private readonly _exitCode?;
    private readonly _export?;
    private readonly _exportImage?;
    private readonly _imageRef?;
    private readonly _label?;
    private readonly _platform?;
    private readonly _publish?;
    private readonly _stderr?;
    private readonly _stdout?;
    private readonly _sync?;
    private readonly _up?;
    private readonly _user?;
    private readonly _workdir?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _combinedOutput?: string, _envVariable?: string, _exists?: boolean, _exitCode?: number, _export?: string, _exportImage?: Void, _imageRef?: string, _label?: string, _platform?: Platform, _publish?: string, _stderr?: string, _stdout?: string, _sync?: ID, _up?: Void, _user?: string, _workdir?: string);
    /**
     * A unique identifier for this Container.
     */
    id: () => Promise<ID>;
    /**
     * Turn the container into a Service.
     *
     * Be sure to set any exposed ports before this conversion.
     * @param opts.args Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
     *
     * If empty, the container's default command is used.
     * @param opts.useEntrypoint If the container has an entrypoint, prepend it to the args.
     * @param opts.disableDaggerInDagger Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     * @param opts.daggerInDaggerNewSession Connect Dagger clients started by the command to the current engine as new sessions, instead of as clients of the current session. Each connection gets its own session, released when that client closes.
     *
     * The command reaches the engine through DAGGER_ENGINE, so SDKs run a Dagger CLI: set _EXPERIMENTAL_DAGGER_CLI_BIN to one in the container, or let the SDK download one.
     *
     * Cannot be combined with "disableDaggerInDagger".
     * @param opts.insecureRootCapabilities Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     * @param opts.noInit If set, skip the automatic init process injected into containers by default.
     *
     * This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
     */
    asService: (opts?: ContainerAsServiceOpts) => Service;
    /**
     * Package the container state as an OCI image, and return it as a tar archive
     * @param opts.platformVariants Identifiers for other platform specific containers.
     *
     * Used for multi-platform images.
     * @param opts.forcedCompression Force each layer of the image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     * @param opts.mediaTypes Use the specified media types for the image's layers.
     *
     * Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
     * @param opts.sourceDateEpoch Clamp the image's timestamps to this time, so that rebuilding unchanged files produces identical layers.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199). File timestamps in every layer and the image's creation times that are later than this are set to it, as with BuildKit's rewrite-timestamp and SOURCE_DATE_EPOCH. Every layer is rewritten, so this costs time on each export.
     */
    asTarball: (opts?: ContainerAsTarballOpts) => File;
    /**
     * The combined buffered standard output and standard error stream of the last executed command
     *
     * Returns an error if no command was executed
     */
    combinedOutput: () => Promise<string>;
    /**
     * Return the container's default arguments.
     */
    defaultArgs: () => Promise<string[]>;
    /**
     * Retrieve a directory from the container's root filesystem
     *
     * Mounts are included.
     * @param path The path of the directory to retrieve (e.g., "./src").
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     * @param opts.resolveBrokenSymlinks Repair symlinks that directory selection would break. Resolve targets across the root filesystem and directory or file mounts. Preserve working relative links and links that already have no target. Cache, volume, tmpfs, secret, and socket mounts cause an error if selection, a resolved link, or mount target resolution needs their contents.
     */
    directory: (path: string, opts?: ContainerDirectoryOpts) => Directory;
    /**
     * Retrieves this container's configured docker healthcheck.
     */
    dockerHealthcheck: () => Promise<HealthcheckConfig | null>;
    /**
     * Return the container's OCI entrypoint.
     */
    entrypoint: () => Promise<string[]>;
    /**
     * Retrieves the value of the specified persistent environment variable.
     * @param name The name of the environment variable to retrieve (e.g., "PATH").
     */
    envVariable: (name: string) => Promise<string>;
    /**
     * Retrieves the list of persistent environment variables configured on the container.
     */
    envVariables: () => Promise<EnvVariable[]>;
    /**
     * check if a file or directory exists
     * @param path Path to check (e.g., "/file.txt").
     * @param opts.expectedType If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").
     * @param opts.doNotFollowSymlinks If specified, do not follow symlinks.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    exists: (path: string, opts?: ContainerExistsOpts) => Promise<boolean>;
    /**
     * The exit code of the last executed command
     *
     * Returns an error if no command was executed
     */
    exitCode: () => Promise<number>;
    /**
     * Configures all available GPUs on the host to be accessible to this container.
     *
     * This currently works for Nvidia devices only.
     * @deprecated Use "withGpu" instead.
     */
    experimentalWithAllGpus: () => Container;
    /**
     * Configures the provided list of devices to be accessible to this container.
     *
     * This currently works for Nvidia devices only.
     * @param devices List of devices to be accessible to this container.
     * @deprecated Use "withGpu" instead, which exposes all GPUs available on the host.
     */
    experimentalWithGpu: (devices: string[]) => Container;
    /**
     * Writes the container as an OCI tarball to the destination file path on the host.
     *
     * It can also export platform variants.
     * @param path Host's destination path (e.g., "./tarball").
     *
     * Path can be relative to the engine's workdir or absolute.
     * @param opts.platformVariants Identifiers for other platform specific containers.
     *
     * Used for multi-platform image.
     * @param opts.forcedCompression Force each layer of the exported image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     * @param opts.mediaTypes Use the specified media types for the exported image's layers.
     *
     * Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     * @param opts.sourceDateEpoch Clamp the image's timestamps to this time, so that rebuilding unchanged files produces identical layers.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199). File timestamps in every layer and the image's creation times that are later than this are set to it, as with BuildKit's rewrite-timestamp and SOURCE_DATE_EPOCH. Every layer is rewritten, so this costs time on each export.
     */
    export: (path: string, opts?: ContainerExportOpts) => Promise<string>;
    /**
     * Exports the container as an image to the host's container image store.
     * @param name Name of image to export to in the host's store
     * @param opts.platformVariants Identifiers for other platform specific containers.
     *
     * Used for multi-platform image.
     * @param opts.forcedCompression Force each layer of the exported image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     * @param opts.mediaTypes Use the specified media types for the exported image's layers.
     *
     * Defaults to OCI, which is largely compatible with most recent container runtimes, but Docker may be needed for older runtimes without OCI support.
     * @param opts.sourceDateEpoch Clamp the image's timestamps to this time, so that rebuilding unchanged files produces identical layers.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199). File timestamps in every layer and the image's creation times that are later than this are set to it, as with BuildKit's rewrite-timestamp and SOURCE_DATE_EPOCH. Every layer is rewritten, so this costs time on each export.
     */
    exportImage: (name: string, opts?: ContainerExportImageOpts) => Promise<void>;
    /**
     * Retrieves the list of exposed ports.
     *
     * This includes ports already exposed by the image, even if not explicitly added with dagger.
     */
    exposedPorts: () => Promise<Port[]>;
    /**
     * Retrieves a file at the given path.
     *
     * Mounts are included.
     * @param path The path of the file to retrieve (e.g., "./README.md").
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    file: (path: string, opts?: ContainerFileOpts) => File;
    /**
     * Download a container image, and apply it to the container state. All previous state will be lost.
     * @param address Address of the container image to download, in standard OCI ref format. Example: "registry.dagger.io/engine:latest".
     *
     * An address without a tag or digest selects the greatest stable release tag, falling back to the literal "latest" tag when no eligible release exists.
     * @param opts.version Version query used to select an image tag. The address must not contain a tag or digest.
     * @param opts.registryService Service to use as the registry endpoint for the image address.
     *
     * The service will be started only for this pull.
     * @param opts.protocol Protocol to use for registry communication.
     *
     * Defaults to "HTTPS". Use "HTTP" only for plain HTTP registries.
     * @param opts.insecureSkipTlsVerify Allow HTTPS registry communication without verifying the server certificate.
     * @param opts.noLock Ignore the workspace lockfile for this lookup.
     */
    from: (address: string, opts?: ContainerFromOpts) => Container;
    /**
     * The unique image reference which can only be retrieved immediately after the 'Container.From' call.
     */
    imageRef: () => Promise<string>;
    /**
     * Reads the container from an OCI tarball.
     * @param source File to read the container from.
     * @param opts.tag Identifies the tag to import from the archive, if the archive bundles multiple tags.
     */
    import_: (source: File, opts?: ContainerImportOpts) => Container;
    /**
     * Retrieves the value of the specified label.
     * @param name The name of the label (e.g., "org.opencontainers.artifact.created").
     */
    label: (name: string) => Promise<string>;
    /**
     * Retrieves the list of labels passed to container.
     */
    labels: () => Promise<Label[]>;
    /**
     * Returns the image layer or configuration blob with the given digest as a File.
     * @param id Digest of the layer or configuration blob (e.g. "sha256:abc123...").
     * @param opts.forcedCompression Force each layer of the image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     * @param opts.mediaTypes Media types to use for image layers. Defaults to OCI.
     */
    layer: (id: string, opts?: ContainerLayerOpts) => File;
    /**
     * Computes and returns the manifest for this container as a File.
     * @param opts.forcedCompression Force each layer of the image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     * @param opts.mediaTypes Media types to use for image layers. Defaults to OCI.
     */
    manifest: (opts?: ContainerManifestOpts) => File;
    /**
     * Retrieves the list of paths where a directory is mounted.
     */
    mounts: () => Promise<string[]>;
    /**
     * The platform this container executes and publishes as.
     */
    platform: () => Promise<Platform>;
    /**
     * Package the container state as an OCI image, and publish it to a registry
     *
     * Returns the fully qualified address of the published image, with digest
     * @param address The OCI address to publish to
     *
     * Same format as "docker push". Example: "registry.example.com/user/repo:tag"
     * @param opts.platformVariants Identifiers for other platform specific containers.
     *
     * Used for multi-platform image.
     * @param opts.forcedCompression Force each layer of the published image to use the specified compression algorithm.
     *
     * If this is unset, then if a layer already has a compressed blob in the engine's cache, that will be used (this can result in a mix of compression algorithms for different layers). If this is unset and a layer has no compressed blob in the engine's cache, then it will be compressed using Gzip.
     * @param opts.mediaTypes Use the specified media types for the published image's layers.
     *
     * Defaults to "OCI", which is compatible with most recent registries, but "Docker" may be needed for older registries without OCI support.
     * @param opts.registryService Service to use as the registry endpoint for the image address.
     *
     * The service will be started only for this push.
     * @param opts.protocol Protocol to use for registry communication.
     *
     * Defaults to "HTTPS". Use "HTTP" only for plain HTTP registries.
     * @param opts.insecureSkipTlsVerify Allow HTTPS registry communication without verifying the server certificate.
     * @param opts.sourceDateEpoch Clamp the image's timestamps to this time, so that rebuilding unchanged files produces identical layers.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199). File timestamps in every layer and the image's creation times that are later than this are set to it, as with BuildKit's rewrite-timestamp and SOURCE_DATE_EPOCH. Every layer is rewritten, so this costs time on each export.
     */
    publish: (address: string, opts?: ContainerPublishOpts) => Promise<string>;
    /**
     * Return a snapshot of the container's root filesystem. The snapshot can be modified then written back using withRootfs. Use that method for filesystem modifications.
     */
    rootfs: () => Directory;
    /**
     * Return the configured shell command. Defaults to ["sh"].
     * @param opts.batch Return the batch command instead of the interactive command.
     */
    shell: (opts?: ContainerShellOpts) => Command;
    /**
     * Return file status
     * @param path Path to check (e.g., "/file.txt").
     * @param opts.doNotFollowSymlinks If specified, do not follow symlinks.
     */
    stat: (path: string, opts?: ContainerStatOpts) => Promise<Stat | null>;
    /**
     * The buffered standard error stream of the last executed command
     *
     * Returns an error if no command was executed
     */
    stderr: () => Promise<string>;
    /**
     * The buffered standard output stream of the last executed command
     *
     * Returns an error if no command was executed
     */
    stdout: () => Promise<string>;
    /**
     * Forces evaluation of the pipeline in the engine.
     *
     * It doesn't run the default command if no exec has been set.
     */
    sync: () => Promise<Container>;
    /**
     * Opens an interactive terminal for this container using its configured default terminal command if not overridden by args (or sh as a fallback default).
     * @param opts.cmd If set, override the container's default terminal command and invoke these command arguments instead.
     * @param opts.disableDaggerInDagger Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     * @param opts.insecureRootCapabilities Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     */
    terminal: (opts?: ContainerTerminalOpts) => Container;
    /**
     * Starts a Service and creates a tunnel that forwards traffic from the caller's network to that service.
     *
     * Be sure to set any exposed ports before calling this api.
     * @param opts.random Bind each tunnel port to a random port on the host.
     * @param opts.ports List of frontend/backend port mappings to forward.
     *
     * Frontend is the port accepting traffic on the host, backend is the service port.
     * @param opts.args Command to run instead of the container's default command (e.g., ["go", "run", "main.go"]).
     *
     * If empty, the container's default command is used.
     * @param opts.useEntrypoint If the container has an entrypoint, prepend it to the args.
     * @param opts.disableDaggerInDagger Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     * @param opts.daggerInDaggerNewSession Connect Dagger clients started by the command to the current engine as new sessions, instead of as clients of the current session. Each connection gets its own session, released when that client closes.
     *
     * The command reaches the engine through DAGGER_ENGINE, so SDKs run a Dagger CLI: set _EXPERIMENTAL_DAGGER_CLI_BIN to one in the container, or let the SDK download one.
     *
     * Cannot be combined with "disableDaggerInDagger".
     * @param opts.insecureRootCapabilities Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     * @param opts.noInit If set, skip the automatic init process injected into containers by default.
     *
     * This should only be used if the user requires that their exec process be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
     */
    up: (opts?: ContainerUpOpts) => Promise<void>;
    /**
     * Retrieves the user to be set for all commands.
     */
    user: () => Promise<string>;
    /**
     * Retrieves this container plus the given OCI annotation.
     * @param name The name of the annotation.
     * @param value The value of the annotation.
     */
    withAnnotation: (name: string, value: string) => Container;
    /**
     * Configures default arguments for future commands. Like CMD in Dockerfile.
     * @param args Arguments to prepend to future executions (e.g., ["-v", "--no-cache"]).
     */
    withDefaultArgs: (args: string[]) => Container;
    /**
     * Set the default command to invoke for the container's terminal API.
     * @param args The args of the command.
     * @param opts.disableDaggerInDagger Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     * @param opts.insecureRootCapabilities Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     * @deprecated Use withShell.
     */
    withDefaultTerminalCmd: (args: string[], opts?: ContainerWithDefaultTerminalCmdOpts) => Container;
    /**
     * Return a new container snapshot, with a directory added to its filesystem
     * @param path Location of the written directory (e.g., "/tmp/directory").
     * @param source Identifier of the directory to write
     * @param opts.exclude Patterns to exclude in the written directory (e.g. ["node_modules/**", ".gitignore", ".git/"]).
     * @param opts.include Patterns to include in the written directory (e.g. ["*.go", "go.mod", "go.sum"]).
     * @param opts.gitignore Apply .gitignore rules when writing the directory.
     * @param opts.owner A user:group to set for the directory and its contents.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.inheritOwner Set the owner to the container's current user.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withDirectory: (path: string, source: Directory, opts?: ContainerWithDirectoryOpts) => Container;
    /**
     * Retrieves this container with the specificed docker healtcheck command set.
     * @param args Healthcheck command to execute. Example: ["go", "run", "main.go"].
     * @param opts.shell When true, command must be a single element, which is run using the container's shell
     * @param opts.interval Interval between running healthcheck. Example: "30s"
     * @param opts.timeout Healthcheck timeout. Example: "3s"
     * @param opts.startPeriod StartPeriod allows for failures during this initial startup period which do not count towards maximum number of retries. Example: "0s"
     * @param opts.startInterval StartInterval configures the duration between checks during the startup phase. Example: "5s"
     * @param opts.retries The maximum number of consecutive failures before the container is marked as unhealthy. Example: "3"
     */
    withDockerHealthcheck: (args: string[], opts?: ContainerWithDockerHealthcheckOpts) => Container;
    /**
     * Set an OCI-style entrypoint. It will be included in the container's OCI configuration. Note, withExec ignores the entrypoint by default.
     * @param args Arguments of the entrypoint. Example: ["go", "run"].
     * @param opts.keepDefaultArgs Don't reset the default arguments when setting the entrypoint. By default it is reset, since entrypoint and default args are often tightly coupled.
     */
    withEntrypoint: (args: string[], opts?: ContainerWithEntrypointOpts) => Container;
    /**
     * Export environment variables from an env-file to the container.
     * @param source Identifier of the envfile
     */
    withEnvFileVariables: (source: EnvFile) => Container;
    /**
     * Set a new environment variable in the container.
     * @param name Name of the environment variable (e.g., "HOST").
     * @param value Value of the environment variable. (e.g., "localhost").
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value according to the current environment variables defined in the container (e.g. "/opt/bin:$PATH").
     */
    withEnvVariable: (name: string, value: string, opts?: ContainerWithEnvVariableOpts) => Container;
    /**
     * Raise an error.
     * @param err Message of the error to raise. If empty, the error will be ignored.
     */
    withError: (err: string) => Container;
    /**
     * Execute a command in the container, and return a new snapshot of the container state after execution.
     * @param args Command to execute. Must be valid exec() arguments, not a shell command. Example: ["go", "run", "main.go"].
     *
     * To run a shell command, execute the shell and pass the shell command as argument. Example: ["sh", "-c", "ls -l | grep foo"]
     *
     * Defaults to the container's default arguments (see "defaultArgs" and "withDefaultArgs").
     * @param opts.useEntrypoint Apply the OCI entrypoint, if present, by prepending it to the args. Ignored by default.
     * @param opts.stdin Content to write to the command's standard input. Example: "Hello world")
     * @param opts.redirectStdin Redirect the command's standard input from a file in the container. Example: "./stdin.txt"
     * @param opts.redirectStdout Redirect the command's standard output to a file in the container. The redirected output is not logged. Example: "./stdout.txt"
     * @param opts.redirectStderr Redirect the command's standard error to a file in the container. The redirected output is not logged. Example: "./stderr.txt"
     * @param opts.expect Exit codes this command is allowed to exit with without error
     * @param opts.disableDaggerInDagger Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     * @param opts.daggerInDaggerNewSession Connect Dagger clients started by the command to the current engine as new sessions, instead of as clients of the current session. Each connection gets its own session, released when that client closes.
     *
     * The command reaches the engine through DAGGER_ENGINE, so SDKs run a Dagger CLI: set _EXPERIMENTAL_DAGGER_CLI_BIN to one in the container, or let the SDK download one.
     *
     * Cannot be combined with "disableDaggerInDagger".
     * @param opts.insecureRootCapabilities Execute the command with all root capabilities. Like --privileged in Docker
     *
     * DANGER: this grants the command full access to the host system. Only use when 1) you trust the command being executed and 2) you specifically need this level of access.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the args according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     * @param opts.noInit Skip the automatic init process injected into containers by default.
     *
     * Only use this if you specifically need the command to be pid 1 in the container. Otherwise it may result in unexpected behavior. If you're not sure, you don't need this.
     * @param opts.transparentHugePages Let the command, and the processes it starts, use the Linux kernel's transparent huge pages. Large-memory programs such as compilers can run faster with them, at some cost in memory.
     *
     * Without this, a command gets huge pages only for memory its own program asks huge pages for, as on a host set to "madvise". That only matters on hosts with Linux 6.18 or later whose transparent huge pages are set to "always" (/sys/kernel/mm/transparent_hugepage/enabled, or a size under /sys/kernel/mm/transparent_hugepage/hugepages-*). With the usual default settings, this changes nothing. On older kernels, commands get what the host's settings give them, whether they set this or not.
     *
     * It isn't part of the exec's cache key: an exec that differs from a cached one only in this setting reuses the cached result, whichever setting produced it.
     */
    withExec: (args: string[], opts?: ContainerWithExecOpts) => Container;
    /**
     * Expose a network port. Like EXPOSE in Dockerfile (but with healthcheck support)
     *
     * Exposed ports serve two purposes:
     *
     * - For health checks and introspection, when running services
     *
     * - For setting the EXPOSE OCI field when publishing the container
     * @param port Port number to expose. Example: 8080
     * @param opts.protocol Network protocol. Example: "tcp"
     * @param opts.description Port description. Example: "payment API endpoint"
     * @param opts.experimentalSkipHealthcheck Skip the health check when run as a service.
     */
    withExposedPort: (port: number, opts?: ContainerWithExposedPortOpts) => Container;
    /**
     * Return a container snapshot with a file added
     * @param path Path of the new file. Example: "/path/to/new-file.txt"
     * @param source File to add
     * @param opts.permissions Permissions of the new file. Example: 0600
     * @param opts.owner A user:group to set for the file.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.inheritOwner Set the owner to the container's current user.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    withFile: (path: string, source: File, opts?: ContainerWithFileOpts) => Container;
    /**
     * Retrieves this container plus the contents of the given files copied to the given path.
     * @param path Location where copied files should be placed (e.g., "/src").
     * @param sources Identifiers of the files to copy.
     * @param opts.permissions Permission given to the copied files (e.g., 0600).
     * @param opts.owner A user:group to set for the files.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.inheritOwner Set the owner to the container's current user.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    withFiles: (path: string, sources: File[], opts?: ContainerWithFilesOpts) => Container;
    /**
     * Configures all GPUs available on the host to be accessible to this container.
     *
     * This currently works with NVIDIA devices only, and requires the engine to run with GPU support enabled.
     */
    withGpu: () => Container;
    /**
     * Retrieves this container plus the given label.
     * @param name The name of the label (e.g., "org.opencontainers.artifact.created").
     * @param value The value of the label (e.g., "2023-01-01T00:00:00Z").
     */
    withLabel: (name: string, value: string) => Container;
    /**
     * Retrieves this container plus a cache volume mounted at the given path.
     * @param path Location of the cache directory (e.g., "/root/.npm").
     * @param cache Identifier of the cache volume to mount.
     * @param opts.source Identifier of the directory to use as the cache volume's root.
     * @param opts.sharing Sharing mode of the cache volume.
     * @param opts.owner A user:group to set for the mounted cache directory.
     *
     * Note that this changes the ownership of the specified mount along with the initial filesystem provided by source (if any). It does not have any effect if/when the cache has already been created.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.inheritOwner Set the owner to the container's current user.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withMountedCache: (path: string, cache: CacheVolume, opts?: ContainerWithMountedCacheOpts) => Container;
    /**
     * Retrieves this container plus a directory mounted at the given path.
     * @param path Location of the mounted directory (e.g., "/mnt/directory").
     * @param source Identifier of the mounted directory.
     * @param opts.owner A user:group to set for the mounted directory and its contents.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.inheritOwner Set the owner to the container's current user.
     * @param opts.readOnly Mount the directory read-only.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withMountedDirectory: (path: string, source: Directory, opts?: ContainerWithMountedDirectoryOpts) => Container;
    /**
     * Retrieves this container plus a file mounted at the given path.
     * @param path Location of the mounted file (e.g., "/tmp/file.txt").
     * @param source Identifier of the mounted file.
     * @param opts.owner A user or user:group to set for the mounted file.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.inheritOwner Set the owner to the container's current user.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     * @param opts.readOnly Mount the file read-only.
     */
    withMountedFile: (path: string, source: File, opts?: ContainerWithMountedFileOpts) => Container;
    /**
     * Retrieves this container plus the given files mounted read-only in a directory at the given path.
     *
     * Each file appears under the base name of its path. The directory holds nothing else, and nothing can be written to it.
     * @param path Location of the directory holding the files (e.g., "/work/lib").
     * @param sources Identifiers of the files to mount. Two files with the same base name are an error.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/lib").
     */
    withMountedFiles: (path: string, sources: File[], opts?: ContainerWithMountedFilesOpts) => Container;
    /**
     * Retrieves this container plus a secret mounted into a file at the given path.
     * @param path Location of the secret file (e.g., "/tmp/secret.txt").
     * @param source Identifier of the secret to mount.
     * @param opts.owner A user:group to set for the mounted secret.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.inheritOwner Set the owner to the container's current user.
     * @param opts.mode Permission given to the mounted secret (e.g., 0600).
     *
     * This option requires an owner to be set to be active.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withMountedSecret: (path: string, source: Secret, opts?: ContainerWithMountedSecretOpts) => Container;
    /**
     * Retrieves this container plus a temporary directory mounted at the given path. Any writes will be ephemeral to a single withExec call; they will not be persisted to subsequent withExecs.
     * @param path Location of the temporary directory (e.g., "/tmp/temp_dir").
     * @param opts.size Size of the temporary directory in bytes.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withMountedTemp: (path: string, opts?: ContainerWithMountedTempOpts) => Container;
    /**
     * Retrieves this container plus a volume mounted at the given path.
     * @param path Location of the volume mount (e.g., "/mnt/volume").
     * @param volume Identifier of the volume to mount.
     * @param opts.readOnly Mount the volume read-only.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withMountedVolume: (path: string, volume: Volume, opts?: ContainerWithMountedVolumeOpts) => Container;
    /**
     * Return a new container snapshot, with a file added to its filesystem with text content
     * @param path Path of the new file. May be relative or absolute. Example: "README.md" or "/etc/profile"
     * @param contents Contents of the new file. Example: "Hello world!"
     * @param opts.permissions Permissions of the new file. Example: 0600
     * @param opts.owner A user:group to set for the file.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.inheritOwner Set the owner to the container's current user.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    withNewFile: (path: string, contents: string, opts?: ContainerWithNewFileOpts) => Container;
    /**
     * Attach credentials for future publishing to a registry. Use in combination with publish
     * @param address The image address that needs authentication. Same format as "docker push". Example: "registry.dagger.io/dagger:latest"
     * @param username The username to authenticate with. Example: "alice"
     * @param secret The API key, password or token to authenticate to this registry
     */
    withRegistryAuth: (address: string, username: string, secret: Secret) => Container;
    /**
     * Change the container's root filesystem. The previous root filesystem will be lost.
     * @param directory The new root filesystem.
     */
    withRootfs: (directory: Directory) => Container;
    /**
     * Execute a script with the configured batch shell and return the modified container.
     * @param command Script to append to the shell command as one argument.
     * @param opts.shell Override the batch shell arguments. Example: ["bash", "-c"].
     * @param opts.disableDaggerInDagger Override whether the shell is denied Dagger API access. Omit to use the configured shell setting.
     * @param opts.insecureRootCapabilities Override whether the shell has all root capabilities.
     */
    withRun: (command: string, opts?: ContainerWithRunOpts) => Container;
    /**
     * Set a new environment variable, using a secret value
     * @param name Name of the secret variable (e.g., "API_SECRET").
     * @param secret Identifier of the secret value.
     */
    withSecretVariable: (name: string, secret: Secret) => Container;
    /**
     * Establish a runtime dependency from a container to a network service.
     *
     * The service will be started automatically when needed and detached when it is no longer needed, executing the default command if none is set.
     *
     * The service will be reachable from the container via the provided hostname alias.
     *
     * The service dependency will also convey to any files or directories produced by the container.
     * @param alias Hostname that will resolve to the target service (only accessible from within this container)
     * @param service The target service
     */
    withServiceBinding: (alias: string, service: Service) => Container;
    /**
     * Set the shell used by terminal() and withRun().
     * @param interactive Command arguments for interactive use. Example: ["sh"].
     * @param opts.batch Command arguments for batch use. The script is appended as one argument. Defaults to interactive followed by "-c".
     * @param opts.disableDaggerInDagger Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     * @param opts.insecureRootCapabilities Give the shell all root capabilities. Use only with trusted commands.
     */
    withShell: (interactive: string[], opts?: ContainerWithShellOpts) => Container;
    /**
     * Return a snapshot with a symlink
     * @param target Location of the file or directory to link to (e.g., "/existing/file").
     * @param linkName Location where the symbolic link will be created (e.g., "/new-file-link").
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    withSymlink: (target: string, linkName: string, opts?: ContainerWithSymlinkOpts) => Container;
    /**
     * Retrieves this container plus a socket forwarded to the given Unix socket path.
     * @param path Location of the forwarded Unix socket (e.g., "/tmp/socket").
     * @param source Identifier of the socket to forward.
     * @param opts.owner A user:group to set for the mounted socket.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.inheritOwner Set the owner to the container's current user.
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withUnixSocket: (path: string, source: Socket, opts?: ContainerWithUnixSocketOpts) => Container;
    /**
     * Retrieves this container with a different command user.
     * @param name The user to set (e.g., "root").
     */
    withUser: (name: string) => Container;
    /**
     * Set a new non-secret environment variable for future execs without invalidating exec cache when only its value changes.
     *
     * This is an expert-only escape hatch. If a volatile value affects observable exec results, stale cached results may be reused.
     * @param name Name of the volatile variable (e.g., "CI_RUN_ID").
     * @param value Value of the volatile variable.
     */
    withVolatileVariable: (name: string, value: string) => Container;
    /**
     * Change the container's working directory. Like WORKDIR in Dockerfile.
     * @param path The path to set as the working directory (e.g., "/app").
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withWorkdir: (path: string, opts?: ContainerWithWorkdirOpts) => Container;
    /**
     * Retrieves this container minus the given OCI annotation.
     * @param name The name of the annotation.
     */
    withoutAnnotation: (name: string) => Container;
    /**
     * Remove the container's default arguments.
     */
    withoutDefaultArgs: () => Container;
    /**
     * Return a new container snapshot, with a directory removed from its filesystem
     * @param path Location of the directory to remove (e.g., ".github/").
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withoutDirectory: (path: string, opts?: ContainerWithoutDirectoryOpts) => Container;
    /**
     * Retrieves this container without a configured docker healtcheck command.
     */
    withoutDockerHealthcheck: () => Container;
    /**
     * Reset the container's OCI entrypoint.
     * @param opts.keepDefaultArgs Don't remove the default arguments when unsetting the entrypoint.
     */
    withoutEntrypoint: (opts?: ContainerWithoutEntrypointOpts) => Container;
    /**
     * Retrieves this container minus the given environment variable.
     * @param name The name of the environment variable (e.g., "HOST").
     */
    withoutEnvVariable: (name: string) => Container;
    /**
     * Unexpose a previously exposed port.
     * @param port Port number to unexpose
     * @param opts.protocol Port protocol to unexpose
     */
    withoutExposedPort: (port: number, opts?: ContainerWithoutExposedPortOpts) => Container;
    /**
     * Retrieves this container with the file at the given path removed.
     * @param path Location of the file to remove (e.g., "/file.txt").
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    withoutFile: (path: string, opts?: ContainerWithoutFileOpts) => Container;
    /**
     * Return a new container spanshot with specified files removed
     * @param paths Paths of the files to remove. Example: ["foo.txt, "/root/.ssh/config"
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of paths according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").
     */
    withoutFiles: (paths: string[], opts?: ContainerWithoutFilesOpts) => Container;
    /**
     * Retrieves this container minus the given environment label.
     * @param name The name of the label to remove (e.g., "org.opencontainers.artifact.created").
     */
    withoutLabel: (name: string) => Container;
    /**
     * Retrieves this container after unmounting everything at the given path.
     * @param path Location of the cache directory (e.g., "/root/.npm").
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withoutMount: (path: string, opts?: ContainerWithoutMountOpts) => Container;
    /**
     * Retrieves this container without the registry authentication of a given address.
     * @param address Registry's address to remove the authentication from.
     *
     * Formatted as [host]/[user]/[repo]:[tag] (e.g. docker.io/dagger/dagger:main).
     */
    withoutRegistryAuth: (address: string) => Container;
    /**
     * Retrieves this container minus the given environment variable containing the secret.
     * @param name The name of the environment variable (e.g., "HOST").
     */
    withoutSecretVariable: (name: string) => Container;
    /**
     * Retrieves this container with a previously added Unix socket removed.
     * @param path Location of the socket to remove (e.g., "/tmp/socket").
     * @param opts.expand Replace "${VAR}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").
     */
    withoutUnixSocket: (path: string, opts?: ContainerWithoutUnixSocketOpts) => Container;
    /**
     * Retrieves this container with an unset command user.
     *
     * Should default to root.
     */
    withoutUser: () => Container;
    /**
     * Retrieves this container minus the given volatile environment variable.
     * @param name The name of the volatile environment variable (e.g., "CI_RUN_ID").
     */
    withoutVolatileVariable: (name: string) => Container;
    /**
     * Unset the container's working directory.
     *
     * Should default to "/".
     */
    withoutWorkdir: () => Container;
    /**
     * Retrieves the working directory for all commands.
     */
    workdir: () => Promise<string>;
    /**
     * Call the provided function with current Container.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Container) => Container) => Container;
}
/**
 * Reflective module API provided to functions at runtime.
 */
declare class CurrentModule extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    private readonly _sourceSubpath?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string, _sourceSubpath?: string);
    /**
     * A unique identifier for this CurrentModule.
     */
    id: () => Promise<ID>;
    /**
     * The module's context directory loaded into the engine (plus any generated code that may have been created).
     *
     * It holds the module's source code and every other file the engine loaded for the module, at their paths relative to the context root. Use sourceSubpath to locate the source code in it.
     */
    contextDirectory: () => Directory;
    /**
     * The dependencies of the module.
     */
    dependencies: () => Promise<Module_[]>;
    /**
     * The generated files and directories made on top of the module source's context directory.
     */
    generatedContextDirectory: () => Directory;
    /**
     * The name of the module being executed in
     */
    name: () => Promise<string>;
    /**
     * The names the engine gives this module's own declarations: the same as its source's ModuleSource.naming, for module runtimes, which have no ModuleSource.
     *
     * It answers for the module under its own name (ModuleSource.moduleName), by the naming rules of the version the module is named by. A workspace or toolchain that loads the module under another name namespaces its types under that name instead, which this doesn't reflect.
     * @experimental
     */
    naming: () => ModuleNaming;
    /**
     * The directory containing the module's source code loaded into the engine (plus any generated code that may have been created).
     */
    source: () => Directory;
    /**
     * The path to the directory containing the module's source code, relative to the context directory.
     */
    sourceSubpath: () => Promise<string>;
    /**
     * Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.
     * @param path Location of the directory to access (e.g., ".").
     * @param opts.exclude Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
     * @param opts.include Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
     * @param opts.gitignore Apply .gitignore filter rules inside the directory
     */
    workdir: (path: string, opts?: CurrentModuleWorkdirOpts) => Directory;
    /**
     * Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.
     * @param path Location of the file to retrieve (e.g., "README.md").
     */
    workdirFile: (path: string) => File;
}
declare class DiffStat extends BaseClient {
    private readonly _id?;
    private readonly _addedLines?;
    private readonly _kind?;
    private readonly _oldPath?;
    private readonly _path?;
    private readonly _removedLines?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _addedLines?: number, _kind?: DiffStatKind, _oldPath?: string, _path?: string, _removedLines?: number);
    /**
     * A unique identifier for this DiffStat.
     */
    id: () => Promise<ID>;
    /**
     * Number of added lines for this path.
     */
    addedLines: () => Promise<number>;
    /**
     * Type of change.
     */
    kind: () => Promise<DiffStatKind>;
    /**
     * Previous path of the file, set only for renames.
     */
    oldPath: () => Promise<string>;
    /**
     * Path of the changed file or directory.
     */
    path: () => Promise<string>;
    /**
     * Number of removed lines for this path.
     */
    removedLines: () => Promise<number>;
}
/**
 * A directory.
 */
declare class Directory extends BaseClient {
    private readonly _id?;
    private readonly _digest?;
    private readonly _exists?;
    private readonly _export?;
    private readonly _findUp?;
    private readonly _name?;
    private readonly _sync?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _digest?: string, _exists?: boolean, _export?: string, _findUp?: string, _name?: string, _sync?: ID);
    /**
     * A unique identifier for this Directory.
     */
    id: () => Promise<ID>;
    /**
     * Converts this directory to a local git repository
     */
    asGit: () => GitRepository;
    /**
     * Load the directory as a Dagger module source
     * @param opts.sourceRootPath An optional subpath of the directory which contains the module's configuration file.
     *
     * If not set, the module source code is loaded from the root of the directory.
     */
    asModule: (opts?: DirectoryAsModuleOpts) => Module_;
    /**
     * Load the directory as a Dagger module source
     * @param opts.sourceRootPath An optional subpath of the directory which contains the module's configuration file.
     *
     * If not set, the module source code is loaded from the root of the directory.
     */
    asModuleSource: (opts?: DirectoryAsModuleSourceOpts) => ModuleSource;
    /**
     * Creates a synthetic workspace from this directory.
     * @param opts.cwd Current working directory inside the workspace root. Defaults to the workspace root.
     */
    asWorkspace: (opts?: DirectoryAsWorkspaceOpts) => Workspace;
    /**
     * Return the difference between this directory and another directory, typically an older snapshot.
     *
     * The difference is encoded as a changeset, which also tracks removed files, and can be applied to other directories.
     * @param from The base directory snapshot to compare against
     */
    changes: (from: Directory) => Changeset;
    /**
     * Change the owner of the directory contents recursively.
     * @param path Path of the directory to change ownership of (e.g., "/").
     * @param owner A user:group to set for the mounted directory and its contents.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    chown: (path: string, owner: string) => Directory;
    /**
     * Return the difference between this directory and an another directory. The difference is encoded as a directory.
     * @param other The directory to compare against
     */
    diff: (other: Directory) => Directory;
    /**
     * Return the directory's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.
     */
    digest: () => Promise<string>;
    /**
     * Retrieves a directory at the given path.
     * @param path Location of the directory to retrieve. Example: "/src"
     * @param opts.resolveBrokenSymlinks Repair symlinks that directory selection would break. Copy targets from this directory into the result. Preserve working relative links and links that already have no target.
     */
    directory: (path: string, opts?: DirectoryDirectoryOpts) => Directory;
    /**
     * Use Dockerfile compatibility to build a container from this directory. Only use this function for Dockerfile compatibility. Otherwise use the native Container type directly, it is feature-complete and supports all Dockerfile features.
     * @param opts.dockerfile Path to the Dockerfile to use (e.g., "frontend.Dockerfile").
     * @param opts.platform The platform to build.
     * @param opts.buildArgs Build arguments to use in the build.
     * @param opts.target Target build stage to build.
     * @param opts.secrets Secrets to pass to the build.
     *
     * They will be mounted at /run/secrets/[secret-name].
     * @param opts.noInit If set, skip the automatic init process injected into containers created by RUN statements.
     *
     * This should only be used if the user requires that their exec processes be the pid 1 process in the container. Otherwise it may result in unexpected behavior.
     * @param opts.ssh A socket to use for SSH authentication during the build
     *
     * (e.g., for Dockerfile RUN --mount=type=ssh instructions).
     *
     * Typically obtained via host.unixSocket() pointing to the SSH_AUTH_SOCK.
     */
    dockerBuild: (opts?: DirectoryDockerBuildOpts) => Container;
    /**
     * Returns a list of files and directories at the given path.
     * @param opts.path Location of the directory to look at (e.g., "/src").
     */
    entries: (opts?: DirectoryEntriesOpts) => Promise<string[]>;
    /**
     * check if a file or directory exists
     * @param path Path to check (e.g., "/file.txt").
     * @param opts.expectedType If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").
     * @param opts.doNotFollowSymlinks If specified, do not follow symlinks.
     */
    exists: (path: string, opts?: DirectoryExistsOpts) => Promise<boolean>;
    /**
     * Writes the contents of the directory to a path on the host.
     * @param path Location of the copied directory (e.g., "logs/").
     * @param opts.wipe If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.
     */
    export: (path: string, opts?: DirectoryExportOpts) => Promise<string>;
    /**
     * Retrieve a file at the given path.
     * @param path Location of the file to retrieve (e.g., "README.md").
     */
    file: (path: string) => File;
    /**
     * Return a snapshot with some paths included or excluded
     * @param opts.exclude If set, paths matching one of these glob patterns is excluded from the new snapshot. Example: ["node_modules/", ".git*", ".env"]
     * @param opts.include If set, only paths matching one of these glob patterns is included in the new snapshot. Example: (e.g., ["app/", "package.*"]).
     * @param opts.gitignore If set, apply .gitignore rules when filtering the directory.
     */
    filter: (opts?: DirectoryFilterOpts) => Directory;
    /**
     * Search up the directory tree for a file or directory, and return its path. If no match, return null
     * @param name The name of the file or directory to search for
     * @param start The path to start the search from
     */
    findUp: (name: string, start: string) => Promise<string>;
    /**
     * Returns a list of files and directories that matche the given pattern.
     * @param pattern Pattern to match (e.g., "*.md").
     */
    glob: (pattern: string) => Promise<string[]>;
    /**
     * Returns the name of the directory.
     */
    name: () => Promise<string>;
    /**
     * Searches for content matching the given regular expression or literal string.
     *
     * Uses Rust regex syntax; escape literal ., [, ], {, }, | with backslashes.
     * @param opts.paths Directory or file paths to search
     * @param opts.globs Glob patterns to match (e.g., "*.md")
     * @param opts.pattern The text to match.
     * @param opts.literal Interpret the pattern as a literal string instead of a regular expression.
     * @param opts.multiline Enable searching across multiple lines.
     * @param opts.dotall Allow the . pattern to match newlines in multiline mode.
     * @param opts.insensitive Enable case-insensitive matching.
     * @param opts.skipIgnored Honor .gitignore, .ignore, and .rgignore files.
     * @param opts.skipHidden Skip hidden files (files starting with .).
     * @param opts.filesOnly Only return matching files, not lines and content
     * @param opts.limit Limit the number of results to return
     */
    search: (opts?: DirectorySearchOpts) => Promise<SearchResult[]>;
    /**
     * Return file status
     * @param path Path to stat (e.g., "/file.txt").
     * @param opts.doNotFollowSymlinks If specified, do not follow symlinks.
     */
    stat: (path: string, opts?: DirectoryStatOpts) => Promise<Stat | null>;
    /**
     * Force evaluation in the engine.
     */
    sync: () => Promise<Directory>;
    /**
     * Opens an interactive terminal in new container with this directory mounted inside.
     * @param opts.container If set, override the default container used for the terminal.
     * @param opts.cmd If set, override the container's default terminal command and invoke these command arguments instead.
     * @param opts.disableDaggerInDagger Disable Dagger API access for the executed command. By default, commands can connect to the current Dagger engine.
     * @param opts.insecureRootCapabilities Execute the command with all root capabilities. This is similar to running a command with "sudo" or executing "docker run" with the "--privileged" flag. Containerization does not provide any security guarantees when using this option. It should only be used when absolutely necessary and only with trusted commands.
     */
    terminal: (opts?: DirectoryTerminalOpts) => Directory;
    /**
     * Return a directory with changes from another directory applied to it.
     * @param changes Changes to apply to the directory
     */
    withChanges: (changes: Changeset) => Directory;
    /**
     * Return a snapshot with a directory added
     * @param path Location of the written directory (e.g., "/src/").
     * @param source Identifier of the directory to copy.
     * @param opts.exclude Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
     * @param opts.include Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
     * @param opts.gitignore Apply .gitignore filter rules inside the directory
     * @param opts.owner A user:group to set for the copied directory and its contents.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     * @param opts.permissions Permission given to the copied directory and contents (e.g., 0755).
     */
    withDirectory: (path: string, source: Directory, opts?: DirectoryWithDirectoryOpts) => Directory;
    /**
     * Raise an error.
     * @param err Message of the error to raise. If empty, the error will be ignored.
     */
    withError: (err: string) => Directory;
    /**
     * Retrieves this directory plus the contents of the given file copied to the given path.
     * @param path Location of the copied file (e.g., "/file.txt").
     * @param source Identifier of the file to copy.
     * @param opts.permissions Permission given to the copied file (e.g., 0600).
     * @param opts.owner A user:group to set for the copied directory and its contents.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    withFile: (path: string, source: File, opts?: DirectoryWithFileOpts) => Directory;
    /**
     * Retrieves this directory plus the contents of the given files copied to the given path.
     * @param path Location where copied files should be placed (e.g., "/src").
     * @param sources Identifiers of the files to copy.
     * @param opts.permissions Permission given to the copied files (e.g., 0600).
     */
    withFiles: (path: string, sources: File[], opts?: DirectoryWithFilesOpts) => Directory;
    /**
     * Retrieves this directory plus a new directory created at the given path.
     * @param path Location of the directory created (e.g., "/logs").
     * @param opts.permissions Permission granted to the created directory (e.g., 0777).
     */
    withNewDirectory: (path: string, opts?: DirectoryWithNewDirectoryOpts) => Directory;
    /**
     * Return a snapshot with a new file added
     * @param path Path of the new file. Example: "foo/bar.txt"
     * @param contents Contents of the new file. Example: "Hello world!"
     * @param opts.permissions Permissions of the new file. Example: 0600
     */
    withNewFile: (path: string, contents: string, opts?: DirectoryWithNewFileOpts) => Directory;
    /**
     * Retrieves this directory with the given Git-compatible patch applied.
     * @param patch Patch to apply (e.g., "diff --git a/file.txt b/file.txt\nindex 1234567..abcdef8 100644\n--- a/file.txt\n+++ b/file.txt\n@@ -1,1 +1,1 @@\n-Hello\n+World\n").
     * @param opts.onConflict How to handle hunks that no longer apply to the target content: fail (default), or apply what fits and leave git-style conflict markers where it doesn't.
     * @experimental
     */
    withPatch: (patch: string, opts?: DirectoryWithPatchOpts) => Directory;
    /**
     * Retrieves this directory with the given Git-compatible patch file applied.
     * @param patch File containing the patch to apply
     * @param opts.onConflict How to handle hunks that no longer apply to the target content: fail (default), or apply what fits and leave git-style conflict markers where it doesn't.
     * @experimental
     */
    withPatchFile: (patch: File, opts?: DirectoryWithPatchFileOpts) => Directory;
    /**
     * Retrieves this directory, declared as made for the given platform.
     *
     * Declare a result whose contents are the same whichever platform made them, such as a Go package compiled for a platform by any platform's Go toolchain. An engine of another platform that makes the same directory for the same platform then finds this one, and the other way around.
     *
     * The contents are not checked: a declared result that depends on the platform it was made on can be found where it doesn't belong.
     * @param platform The platform the directory's contents are for (e.g., "linux/arm64").
     */
    withPlatform: (platform: Platform) => Directory;
    /**
     * Return a snapshot with a symlink
     * @param target Location of the file or directory to link to (e.g., "/existing/file").
     * @param linkName Location where the symbolic link will be created (e.g., "/new-file-link").
     */
    withSymlink: (target: string, linkName: string) => Directory;
    /**
     * Retrieves this directory with all file/dir timestamps set to the given time.
     * @param timestamp Timestamp to set dir/files in.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199).
     */
    withTimestamps: (timestamp: number) => Directory;
    /**
     * Return a snapshot with a subdirectory removed
     * @param path Path of the subdirectory to remove. Example: ".github/workflows"
     */
    withoutDirectory: (path: string) => Directory;
    /**
     * Return a snapshot with a file removed
     * @param path Path of the file to remove (e.g., "/file.txt").
     */
    withoutFile: (path: string) => Directory;
    /**
     * Return a snapshot with files removed
     * @param paths Paths of the files to remove (e.g., ["/file.txt"]).
     */
    withoutFiles: (paths: string[]) => Directory;
    /**
     * Call the provided function with current Directory.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Directory) => Directory) => Directory;
}
/**
 * The Dagger engine configuration and state
 */
declare class Engine extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string);
    /**
     * A unique identifier for this Engine.
     */
    id: () => Promise<ID>;
    /**
     * The list of connected client IDs
     */
    clients: () => Promise<string[]>;
    /**
     * The local engine cache state tracked by dagql
     */
    localCache: () => EngineCache;
    /**
     * The name of the engine instance.
     */
    name: () => Promise<string>;
}
/**
 * A cache storage for the Dagger engine
 */
declare class EngineCache extends BaseClient {
    private readonly _id?;
    private readonly _maxUsedSpace?;
    private readonly _minFreeSpace?;
    private readonly _prune?;
    private readonly _reservedSpace?;
    private readonly _targetSpace?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _maxUsedSpace?: number, _minFreeSpace?: number, _prune?: Void, _reservedSpace?: number, _targetSpace?: number);
    /**
     * A unique identifier for this EngineCache.
     */
    id: () => Promise<ID>;
    /**
     * The current set of entries in the cache
     */
    entrySet: (opts?: EngineCacheEntrySetOpts) => EngineCacheEntrySet;
    /**
     * The maximum bytes to keep in the cache without pruning.
     */
    maxUsedSpace: () => Promise<number>;
    /**
     * The target amount of free disk space the garbage collector will attempt to leave.
     */
    minFreeSpace: () => Promise<number>;
    /**
     * Prune the cache of releaseable entries
     * @param opts.useDefaultPolicy Use enabled engine-wide default disk and structural policies. If no default disk policy is enabled, the disk stage falls back to pruning all releasable disk-cache entries. If false, explicit options select stages; with no options, all releasable disk-cache entries are pruned.
     * @param opts.maxUsedSpace Override the maximum disk space to keep before pruning (e.g. "200GB" or "80%").
     * @param opts.reservedSpace Override the minimum disk space to retain during pruning (e.g. "500GB" or "10%").
     * @param opts.minFreeSpace Override the minimum free disk space target during pruning (e.g. "20GB" or "20%").
     * @param opts.targetSpace Override the target disk space to keep after pruning (e.g. "200GB" or "50%").
     * @param opts.maxEstimatedBytes Override the maximum structural metadata estimate in absolute bytes. Explicit values must be positive; the configured/default value is used when omitted.
     * @param opts.targetEstimatedBytes Override the structural metadata estimate to target in absolute bytes. Explicit values must be positive and lower than the resolved maximum; the configured/default value is used when omitted.
     */
    prune: (opts?: EngineCachePruneOpts) => Promise<void>;
    /**
     * The minimum amount of disk space this policy is guaranteed to retain.
     */
    reservedSpace: () => Promise<number>;
    /**
     * The target number of bytes to keep when pruning.
     */
    targetSpace: () => Promise<number>;
}
/**
 * An individual cache entry in a cache entry set
 */
declare class EngineCacheEntry extends BaseClient {
    private readonly _id?;
    private readonly _activelyUsed?;
    private readonly _createdTimeUnixNano?;
    private readonly _dagqlCall?;
    private readonly _description?;
    private readonly _diskSpaceBytes?;
    private readonly _mostRecentUseTimeUnixNano?;
    private readonly _recordType?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _activelyUsed?: boolean, _createdTimeUnixNano?: number, _dagqlCall?: string, _description?: string, _diskSpaceBytes?: number, _mostRecentUseTimeUnixNano?: number, _recordType?: string);
    /**
     * A unique identifier for this EngineCacheEntry.
     */
    id: () => Promise<ID>;
    /**
     * Whether the cache entry is actively being used.
     */
    activelyUsed: () => Promise<boolean>;
    /**
     * The time the cache entry was created, in Unix nanoseconds.
     */
    createdTimeUnixNano: () => Promise<number>;
    /**
     * The DagQL call that produced this cache entry.
     */
    dagqlCall: () => Promise<string>;
    /**
     * The description of the cache entry.
     */
    description: () => Promise<string>;
    /**
     * The disk space used by the cache entry.
     */
    diskSpaceBytes: () => Promise<number>;
    /**
     * The most recent time the cache entry was used, in Unix nanoseconds.
     */
    mostRecentUseTimeUnixNano: () => Promise<number>;
    /**
     * The type of the cache record (e.g. regular, internal, frontend, source.local, source.git.checkout, exec.cachemount).
     */
    recordType: () => Promise<string>;
    /**
     * The storage record types represented by this cache entry.
     */
    recordTypes: () => Promise<string[]>;
}
/**
 * A set of cache entries returned by a query to a cache
 */
declare class EngineCacheEntrySet extends BaseClient {
    private readonly _id?;
    private readonly _diskSpaceBytes?;
    private readonly _entryCount?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _diskSpaceBytes?: number, _entryCount?: number);
    /**
     * A unique identifier for this EngineCacheEntrySet.
     */
    id: () => Promise<ID>;
    /**
     * The total disk space used by the cache entries in this set.
     */
    diskSpaceBytes: () => Promise<number>;
    /**
     * The list of individual cache entries in the set
     */
    entries: () => Promise<EngineCacheEntry[]>;
    /**
     * The number of cache entries in this set.
     */
    entryCount: () => Promise<number>;
}
/**
 * A definition of a custom enum defined in a Module.
 */
declare class EnumTypeDef extends BaseClient {
    private readonly _id?;
    private readonly _description?;
    private readonly _name?;
    private readonly _sourceModuleName?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _description?: string, _name?: string, _sourceModuleName?: string);
    /**
     * A unique identifier for this EnumTypeDef.
     */
    id: () => Promise<ID>;
    /**
     * A doc string for the enum, if any.
     */
    description: () => Promise<string>;
    /**
     * The members of the enum.
     */
    members: () => Promise<EnumValueTypeDef[]>;
    /**
     * The name of the enum.
     */
    name: () => Promise<string>;
    /**
     * The location of this enum declaration.
     */
    sourceMap: () => Promise<SourceMap | null>;
    /**
     * If this EnumTypeDef is associated with a Module, the name of the module. Unset otherwise.
     */
    sourceModuleName: () => Promise<string>;
    /**
     * The members of the enum.
     * @deprecated use members instead
     */
    values: () => Promise<EnumValueTypeDef[]>;
}
/**
 * A definition of a value in a custom enum defined in a Module.
 */
declare class EnumValueTypeDef extends BaseClient {
    private readonly _id?;
    private readonly _deprecated?;
    private readonly _description?;
    private readonly _name?;
    private readonly _value?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _deprecated?: string, _description?: string, _name?: string, _value?: string);
    /**
     * A unique identifier for this EnumValueTypeDef.
     */
    id: () => Promise<ID>;
    /**
     * The reason this enum member is deprecated, if any.
     */
    deprecated: () => Promise<string>;
    /**
     * A doc string for the enum member, if any.
     */
    description: () => Promise<string>;
    /**
     * The name of the enum member.
     */
    name: () => Promise<string>;
    /**
     * The location of this enum member declaration.
     */
    sourceMap: () => Promise<SourceMap | null>;
    /**
     * The value of the enum member
     */
    value: () => Promise<string>;
}
/**
 * A collection of environment variables.
 */
declare class EnvFile extends BaseClient {
    private readonly _id?;
    private readonly _exists?;
    private readonly _get?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _exists?: boolean, _get?: string);
    /**
     * A unique identifier for this EnvFile.
     */
    id: () => Promise<ID>;
    /**
     * Return as a file
     */
    asFile: () => File;
    /**
     * Check if a variable exists
     * @param name Variable name
     */
    exists: (name: string) => Promise<boolean>;
    /**
     * Lookup a variable (last occurrence wins) and return its value, or an empty string
     * @param name Variable name
     * @param opts.raw Return the value exactly as written to the file. No quote removal or variable expansion
     */
    get: (name: string, opts?: EnvFileGetOpts) => Promise<string>;
    /**
     * Filters variables by prefix and removes the pref from keys. Variables without the prefix are excluded. For example, with the prefix "MY_APP_" and variables: MY_APP_TOKEN=topsecret MY_APP_NAME=hello FOO=bar the resulting environment will contain: TOKEN=topsecret NAME=hello
     * @param prefix The prefix to filter by
     */
    namespace_: (prefix: string) => EnvFile;
    /**
     * Return all variables
     * @param opts.raw Return values exactly as written to the file. No quote removal or variable expansion
     */
    variables: (opts?: EnvFileVariablesOpts) => Promise<EnvVariable[]>;
    /**
     * Add a variable
     * @param name Variable name
     * @param value Variable value
     */
    withVariable: (name: string, value: string) => EnvFile;
    /**
     * Remove all occurrences of the named variable
     * @param name Variable name
     */
    withoutVariable: (name: string) => EnvFile;
    /**
     * Call the provided function with current EnvFile.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: EnvFile) => EnvFile) => EnvFile;
}
/**
 * An environment variable name and value.
 */
declare class EnvVariable extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    private readonly _value?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string, _value?: string);
    /**
     * A unique identifier for this EnvVariable.
     */
    id: () => Promise<ID>;
    /**
     * The environment variable name.
     */
    name: () => Promise<string>;
    /**
     * The environment variable value.
     */
    value: () => Promise<string>;
}
declare class Error$1 extends BaseClient {
    private readonly _id?;
    private readonly _message?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _message?: string);
    /**
     * A unique identifier for this Error.
     */
    id: () => Promise<ID>;
    /**
     * A description of the error.
     */
    message: () => Promise<string>;
    /**
     * The extensions of the error.
     */
    values: () => Promise<ErrorValue[]>;
    /**
     * Add a value to the error.
     * @param name The name of the value.
     * @param value The value to store on the error.
     */
    withValue: (name: string, value: JSON) => Error$1;
    /**
     * Call the provided function with current Error.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Error$1) => Error$1) => Error$1;
}
declare class ErrorValue extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    private readonly _value?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string, _value?: JSON);
    /**
     * A unique identifier for this ErrorValue.
     */
    id: () => Promise<ID>;
    /**
     * The name of the value.
     */
    name: () => Promise<string>;
    /**
     * The value.
     */
    value: () => Promise<JSON>;
}
/**
 * A function that can modify a conversation.
 */
declare class Expertise extends BaseClient {
    private readonly _id?;
    private readonly _description?;
    private readonly _name?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _description?: string, _name?: string);
    /**
     * A unique identifier for this Expertise.
     */
    id: () => Promise<ID>;
    /**
     * The artifact this expertise runs. Look it up again by URI to pick up module changes.
     */
    artifact: () => Artifact;
    /**
     * The function's description.
     */
    description: () => Promise<string>;
    /**
     * The function's name.
     */
    name: () => Promise<string>;
    /**
     * The module that defines the function.
     */
    originalModule: () => Module_;
    /**
     * The function's path within its module.
     */
    path: () => Promise<string[]>;
}
/**
 * An object that can be exported to the host.
 *
 * Calling export writes the object to a path on the host filesystem and returns the path that was written.
 */
interface Exportable {
    id(): Promise<ID>;
    export(path: string): Promise<string>;
}
declare class _ExportableClient extends BaseClient {
    private readonly _id?;
    private readonly _export?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _export?: string);
    id: () => Promise<ID>;
    export: (path: string) => Promise<string>;
}
/**
 * A definition of a field on a custom object defined in a Module.
 *
 * A field on an object has a static value, as opposed to a function on an object whose value is computed by invoking code (and can accept arguments).
 */
declare class FieldTypeDef extends BaseClient {
    private readonly _id?;
    private readonly _deprecated?;
    private readonly _description?;
    private readonly _name?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _deprecated?: string, _description?: string, _name?: string);
    /**
     * A unique identifier for this FieldTypeDef.
     */
    id: () => Promise<ID>;
    /**
     * The reason this enum member is deprecated, if any.
     */
    deprecated: () => Promise<string>;
    /**
     * A doc string for the field, if any.
     */
    description: () => Promise<string>;
    /**
     * The name of the field in lowerCamelCase format.
     */
    name: () => Promise<string>;
    /**
     * The location of this field declaration.
     */
    sourceMap: () => Promise<SourceMap | null>;
    /**
     * The type of the field.
     */
    typeDef: () => TypeDef;
}
/**
 * A file.
 */
declare class File extends BaseClient {
    private readonly _id?;
    private readonly _contents?;
    private readonly _digest?;
    private readonly _export?;
    private readonly _name?;
    private readonly _size?;
    private readonly _sync?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _contents?: string, _digest?: string, _export?: string, _name?: string, _size?: number, _sync?: ID);
    /**
     * A unique identifier for this File.
     */
    id: () => Promise<ID>;
    /**
     * Parse as an env file
     * @param opts.expand Replace "${VAR}" or "$VAR" with the value of other vars
     */
    asEnvFile: (opts?: FileAsEnvFileOpts) => EnvFile;
    /**
     * Interpret this file as a Git bundle by lazily parsing its header.
     */
    asGitBundle: () => GitBundle;
    /**
     * Parse the file contents as JSON.
     */
    asJson: () => JSONValue;
    /**
     * Change the owner of the file recursively.
     * @param owner A user:group to set for the file.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    chown: (owner: string) => File;
    /**
     * Retrieves the contents of the file.
     * @param opts.offsetLines Start reading after this line
     * @param opts.limitLines Maximum number of lines to read
     */
    contents: (opts?: FileContentsOpts) => Promise<string>;
    /**
     * Return the file's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.
     * @param opts.excludeMetadata If true, exclude metadata from the digest.
     */
    digest: (opts?: FileDigestOpts) => Promise<string>;
    /**
     * Writes the file to a file path on the host.
     * @param path Location of the written directory (e.g., "output.txt").
     * @param opts.allowParentDirPath If allowParentDirPath is true, the path argument can be a directory path, in which case the file will be created in that directory.
     */
    export: (path: string, opts?: FileExportOpts) => Promise<string>;
    /**
     * Extracts this archive file into a directory.
     *
     * The format is detected from the file's contents: zip, or tar, either uncompressed or compressed with gzip, bzip2, xz or zstd.
     *
     * An entry whose path would leave the directory is an error.
     * @param opts.stripComponents Remove this many leading elements from each entry's path, as tar --strip-components does.
     *
     * Entries with no more elements than this are skipped.
     * @param opts.include If set, only entries matching one of these glob patterns, or in a directory that does, are extracted (e.g., ["src/", "*.go"]).
     *
     * Patterns match entry paths after stripComponents is applied.
     * @param opts.exclude If set, entries matching one of these glob patterns, or in a directory that does, are not extracted (e.g., ["**\/testdata", "**\/*_test.go"]).
     *
     * Patterns match entry paths after stripComponents is applied.
     */
    extract: (opts?: FileExtractOpts) => Directory;
    /**
     * Retrieves the name of the file.
     */
    name: () => Promise<string>;
    /**
     * Searches for content matching the given regular expression or literal string.
     *
     * Uses Rust regex syntax; escape literal ., [, ], {, }, | with backslashes.
     * @param pattern The text to match.
     * @param opts.literal Interpret the pattern as a literal string instead of a regular expression.
     * @param opts.multiline Enable searching across multiple lines.
     * @param opts.dotall Allow the . pattern to match newlines in multiline mode.
     * @param opts.insensitive Enable case-insensitive matching.
     * @param opts.skipIgnored Honor .gitignore, .ignore, and .rgignore files.
     * @param opts.skipHidden Skip hidden files (files starting with .).
     * @param opts.filesOnly Only return matching files, not lines and content
     * @param opts.limit Limit the number of results to return
     */
    search: (pattern: string, opts?: FileSearchOpts) => Promise<SearchResult[]>;
    /**
     * Retrieves the size of the file, in bytes.
     */
    size: () => Promise<number>;
    /**
     * Return file status
     */
    stat: () => Promise<Stat | null>;
    /**
     * Force evaluation in the engine.
     * @param opts.keyByContent Also key the file by its content, so that what consumes it is cached across any two recipes that produce the same file. This hashes the file unless it already has a content key.
     */
    sync: (opts?: FileSyncOpts) => Promise<File>;
    /**
     * Retrieves this file with its name set to the given name.
     * @param name Name to set file to.
     */
    withName: (name: string) => File;
    /**
     * Retrieves the file with content replaced with the given text.
     *
     * If 'all' is true, all occurrences of the pattern will be replaced.
     *
     * If 'firstAfter' is specified, only the first match starting at the specified line will be replaced.
     *
     * If neither are specified, and there are multiple matches for the pattern, this will error.
     *
     * If there are no matches for the pattern, this will error.
     * @param search The text to match.
     * @param replacement The text to match.
     * @param opts.all Replace all occurrences of the pattern.
     * @param opts.firstFrom Replace the first match starting from the specified line.
     */
    withReplaced: (search: string, replacement: string, opts?: FileWithReplacedOpts) => File;
    /**
     * Retrieves this file with its created/modified timestamps set to the given time.
     * @param timestamp Timestamp to set dir/files in.
     *
     * Formatted in seconds following Unix epoch (e.g., 1672531199).
     */
    withTimestamps: (timestamp: number) => File;
    /**
     * Call the provided function with current File.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: File) => File) => File;
}
/**
 * Function represents a resolver provided by a Module.
 *
 * A function always evaluates against a parent object and is given a set of named arguments.
 */
declare class Function_ extends BaseClient {
    private readonly _id?;
    private readonly _deprecated?;
    private readonly _description?;
    private readonly _name?;
    private readonly _sourceModuleName?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _deprecated?: string, _description?: string, _name?: string, _sourceModuleName?: string);
    /**
     * A unique identifier for this Function.
     */
    id: () => Promise<ID>;
    /**
     * Arguments accepted by the function, if any.
     */
    args: () => Promise<FunctionArg[]>;
    /**
     * The reason this function is deprecated, if any.
     */
    deprecated: () => Promise<string>;
    /**
     * A doc string for the function, if any.
     */
    description: () => Promise<string>;
    /**
     * The name of the function.
     */
    name: () => Promise<string>;
    /**
     * The type returned by the function.
     */
    returnType: () => TypeDef;
    /**
     * The location of this function declaration.
     */
    sourceMap: () => Promise<SourceMap | null>;
    /**
     * If this function is provided by a module, the name of the module. Unset otherwise.
     */
    sourceModuleName: () => Promise<string>;
    /**
     * Returns the function with a flag indicating it is a source of expertise.
     * @experimental
     */
    withAgent: () => Function_;
    /**
     * Returns the function with the provided argument
     * @param name The name of the argument
     * @param typeDef The type of the argument
     * @param opts.description A doc string for the argument, if any
     * @param opts.defaultValue A default value to use for this argument if not explicitly set by the caller, if any
     * @param opts.defaultPath If the argument is a Directory or File type, default to load path from context directory, relative to root directory.
     * @param opts.ignore Patterns to ignore when loading the contextual argument value.
     * @param opts.sourceMap The source map for the argument definition.
     * @param opts.deprecated If deprecated, the reason or migration path.
     */
    withArg: (name: string, typeDef: TypeDef, opts?: FunctionWithArgOpts) => Function_;
    /**
     * Returns the function updated to use the provided cache policy.
     * @param policy The cache policy to use.
     * @param opts.timeToLive The TTL for the cache policy, if applicable. Provided as a duration string, e.g. "5m", "1h30s".
     */
    withCachePolicy: (policy: FunctionCachePolicy, opts?: FunctionWithCachePolicyOpts) => Function_;
    /**
     * Returns the function with a flag indicating it's a check.
     */
    withCheck: () => Function_;
    /**
     * Returns the function with the provided deprecation reason.
     * @param opts.reason Reason or migration path describing the deprecation.
     */
    withDeprecated: (opts?: FunctionWithDeprecatedOpts) => Function_;
    /**
     * Returns the function with the given doc string.
     * @param description The doc string to set.
     */
    withDescription: (description: string) => Function_;
    /**
     * Returns the function with a flag indicating it's a generator.
     */
    withGenerator: () => Function_;
    /**
     * Returns the function with the given source map.
     * @param sourceMap The source map for the function definition.
     */
    withSourceMap: (sourceMap: SourceMap) => Function_;
    /**
     * Returns the function with a flag indicating it returns a service for dagger up.
     */
    withUp: () => Function_;
    /**
     * Call the provided function with current Function.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Function_) => Function_) => Function_;
}
/**
 * An argument accepted by a function.
 *
 * This is a specification for an argument at function definition time, not an argument passed at function call time.
 */
declare class FunctionArg extends BaseClient {
    private readonly _id?;
    private readonly _defaultAddress?;
    private readonly _defaultPath?;
    private readonly _defaultValue?;
    private readonly _deprecated?;
    private readonly _description?;
    private readonly _name?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _defaultAddress?: string, _defaultPath?: string, _defaultValue?: JSON, _deprecated?: string, _description?: string, _name?: string);
    /**
     * A unique identifier for this FunctionArg.
     */
    id: () => Promise<ID>;
    /**
     * Only applies to arguments of type Container. If the argument is not set, load it from the given address (e.g. alpine:latest)
     */
    defaultAddress: () => Promise<string>;
    /**
     * Only applies to arguments of type File or Directory. If the argument is not set, load it from the given path in the context directory
     */
    defaultPath: () => Promise<string>;
    /**
     * A default value to use for this argument when not explicitly set by the caller, if any.
     */
    defaultValue: () => Promise<JSON>;
    /**
     * The reason this function is deprecated, if any.
     */
    deprecated: () => Promise<string>;
    /**
     * A doc string for the argument, if any.
     */
    description: () => Promise<string>;
    /**
     * Only applies to arguments of type Directory. The ignore patterns are applied to the input directory, and matching entries are filtered out, in a cache-efficient manner.
     */
    ignore: () => Promise<string[]>;
    /**
     * The name of the argument in lowerCamelCase format.
     */
    name: () => Promise<string>;
    /**
     * The location of this arg declaration.
     */
    sourceMap: () => Promise<SourceMap | null>;
    /**
     * The type of the argument.
     */
    typeDef: () => TypeDef;
}
/**
 * An active function call.
 */
declare class FunctionCall extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    private readonly _parent?;
    private readonly _parentName?;
    private readonly _returnError?;
    private readonly _returnValue?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string, _parent?: JSON, _parentName?: string, _returnError?: Void, _returnValue?: Void);
    /**
     * A unique identifier for this FunctionCall.
     */
    id: () => Promise<ID>;
    /**
     * The argument values the function is being invoked with.
     */
    inputArgs: () => Promise<FunctionCallArgValue[]>;
    /**
     * The name of the function being called.
     */
    name: () => Promise<string>;
    /**
     * The value of the parent object of the function being called. If the function is top-level to the module, this is always an empty object.
     */
    parent: () => Promise<JSON>;
    /**
     * The name of the parent object of the function being called. If the function is top-level to the module, this is the name of the module.
     */
    parentName: () => Promise<string>;
    /**
     * Return an error from the function.
     * @param error The error to return.
     */
    returnError: (error: Error$1) => Promise<void>;
    /**
     * Set the return value of the function call to the provided value.
     * @param value JSON serialization of the return value.
     */
    returnValue: (value: JSON) => Promise<void>;
}
/**
 * A value passed as a named argument to a function call.
 */
declare class FunctionCallArgValue extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    private readonly _value?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string, _value?: JSON);
    /**
     * A unique identifier for this FunctionCallArgValue.
     */
    id: () => Promise<ID>;
    /**
     * The name of the argument.
     */
    name: () => Promise<string>;
    /**
     * The value of the argument represented as a JSON serialized string.
     */
    value: () => Promise<JSON>;
}
/**
 * The result of running an SDK's codegen.
 */
declare class GeneratedCode extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this GeneratedCode.
     */
    id: () => Promise<ID>;
    /**
     * The directory containing the generated code.
     */
    code: () => Directory;
    /**
     * List of paths to mark generated in version control (i.e. .gitattributes).
     */
    vcsGeneratedPaths: () => Promise<string[]>;
    /**
     * List of paths to ignore in version control (i.e. .gitignore).
     */
    vcsIgnoredPaths: () => Promise<string[]>;
    /**
     * Set the list of paths to mark generated in version control.
     */
    withVcsGeneratedPaths: (paths: string[]) => GeneratedCode;
    /**
     * Set the list of paths to ignore in version control.
     */
    withVcsIgnoredPaths: (paths: string[]) => GeneratedCode;
    /**
     * Call the provided function with current GeneratedCode.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: GeneratedCode) => GeneratedCode) => GeneratedCode;
}
/**
 * A generation function and its staleness check. Reading changeset runs the function.
 */
declare class Generator extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this Generator.
     */
    id: () => Promise<ID>;
    /**
     * Run the generator and return its changes.
     */
    changeset: () => Changeset;
    /**
     * A check that passes when this generator would produce no changes.
     */
    stale: () => Check;
    /**
     * Run the generator and retain its result.
     */
    sync: () => Generator;
    /**
     * Call the provided function with current Generator.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Generator) => Generator) => Generator;
}
/**
 * A Git bundle: a self-describing container of refs and the objects needed to reconstruct them, optionally rooted at prerequisite commits.
 */
declare class GitBundle extends BaseClient {
    private readonly _id?;
    private readonly _objectFormat?;
    private readonly _version?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _objectFormat?: string, _version?: number);
    /**
     * A unique identifier for this GitBundle.
     */
    id: () => Promise<ID>;
    /**
     * Return the bundle bytes as a File.
     */
    asFile: () => File;
    /**
     * Object format capability: sha1 or sha256.
     */
    objectFormat: () => Promise<string>;
    /**
     * Commits that must already exist wherever this bundle is applied.
     */
    prerequisiteShas: () => Promise<string[]>;
    /**
     * Refs advertised by the bundle and the object IDs they resolve to.
     */
    refs: () => Promise<GitBundleRef[]>;
    /**
     * Perform full structural verification of the bundle and error if it is malformed.
     */
    validate: () => GitBundle;
    /**
     * Bundle format version (2 or 3).
     */
    version: () => Promise<number>;
    /**
     * Call the provided function with current GitBundle.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: GitBundle) => GitBundle) => GitBundle;
}
/**
 * A ref advertised by a Git bundle.
 */
declare class GitBundleRef extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    private readonly _sha?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string, _sha?: string);
    /**
     * A unique identifier for this GitBundleRef.
     */
    id: () => Promise<ID>;
    /**
     * The advertised ref name.
     */
    name: () => Promise<string>;
    /**
     * The object ID the advertised ref resolves to.
     */
    sha: () => Promise<string>;
}
/**
 * An immutable git commit.
 */
declare class GitCommit extends BaseClient {
    private readonly _id?;
    private readonly _authorEmail?;
    private readonly _authorName?;
    private readonly _authoredDate?;
    private readonly _committedDate?;
    private readonly _committerEmail?;
    private readonly _committerName?;
    private readonly _message?;
    private readonly _messageBody?;
    private readonly _messageHeadline?;
    private readonly _sha?;
    private readonly _shortSha?;
    private readonly _treeSha?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _authorEmail?: string, _authorName?: string, _authoredDate?: string, _committedDate?: string, _committerEmail?: string, _committerName?: string, _message?: string, _messageBody?: string, _messageHeadline?: string, _sha?: string, _shortSha?: string, _treeSha?: string);
    /**
     * A unique identifier for this GitCommit.
     */
    id: () => Promise<ID>;
    /**
     * The latest semver release tag reachable from this commit.
     * @param opts.includePreRelease Include pre-release tags when choosing the latest tag.
     */
    ancestorReleaseTag: (opts?: GitCommitAncestorReleaseTagOpts) => Promise<GitRef | null>;
    /**
     * Git author email.
     */
    authorEmail: () => Promise<string>;
    /**
     * Git author name.
     */
    authorName: () => Promise<string>;
    /**
     * Git author date, in RFC3339 format.
     */
    authoredDate: () => Promise<string>;
    /**
     * Returns the changes from the first parent to this commit, excluding Git metadata.
     *
     * Root commits are compared with an empty tree. Merge commits are compared with their first parent, not a merge base.
     * @param opts.against Use this commit as the comparison base instead of the first parent. The comparison commit may belong to an unrelated history or repository.
     */
    changes: (opts?: GitCommitChangesOpts) => Changeset;
    /**
     * Git committer date, in RFC3339 format.
     */
    committedDate: () => Promise<string>;
    /**
     * Git committer email.
     */
    committerEmail: () => Promise<string>;
    /**
     * Git committer name.
     */
    committerName: () => Promise<string>;
    /**
     * Full commit message.
     */
    message: () => Promise<string>;
    /**
     * Commit message body, excluding the headline.
     */
    messageBody: () => Promise<string>;
    /**
     * First line of the commit message.
     */
    messageHeadline: () => Promise<string>;
    /**
     * Parent commit SHAs.
     */
    parentShas: () => Promise<string[]>;
    /**
     * The latest semver release tag that points directly at this commit.
     * @param opts.includePreRelease Include pre-release tags when choosing the latest tag.
     */
    releaseTag: (opts?: GitCommitReleaseTagOpts) => Promise<GitRef | null>;
    /**
     * The full commit SHA.
     */
    sha: () => Promise<string>;
    /**
     * The abbreviated commit SHA.
     */
    shortSha: () => Promise<string>;
    /**
     * The filesystem tree at this commit.
     * @param opts.discardGitDir Set to true to discard .git directory.
     * @param opts.depth The depth of the tree to fetch.
     * @param opts.includeTags Set to true to populate tag refs in the local checkout .git.
     */
    tree: (opts?: GitCommitTreeOpts) => Directory;
    /**
     * The ID of the commit's Git tree object.
     *
     * Commits with identical trees have identical contents and file modes.
     */
    treeSha: () => Promise<string>;
}
/**
 * A receipt for a completed Git push. Reading or replaying the receipt does not push again.
 */
declare class GitPushResult extends BaseClient {
    private readonly _id?;
    private readonly _disposition?;
    private readonly _previousSha?;
    private readonly _ref?;
    private readonly _sha?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _disposition?: GitPushDisposition, _previousSha?: string, _ref?: string, _sha?: string);
    /**
     * A unique identifier for this GitPushResult.
     */
    id: () => Promise<ID>;
    /**
     * How the remote ref was updated.
     */
    disposition: () => Promise<GitPushDisposition>;
    /**
     * The previous remote object ID; empty when the ref was created.
     */
    previousSha: () => Promise<string>;
    /**
     * The fully qualified remote ref.
     */
    ref: () => Promise<string>;
    /**
     * The object ID pushed to the remote.
     */
    sha: () => Promise<string>;
}
/**
 * A git ref (tag, branch, or commit).
 */
declare class GitRef extends BaseClient {
    private readonly _id?;
    private readonly _commit?;
    private readonly _commitSha?;
    private readonly _contains?;
    private readonly _name?;
    private readonly _ref?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _commit?: string, _commitSha?: string, _contains?: boolean, _name?: string, _ref?: string);
    /**
     * A unique identifier for this GitRef.
     */
    id: () => Promise<ID>;
    /**
     * Return this ref's repository with HEAD pinned to the selected commit.
     *
     * Preserves the original repository backend, connection information, and other refs. Does not modify a branch or checkout, or prune history.
     */
    asRepository: () => GitRepository;
    /**
     * Creates a synthetic workspace from this git ref.
     * @param opts.cwd Current working directory inside the workspace root. Defaults to the workspace root.
     */
    asWorkspace: (opts?: GitRefAsWorkspaceOpts) => Workspace;
    /**
     * The resolved commit id at this ref.
     * @deprecated Use "commitSha" instead.
     */
    commit: () => Promise<string>;
    /**
     * The resolved commit SHA at this ref.
     */
    commitSha: () => Promise<string>;
    /**
     * Find the best common ancestor between this ref and another ref.
     * @param other The other ref to compare against.
     */
    commonAncestor: (other: GitRef) => GitRef;
    /**
     * Return true when the other ref's commit equals this commit or is an ancestor of it.
     *
     * Compares commit history across branches, tags and detached refs. Incomplete or unavailable history is an error.
     * @param other The ref whose commit to look for in this ref's history.
     */
    contains: (other: GitRef) => Promise<boolean>;
    /**
     * Commits reachable from this ref, newest first, starting with the commit this ref resolves to.
     * @param opts.limit Maximum number of commits to return.
     * @param opts.paths Only include commits touching these paths, relative to the root of the repository.
     * @param opts.base Exclude commits reachable from this ref, i.e. only list commits added on top of it.
     */
    log: (opts?: GitRefLogOpts) => Promise<GitCommit[]>;
    /**
     * The resolved name of this ref.
     */
    name: () => Promise<string>;
    /**
     * Push this ref's commit and history to a remote repository using the destination's credentials.
     *
     * The source can come from a remote repository or an engine-side Git repository. To publish a workspace's commits, use Workspace.git.head.push. Pushing does not modify the calling client's checkout, and checkout hooks do not run.
     *
     * A missing remote ref is created. Without a lease, Git's normal non-force rules apply. Each invocation performs a push; loading the returned receipt does not push again.
     * @param opts.to Destination remote repository. Defaults to the origin remote's push routing, or the source's repository URL when none is registered. Required when the source has no remote URL.
     * @param opts.remote Name of a registered remote to push to (see GitRepository.withRemote). Defaults to origin. The remote's push URLs, or its URL, become the destination; more than one push URL requires an explicit to instead.
     * @param opts.branch Destination branch; a refs/ prefix is used verbatim. Defaults to this ref's branch name. Required for detached and non-branch refs.
     * @param opts.expectedRemoteSha Optional lease: a full lowercase object ID allows replacement only if the remote ref still has that value. Checked even for up-to-date pushes. Empty or omitted uses normal non-force rules, creating the ref if it does not exist.
     */
    push: (opts?: GitRefPushOpts) => GitPushResult;
    /**
     * The resolved ref name at this ref.
     * @deprecated Use "name" instead.
     */
    ref: () => Promise<string>;
    /**
     * The commit this ref resolves to.
     */
    targetCommit: () => GitCommit;
    /**
     * The filesystem tree at this ref.
     * @param opts.discardGitDir Set to true to discard .git directory.
     * @param opts.depth The depth of the tree to fetch.
     * @param opts.includeTags Set to true to populate tag refs in the local checkout .git.
     */
    tree: (opts?: GitRefTreeOpts) => Directory;
    /**
     * Create a single-parent commit on this ref by applying a changeset's edits.
     *
     * Three-way merges the changeset against this ref's tree, using its before snapshot as the base. Preserves compatible parent edits and fails on conflicts. Does not modify the input repository or host checkout.
     *
     * Identity and dates are explicit; neither client Git configuration nor the current clock is consulted.
     * @param changes Changes to apply. Use Changeset.filter to select paths before committing.
     * @param message Commit message.
     * @param date RFC3339 author date; also the default committer date.
     * @param authorName Author name.
     * @param authorEmail Author email.
     * @param opts.committerName Committer name. Defaults to authorName.
     * @param opts.committerEmail Committer email. Defaults to authorEmail.
     * @param opts.committerDate RFC3339 committer date. Defaults to date.
     * @param opts.allowEmpty Allow a commit whose tree matches its parent, including when the supplied edits are already present. Defaults to false.
     * @param opts.signoff Add a Signed-off-by trailer using the commit author's name and email.
     */
    withCommit: (changes: Changeset, message: string, date: string, authorName: string, authorEmail: string, opts?: GitRefWithCommitOpts) => GitRef;
    /**
     * Call the provided function with current GitRef.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: GitRef) => GitRef) => GitRef;
}
/**
 * A named reference to a remote Git repository.
 */
declare class GitRemote extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string);
    /**
     * A unique identifier for this GitRemote.
     */
    id: () => Promise<ID>;
    /**
     * The remote's name.
     */
    name: () => Promise<string>;
    /**
     * Access this remote's repository using its fetch URL and the caller's credentials, or the source's existing capability for this exact destination.
     *
     * HEAD is the remote's HEAD, independent of the workspace's selected commit. Remote registration alone does not grant credentials.
     */
    repository: () => GitRepository;
}
/**
 * A git repository.
 */
declare class GitRepository extends BaseClient {
    private readonly _id?;
    private readonly _url?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _url?: string);
    /**
     * A unique identifier for this GitRepository.
     */
    id: () => Promise<ID>;
    /**
     * Creates a synthetic workspace from this repository's HEAD and uncommitted file changes.
     *
     * Pending changes are applied at the repository root. The staging split is not preserved. The source repository is not modified.
     * @param opts.cwd Current working directory inside the workspace root. Defaults to the workspace root.
     */
    asWorkspace: (opts?: GitRepositoryAsWorkspaceOpts) => Workspace;
    /**
     * Returns details of a branch.
     * @param name Branch's name (e.g., "main").
     * @param opts.noLock Ignore the workspace lockfile for this lookup.
     */
    branch: (name: string, opts?: GitRepositoryBranchOpts) => GitRef;
    /**
     * branches that match any of the given glob patterns.
     * @param opts.patterns Glob patterns (e.g., "refs/tags/v*").
     */
    branches: (opts?: GitRepositoryBranchesOpts) => Promise<string[]>;
    /**
     * Pack the given refs and the objects needed to reconstruct them into a Git bundle.
     * @param refs Refs to advertise in the bundle. At least one named ref is required.
     * @param opts.base A Git ref whose reachable objects are omitted and recorded as a prerequisite.
     */
    bundle: (refs: string[], opts?: GitRepositoryBundleOpts) => GitBundle;
    /**
     * Returns details of a commit.
     * @param id Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").
     *
     * May be abbreviated to an unambiguous hex prefix (4-40 characters), which is expanded to the full SHA. A remote repository that has not fetched the commit yet fetches the history of the refs it advertises to find it.
     */
    commit: (id: string) => GitCommit;
    /**
     * Return the sole remote, otherwise origin, otherwise the selected branch's upstream remote, otherwise null.
     *
     * Frozen workspaces retain their captured upstream selection. Does not contact remote servers.
     */
    defaultRemote: () => Promise<GitRemote | null>;
    /**
     * Returns details for HEAD.
     * @param opts.noLock Ignore the workspace lockfile for this lookup.
     */
    head: (opts?: GitRepositoryHeadOpts) => GitRef;
    /**
     * Return the latest stable release tag, falling back to HEAD when no release exists.
     *
     * Release selection accepts an optional "v" prefix, incomplete versions, and zero-padded numeric components. This operation is pinned unless noLock is enabled.
     * @param opts.version Version query used to select the greatest matching release ref.
     * @param opts.noLock Ignore the workspace lockfile for this lookup.
     */
    latest: (opts?: GitRepositoryLatestOpts) => GitRef;
    /**
     * Returns details of a ref.
     * @param name Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).
     *
     * Commit identifiers may be abbreviated: an unambiguous hex prefix (4-40 characters) of a commit SHA resolves like git rev-parse, with named refs taking precedence, to the full SHA. A remote repository that has not fetched the commit yet fetches the history of the refs it advertises to find it, so only commits reachable from those refs can be abbreviated.
     *
     * The name may be followed by git revision suffixes, applied left to right: `~N` follows first parents N times and `^N` selects the Nth parent (`~` and `^` mean 1, `^0` is the commit itself), e.g. `HEAD~3`, `main^2` or `abc1234~2`. The result is a detached ref of the resulting commit; remote repositories fetch the history the walk needs. Other git revision syntax (`^{...}`, `@{...}`, `:path`, ranges) is not supported.
     *
     * A repository derived from a remote one (e.g. a workspace's history after a snapshot or commit) resolves names and commits it does not contain itself through that remote, with its authentication. Its branches and tags listings include the remote's.
     * @param opts.noLock Ignore the workspace lockfile for this lookup.
     */
    ref: (name: string, opts?: GitRepositoryRefOpts) => GitRef;
    /**
     * Look up a remote by name. Fails when the remote does not exist.
     * @param name The remote's name.
     */
    remote: (name: string) => GitRemote;
    /**
     * List this repository's named remotes, with registered remotes overriding configured ones. Does not contact remote servers.
     */
    remotes: () => Promise<GitRemote[]>;
    /**
     * Returns details of a tag.
     * @param name Tag's name (e.g., "v0.3.9").
     * @param opts.noLock Ignore the workspace lockfile for this lookup.
     */
    tag: (name: string, opts?: GitRepositoryTagOpts) => GitRef;
    /**
     * tags that match any of the given glob patterns.
     * @param opts.patterns Glob patterns (e.g., "refs/tags/v*").
     */
    tags: (opts?: GitRepositoryTagsOpts) => Promise<string[]>;
    /**
     * Returns the changeset of uncommitted changes in the git repository.
     */
    uncommitted: () => Changeset;
    /**
     * The URL of the git repository.
     */
    url: () => Promise<string>;
    /**
     * Import a Git bundle after fetching and verifying all of its prerequisites.
     * @param bundle The Git bundle to import.
     * @param opts.prerequisiteRef An optional remote ref hint for fetching a prerequisite when the remote does not allow fetches by object ID.
     */
    withBundle: (bundle: GitBundle, opts?: GitRepositoryWithBundleOpts) => GitRepository;
    /**
     * Replace this repository's storage with the supplied self-contained Git repository, retaining its logical URL and push destinations.
     *
     * Accepts a whole checkout (including .git and pending file edits), .git contents, or a bare repository. Does not initialize a repository, merge histories, or modify either input.
     *
     * The receiver's logical routing wins over the supplied Git configuration; that configuration is not rewritten. Use Directory.asGit to open the supplied repository without retaining the receiver's routing.
     *
     * When the receiver is a remote repository (or was derived from one), that remote is retained with its authentication: refs the supplied storage does not contain resolve through it.
     * @param directory Existing Git storage to open. Git metadata and object dependencies must be contained in this directory.
     */
    withContents: (directory: Directory) => GitRepository;
    /**
     * Register a named remote on this repository, replacing any registered remote of the same name.
     *
     * Registered remotes are recorded in checkouts materialized from this repository (GitRef.tree, Workspace.git.directory), so remote-aware tooling like gh can resolve and fetch from them. The origin remote also routes push when no explicit destination is passed: its push URL, or its URL, becomes the default destination.
     *
     * Routing metadata only, never a credential grant: pushes still authenticate with the caller's own credentials and require approval as usual.
     * @param name The remote's name, e.g. "origin" or "upstream".
     * @param url The remote's fetch URL.
     * @param opts.pushUrl Push destination, when pushes go somewhere other than url. Empty uses url.
     */
    withRemote: (name: string, url: string, opts?: GitRepositoryWithRemoteOpts) => GitRepository;
    /**
     * Call the provided function with current GitRepository.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: GitRepository) => GitRepository) => GitRepository;
}
/**
 * An internal persistent HTTP state.
 */
declare class HTTPState extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this HTTPState.
     */
    id: () => Promise<ID>;
}
/**
 * Image healthcheck configuration.
 */
declare class HealthcheckConfig extends BaseClient {
    private readonly _id?;
    private readonly _interval?;
    private readonly _retries?;
    private readonly _shell?;
    private readonly _startInterval?;
    private readonly _startPeriod?;
    private readonly _timeout?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _interval?: string, _retries?: number, _shell?: boolean, _startInterval?: string, _startPeriod?: string, _timeout?: string);
    /**
     * A unique identifier for this HealthcheckConfig.
     */
    id: () => Promise<ID>;
    /**
     * Healthcheck command arguments.
     */
    args: () => Promise<string[]>;
    /**
     * Interval between running healthcheck. Example:30s
     */
    interval: () => Promise<string>;
    /**
     * The maximum number of consecutive failures before the container is marked as unhealthy. Example:3
     */
    retries: () => Promise<number>;
    /**
     * Healthcheck command is a shell command.
     */
    shell: () => Promise<boolean>;
    /**
     * StartInterval configures the duration between checks during the startup phase. Example:5s
     */
    startInterval: () => Promise<string>;
    /**
     * StartPeriod allows for failures during this initial startup period which do not count towards maximum number of retries. Example:0s
     */
    startPeriod: () => Promise<string>;
    /**
     * Healthcheck timeout. Example:3s
     */
    timeout: () => Promise<string>;
}
/**
 * Information about the host environment.
 */
declare class Host extends BaseClient {
    private readonly _id?;
    private readonly _findUp?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _findUp?: string);
    /**
     * A unique identifier for this Host.
     */
    id: () => Promise<ID>;
    /**
     * Accesses a container image on the host.
     * @param name Name of the image to access.
     */
    containerImage: (name: string) => Container;
    /**
     * Accesses a directory on the host.
     * @param path Location of the directory to access (e.g., ".").
     * @param opts.exclude Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
     * @param opts.include Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
     * @param opts.noCache If true, the directory will always be reloaded from the host.
     * @param opts.gitignore Apply .gitignore filter rules inside the directory
     */
    directory: (path: string, opts?: HostDirectoryOpts) => Directory;
    /**
     * Accesses a file on the host.
     * @param path Location of the file to retrieve (e.g., "README.md").
     * @param opts.noCache If true, the file will always be reloaded from the host.
     */
    file: (path: string, opts?: HostFileOpts) => File;
    /**
     * Search for a file or directory by walking up the tree from system workdir. Return its relative path. If no match, return null
     * @param name name of the file or directory to search for
     */
    findUp: (name: string, opts?: HostFindUpOpts) => Promise<string>;
    /**
     * Creates a service that forwards traffic to a specified address via the host.
     * @param ports Ports to expose via the service, forwarding through the host network.
     *
     * If a port's frontend is unspecified or 0, it defaults to the same as the backend port.
     *
     * An empty set of ports is not valid; an error will be returned.
     * @param opts.host Upstream host to forward traffic to.
     */
    service: (ports: PortForward[], opts?: HostServiceOpts) => Service;
    /**
     * Creates a tunnel that forwards traffic from the host to a service.
     * @param service Service to send traffic from the tunnel.
     * @param opts.native Map each service port to the same port on the host, as if the service were running natively.
     *
     * Note: enabling may result in port conflicts.
     * @param opts.ports Configure explicit port forwarding rules for the tunnel.
     *
     * If a port's frontend is unspecified or 0, a random port will be chosen by the host.
     *
     * If no ports are given, all of the service's ports are forwarded. If native is true, each port maps to the same port on the host. If native is false, each port maps to a random port chosen by the host.
     *
     * If ports are given and native is true, the ports are additive.
     */
    tunnel: (service: Service, opts?: HostTunnelOpts) => Service;
    /**
     * Accesses a Unix socket on the host.
     * @param path Location of the Unix socket (e.g., "/var/run/docker.sock").
     */
    unixSocket: (path: string) => Socket;
}
/**
 * A name parsed into words, which can be formatted in any casing.
 */
declare class Identifier extends BaseClient {
    private readonly _id?;
    private readonly _format?;
    private readonly _name?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _format?: string, _name?: string);
    /**
     * A unique identifier for this Identifier.
     */
    id: () => Promise<ID>;
    /**
     * Format the identifier in a casing.
     * @param casing The casing to format the identifier in.
     * @param opts.acronyms How to write acronyms and terms where a word starts with a capital.
     */
    format: (casing: Casing, opts?: IdentifierFormatOpts) => Promise<string>;
    /**
     * The name as given.
     */
    name: () => Promise<string>;
    /**
     * The words that make up the name, in order.
     */
    words: () => Promise<IdentifierWord[]>;
}
/**
 * One word of an identifier.
 */
declare class IdentifierWord extends BaseClient {
    private readonly _id?;
    private readonly _kind?;
    private readonly _suffix?;
    private readonly _text?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _kind?: IdentifierWordKind, _suffix?: string, _text?: string);
    /**
     * A unique identifier for this IdentifierWord.
     */
    id: () => Promise<ID>;
    /**
     * The kind of word.
     */
    kind: () => Promise<IdentifierWordKind>;
    /**
     * Trailing digits and/or a plural "s", in that order: SHA+"s", OAuth+"2", HTTP+"2s".
     */
    suffix: () => Promise<string>;
    /**
     * The dictionary entry this word matched, if any.
     */
    term: () => Promise<NamingTerm | null>;
    /**
     * Standard spelling: "client" (WORD), "HTTP" (ACRONYM), "GitHub" (TERM).
     */
    text: () => Promise<string>;
}
/**
 * A graphql input type, which is essentially just a group of named args.
 * This is currently only used to represent pre-existing usage of graphql input types
 * in the core API. It is not used by user modules and shouldn't ever be as user
 * module accept input objects via their id rather than graphql input types.
 */
declare class InputTypeDef extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string);
    /**
     * A unique identifier for this InputTypeDef.
     */
    id: () => Promise<ID>;
    /**
     * Static fields defined on this input object, if any.
     */
    fields: () => Promise<FieldTypeDef[]>;
    /**
     * The name of the input object.
     */
    name: () => Promise<string>;
}
/**
 * A definition of a custom interface defined in a Module.
 */
declare class InterfaceTypeDef extends BaseClient {
    private readonly _id?;
    private readonly _description?;
    private readonly _name?;
    private readonly _sourceModuleName?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _description?: string, _name?: string, _sourceModuleName?: string);
    /**
     * A unique identifier for this InterfaceTypeDef.
     */
    id: () => Promise<ID>;
    /**
     * The doc string for the interface, if any.
     */
    description: () => Promise<string>;
    /**
     * Functions defined on this interface, if any.
     */
    functions: () => Promise<Function_[]>;
    /**
     * The name of the interface.
     */
    name: () => Promise<string>;
    /**
     * The location of this interface declaration.
     */
    sourceMap: () => Promise<SourceMap | null>;
    /**
     * If this InterfaceTypeDef is associated with a Module, the name of the module. Unset otherwise.
     */
    sourceModuleName: () => Promise<string>;
}
declare class JSONValue extends BaseClient {
    private readonly _id?;
    private readonly _asBoolean?;
    private readonly _asInteger?;
    private readonly _asString?;
    private readonly _contents?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _asBoolean?: boolean, _asInteger?: number, _asString?: string, _contents?: JSON);
    /**
     * A unique identifier for this JSONValue.
     */
    id: () => Promise<ID>;
    /**
     * Decode an array from json
     */
    asArray: () => Promise<JSONValue[]>;
    /**
     * Decode a boolean from json
     */
    asBoolean: () => Promise<boolean>;
    /**
     * Decode an integer from json
     */
    asInteger: () => Promise<number>;
    /**
     * Decode a string from json
     */
    asString: () => Promise<string>;
    /**
     * Return the value encoded as json
     * @param opts.pretty Pretty-print
     * @param opts.indent Optional line prefix
     */
    contents: (opts?: JSONValueContentsOpts) => Promise<JSON>;
    /**
     * Lookup the field at the given path, and return its value.
     * @param path Path of the field to lookup, encoded as an array of field names
     */
    field: (path: string[]) => JSONValue;
    /**
     * List fields of the encoded object
     */
    fields: () => Promise<string[]>;
    /**
     * Encode a boolean to json
     * @param value New boolean value
     */
    newBoolean: (value: boolean) => JSONValue;
    /**
     * Encode an integer to json
     * @param value New integer value
     */
    newInteger: (value: number) => JSONValue;
    /**
     * Encode a string to json
     * @param value New string value
     */
    newString: (value: string) => JSONValue;
    /**
     * Return a new json value, decoded from the given content
     * @param contents New JSON-encoded contents
     */
    withContents: (contents: JSON) => JSONValue;
    /**
     * Set a new field at the given path
     * @param path Path of the field to set, encoded as an array of field names
     * @param value The new value of the field
     */
    withField: (path: string[], value: JSONValue) => JSONValue;
    /**
     * Call the provided function with current JSONValue.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: JSONValue) => JSONValue) => JSONValue;
}
/**
 * A conversation with a large language model (LLM): queue prompts, expose tools, and step the model until it completes its turn.
 */
declare class LLM extends BaseClient {
    private readonly _id?;
    private readonly _contextTokens?;
    private readonly _contextWindow?;
    private readonly _hasPending?;
    private readonly _lastReply?;
    private readonly _model?;
    private readonly _provider?;
    private readonly _reasoningEffort?;
    private readonly _spawn?;
    private readonly _sync?;
    private readonly _tools?;
    private readonly _transcript?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _contextTokens?: number, _contextWindow?: number, _hasPending?: boolean, _lastReply?: string, _model?: string, _provider?: string, _reasoningEffort?: string, _spawn?: ID, _sync?: ID, _tools?: string, _transcript?: string);
    /**
     * A unique identifier for this LLM.
     */
    id: () => Promise<ID>;
    /**
     * Reconstruct a spawned agent from its runtime handle.
     *
     * This is the lookup spawn pins its result's identity through: the returned handle's ID is an honest, replayable chain denoting the one instance the spawn minted. It never creates an instance itself.
     * @param handle The opaque runtime handle minted by the spawn that created the agent.
     * @param name The agent's display name, as recorded by the spawn.
     * @experimental
     */
    agent: (handle: string, name: string) => Agent;
    /**
     * Discover every artifact this conversation can address, as one selection, without evaluating their values.
     *
     * Tool objects bound with withTools contribute their modules' artifacts, rooted at their current values: evaluating one reads the live state of the bound tools, not a fresh construction. If a module's main object is bound, only its tree is included; otherwise each bound object of that module contributes its own tree. Addresses start with the module name. These artifacts have no workspace of their own: they evaluate in the LLM's bound workspace, if any, whoever evaluates them.
     *
     * The workspace part is the artifacts of the workspace bound with withWorkspace, as returned by Workspace.artifacts; an LLM with no bound workspace has none. A workspace module with the same name as a module with bound tool objects is omitted: the bound tools shadow it. Unless they are only a plain construction of the module, which has no state of its own: then the workspace module's artifacts are kept instead.
     *
     * Tool arguments that take an address resolve it here: a DAG address to one object, or, for Artifacts and Artifact arguments, a selection filtered like filterUri.
     * @param opts.include Only include artifacts matching these path patterns, as with Workspace.artifacts. A path selects that path and its children.
     * @experimental
     */
    artifacts: (opts?: LLMArtifactsOpts) => Artifacts;
    /**
     * Run expertise on this conversation, passing it through each function in order, and record it in expertise.
     *
     * Composing an entry that is already recorded refreshes its module: the module's contributions are cleared, all of its recorded entries run again, and compatible tool state is kept. Other contributions are untouched.
     *
     * Agent arguments receive the agent that owns this conversation. Without one, entries requiring an Agent are skipped.
     * @param expertise The expertise to run. Each reference retains its source workspace.
     */
    compose: (expertise: Expertise[]) => LLM;
    /**
     * estimated number of tokens currently occupying the context window; unlike tokenUsage this is not cumulative over the session
     */
    contextTokens: () => Promise<number>;
    /**
     * The model's total context window in tokens, or null if unknown (e.g. a local or uncatalogued model).
     */
    contextWindow: () => Promise<number>;
    /**
     * Expertise composed into this conversation, in order.
     */
    expertise: () => Promise<Expertise[]>;
    /**
     * Fork the conversation, so that otherwise-identical follow-ups evaluate independently instead of deduplicating to a single cached result.
     * @param label A label distinguishing this fork from its siblings, e.g. "attempt-2" when retrying a flaky evaluation.
     */
    fork: (label: string) => LLM;
    /**
     * Report whether anything is queued to send to the model: an unsent prompt or unevaluated tool results. When true, another step will do work; when false, the turn is complete.
     */
    hasPending: () => Promise<boolean>;
    /**
     * The text of the model's most recent reply.
     */
    lastReply: () => Promise<string>;
    /**
     * Send the queued prompt and step the model against the available tools, until it ends its turn: a reply with no tool calls and nothing left queued.
     * @param opts.maxSteps Cap the number of steps. The loop fails if the cap is reached before the model ends its turn.
     * @param opts.maxTokens Cap the model's output tokens on each step. Defaults to the model's maximum.
     */
    loop: (opts?: LLMLoopOpts) => LLM;
    /**
     * The full message history, as structured messages.
     */
    messages: () => Promise<LLMMessage[]>;
    /**
     * The model the conversation is running against, after resolving any configured default.
     */
    model: () => Promise<string>;
    /**
     * The provider serving the model, e.g. "anthropic", "openai", "google", or "local".
     */
    provider: () => Promise<string>;
    /**
     * The reasoning effort in use, e.g. "low", "medium", or "high". Empty or "none" when reasoning is disabled.
     */
    reasoningEffort: () => Promise<string>;
    /**
     * The skills visible to the model, exactly as the ListSkills tool serves them: engine-embedded skills, skills installed with withSkills, and skills discovered in the workspace.
     */
    skills: () => Promise<LLMSkill[]>;
    /**
     * Spawn the conversation as an agent: a startable, addressable evaluation loop seeded with this conversation's state, tools, and workspace.
     *
     * Every spawn mints a unique agent instance — two spawns of an identical conversation are two distinct agents, like two calls to a process spawn. The result is pinned to the instance (via the agent lookup field), so re-loading its ID re-addresses the same agent from any request in the session.
     *
     * The loop is not started: the agent spends nothing until it is prompted or resumed, and any input pending on the conversation is stepped then.
     *
     * With a handle, spawn restores an instance instead of minting one: this conversation becomes the committed history of the agent that handle names, so prompting it continues where it left off — rebuild a conversation's ID from a trace, load it, and spawn it under the handle it belonged to. Fails if that instance already has a runtime entry in this session: a restore must happen before anything else addresses the instance, since by then it may have stepped.
     * @param opts.expertise Expertise to compose into the new agent's conversation. Agent arguments receive the new agent, which cannot be used until composition completes; if composition fails, the agent is left STOPPED.
     * @param opts.name Display label for the agent — telemetry and error messages; carries no identity. Defaults to a short name derived from the conversation.
     * @param opts.handle The runtime handle to restore the instance under, as published on its loop span as dagger.io/agent.id. Omit to mint a fresh instance.
     * @param opts.state The lifecycle state to create the agent in, as facts on the entry: IDLE is ready to be prompted, PAUSED parks it, FAILED holds an error a resume retries past, STOPPED preserves a dormant snapshot that send or resume can relaunch.
     *
     * RUNNING and WAITING_INPUT are refused: they describe a loop, and a restored loop died with the session that published it — restore such an agent as IDLE, its interrupted turn's input still pending on the conversation.
     * @param opts.parentHandle Recorded parent handle when restoring an agent. Lineage does not install a notification subscription. Requires a supplied handle. The parent must already be restored in this session and cannot be the agent itself or its descendant.
     * @param opts.error The loop error to create the agent with, for state FAILED. Refused with any other state.
     * @experimental
     */
    spawn: (opts?: LLMSpawnOpts) => Promise<ID>;
    /**
     * Advance the conversation by a single step: send the queued prompt or tool results to the model, evaluate any tool calls it makes, and queue their results. Use loop to step until the model ends its turn.
     * @param opts.maxTokens Cap the model's output tokens for this step. Defaults to the model's maximum.
     */
    step: (opts?: LLMStepOpts) => LLM;
    /**
     * Force evaluation of the conversation's pending operations (prompts, steps, loops) in the engine.
     */
    sync: () => Promise<LLM>;
    /**
     * The cumulative token usage, summed across every API call in the conversation.
     */
    tokenUsage: () => LLMTokenUsage;
    /**
     * Render documentation for the tools currently exposed to the model.
     */
    tools: () => Promise<string>;
    /**
     * The message history rendered as a plain-text transcript, suitable for feeding back to an LLM (e.g. for summarization).
     */
    transcript: () => Promise<string>;
    /**
     * Queue one user message containing ordered text and media blocks.
     * @param content The ordered message content.
     * @param opts.origin The message's recorded provenance.
     */
    withContent: (content: LLMContentBlockInput[], opts?: LLMWithContentOpts) => LLM;
    /**
     * Queue an image, audio, or PDF file as one user message. Media bytes are stored in the conversation.
     * @param file The media file.
     * @param opts.mimeType The media MIME type; inferred from the file's contents when omitted.
     */
    withContentFile: (file: File, opts?: LLMWithContentFileOpts) => LLM;
    /**
     * Add an external MCP server to the LLM
     * @param name The name of the MCP server
     * @param service The MCP service to run and communicate with over stdio
     */
    withMcpServer: (name: string, service: Service) => LLM;
    /**
     * Change the model for the rest of the conversation. The message history is preserved; the new model takes effect on the next step.
     * @param model The model to use, e.g. "claude-sonnet-4-5" or "gpt-5.4".
     * @param opts.provider The provider serving the model, e.g. "openai". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.
     */
    withModel: (model: string, opts?: LLMWithModelOpts) => LLM;
    /**
     * Queue a user prompt, to be sent to the model on the next step or loop.
     * @param prompt The prompt to send
     * @param opts.origin The message's recorded provenance, when it arrived through an agent mailbox rather than from the user. Rendered to the model as an attribution header at request-build time.
     */
    withPrompt: (prompt: string, opts?: LLMWithPromptOpts) => LLM;
    /**
     * Queue a file's contents as a user prompt, like withPrompt.
     * @param file The file to read the prompt from
     */
    withPromptFile: (file: File) => LLM;
    /**
     * Change the reasoning effort for the rest of the conversation, overriding any configured default. The message history is preserved; the new effort takes effect on the next step.
     * @param effort The reasoning effort, e.g. "low", "medium", or "high"; "none" disables reasoning. Supported levels are model-specific — some models also accept e.g. "minimal", "xhigh", or "max".
     */
    withReasoningEffort: (effort: string) => LLM;
    /**
     * Append an assistant response to the message history without calling the model, e.g. to reconstruct a conversation from another source.
     * @param content The response content
     * @param opts.inputTokens Uncached input tokens sent
     * @param opts.outputTokens Tokens received from the model, including text and tool calls
     * @param opts.cachedTokenReads Cached input tokens read
     * @param opts.cachedTokenWrites Cached input tokens written
     * @param opts.totalTokens Total tokens consumed by this response
     */
    withResponse: (content: LLMContentBlockInput[], opts?: LLMWithResponseOpts) => LLM;
    /**
     * Install skills from a directory, adding them to the skills the model discovers with ListSkills and reads with ReadSkill. Each skill is a directory containing a SKILL.md with name and description frontmatter, discovered anywhere in the tree. Installed skills take precedence over skills discovered in the workspace, but cannot shadow the engine's built-in skills.
     * @param directory A directory containing skills, each a subdirectory holding a SKILL.md.
     */
    withSkills: (directory: Directory) => LLM;
    /**
     * Switch to the configured small model for the current provider, or that provider's recommended default. The message history is preserved; unknown providers without a small-model configuration keep their current model.
     */
    withSmallModel: () => LLM;
    /**
     * Add a system prompt, instructing the model across the whole conversation.
     * @param prompt The system prompt to send
     */
    withSystemPrompt: (prompt: string) => LLM;
    /**
     * Append the result of a tool call to the message history.
     * @param callId The ID of the tool call this result responds to
     * @param content Text returned by the tool, placed before blocks
     * @param errored Whether the tool call resulted in an error
     * @param opts.blocks Ordered text and media returned by the tool
     */
    withToolResult: (callId: string, content: string, errored: boolean, opts?: LLMWithToolResultOpts) => LLM;
    /**
     * Expose an object's methods as tools. Every eligible method of the bound object becomes a tool; a tool that returns this object's own type replaces it as the new state. Repeatable to bind several objects.
     * @param object The object whose methods become tools.
     * @param opts.except Method names to exclude from the toolset (e.g. constructors, entrypoints).
     * @param opts.version Version of this binding's state contract. Recomposition preserves compatible state when the version is unchanged and resets to the newly bound object's defaults when it differs. Change this when the state layout changes incompatibly. Same-type tool returns retain the version. Module identity and ownership checks still apply.
     */
    withTools: (object: Node, opts?: LLMWithToolsOpts) => LLM;
    /**
     * Bind the LLM to a workspace, exposing its modules as tools exactly as the Dagger CLI would serve them for that workspace.
     * @param workspace The workspace to work in.
     */
    withWorkspace: (workspace: Workspace) => LLM;
    /**
     * Disable the default system prompt
     */
    withoutDefaultSystemPrompt: () => LLM;
    /**
     * Clear the message history, keeping only the system prompts.
     */
    withoutMessageHistory: () => LLM;
    /**
     * Clear the user-added system prompts, keeping only the default system prompt.
     */
    withoutSystemPrompts: () => LLM;
    /**
     * Return the workspace the LLM is bound to.
     */
    workspace: () => Workspace;
    /**
     * Call the provided function with current LLM.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: LLM) => LLM) => LLM;
}
/**
 * An ordered run of text and media content for a model to read, built outside any conversation.
 */
declare class LLMContent extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this LLMContent.
     */
    id: () => Promise<ID>;
    /**
     * The ordered text and media blocks.
     */
    blocks: () => Promise<LLMContentBlock[]>;
    /**
     * Append image, audio, or PDF bytes as an inline media block. The media kind follows the MIME type.
     *
     * Prefer withFile for anything but small payloads: the bytes become part of the content's identity, so they travel with every reference to it.
     * @param data The media bytes.
     * @param mimeType The media MIME type, e.g. "image/png".
     */
    withData: (data: Bytes, mimeType: string) => LLMContent;
    /**
     * Append an image, audio, or PDF file as an inline media block. The media kind follows the MIME type.
     * @param file The media file. Its contents become the block's inline bytes.
     * @param opts.mimeType The media MIME type, e.g. "image/png". Inferred from the file's contents when omitted.
     */
    withFile: (file: File, opts?: LLMContentWithFileOpts) => LLMContent;
    /**
     * Append a block of text.
     * @param text The text.
     */
    withText: (text: string) => LLMContent;
    /**
     * Call the provided function with current LLMContent.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: LLMContent) => LLMContent) => LLMContent;
}
/**
 * A single piece of content within an LLM message.
 */
declare class LLMContentBlock extends BaseClient {
    private readonly _id?;
    private readonly _arguments?;
    private readonly _callId?;
    private readonly _data?;
    private readonly _errored?;
    private readonly _kind?;
    private readonly _mimeType?;
    private readonly _signature?;
    private readonly _text?;
    private readonly _toolName?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _arguments?: JSON, _callId?: string, _data?: Bytes, _errored?: boolean, _kind?: LLMContentBlockKind, _mimeType?: string, _signature?: string, _text?: string, _toolName?: string);
    /**
     * A unique identifier for this LLMContentBlock.
     */
    id: () => Promise<ID>;
    /**
     * The arguments passed to the tool, JSON-encoded (for TOOL_CALL kind).
     */
    arguments_: () => Promise<JSON>;
    /**
     * The unique ID of a tool call (for TOOL_CALL or TOOL_RESULT kinds).
     */
    callId: () => Promise<string>;
    /**
     * Ordered content returned by a tool, following any text (for TOOL_RESULT kind).
     */
    content: () => Promise<LLMContentBlock[]>;
    /**
     * The media bytes (for IMAGE, AUDIO, or DOCUMENT kinds).
     */
    data: () => Promise<Bytes>;
    /**
     * Whether the tool call resulted in an error (for TOOL_RESULT kind).
     */
    errored: () => Promise<boolean>;
    /**
     * The kind of content block, which determines the other populated fields.
     */
    kind: () => Promise<LLMContentBlockKind>;
    /**
     * The media MIME type (for IMAGE, AUDIO, or DOCUMENT kinds).
     */
    mimeType: () => Promise<string>;
    /**
     * Provider-specific opaque data (e.g. Anthropic thinking signature). Preserve it when reconstructing a conversation.
     */
    signature: () => Promise<string>;
    /**
     * Text content (for TEXT, THINKING, or TOOL_RESULT kinds).
     */
    text: () => Promise<string>;
    /**
     * The name of the tool called (for TOOL_CALL kind).
     */
    toolName: () => Promise<string>;
}
/**
 * A single message in an LLM conversation.
 */
declare class LLMMessage extends BaseClient {
    private readonly _id?;
    private readonly _role?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _role?: LLMMessageRole);
    /**
     * A unique identifier for this LLMMessage.
     */
    id: () => Promise<ID>;
    /**
     * The message's content blocks, in the order the model produced them.
     */
    content: () => Promise<LLMContentBlock[]>;
    /**
     * Who put this message on the record, when it arrived through an agent mailbox.
     *
     * Null for the user's own prompts and for everything the model or tools produced.
     * @experimental
     */
    origin: () => Promise<LLMMessageOrigin | null>;
    /**
     * The role that produced this message.
     */
    role: () => Promise<LLMMessageRole>;
    /**
     * Token usage reported by the provider for the API call that produced this message; all zeros except on assistant responses.
     */
    tokenUsage: () => LLMTokenUsage;
}
/**
 * EXPERIMENTAL: Agent APIs are likely to change.
 *
 * The recorded provenance of a message that arrived through an agent mailbox.
 */
declare class LLMMessageOrigin extends BaseClient {
    private readonly _id?;
    private readonly _agentName?;
    private readonly _kind?;
    private readonly _ref?;
    private readonly _replyTo?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _agentName?: string, _kind?: LLMMessageOriginKind, _ref?: string, _replyTo?: string);
    /**
     * A unique identifier for this LLMMessageOrigin.
     */
    id: () => Promise<ID>;
    /**
     * The display name of the sending agent (for AGENT origins) or the observed agent (for EVENT origins).
     * @experimental
     */
    agentName: () => Promise<string>;
    /**
     * Who put this message on the record.
     * @experimental
     */
    kind: () => Promise<LLMMessageOriginKind>;
    /**
     * The message's short ref within the receiving agent's runtime, e.g. "#3": the deterministic token replies name (send's replyTo) and the message lookup takes.
     * @experimental
     */
    ref: () => Promise<string>;
    /**
     * The ref of the message this one answers, in the sender's own runtime, if any.
     * @experimental
     */
    replyTo: () => Promise<string>;
}
/**
 * A skill available to a model: task-specific guidance discovered with ListSkills and read with ReadSkill.
 */
declare class LLMSkill extends BaseClient {
    private readonly _id?;
    private readonly _description?;
    private readonly _name?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _description?: string, _name?: string);
    /**
     * A unique identifier for this LLMSkill.
     */
    id: () => Promise<ID>;
    /**
     * The one-line description from the SKILL.md frontmatter.
     */
    description: () => Promise<string>;
    /**
     * The skill name, as passed to ReadSkill.
     */
    name: () => Promise<string>;
}
/**
 * A count of tokens consumed by LLM API calls.
 */
declare class LLMTokenUsage extends BaseClient {
    private readonly _id?;
    private readonly _cachedTokenReads?;
    private readonly _cachedTokenWrites?;
    private readonly _inputTokens?;
    private readonly _outputTokens?;
    private readonly _totalTokens?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _cachedTokenReads?: number, _cachedTokenWrites?: number, _inputTokens?: number, _outputTokens?: number, _totalTokens?: number);
    /**
     * A unique identifier for this LLMTokenUsage.
     */
    id: () => Promise<ID>;
    /**
     * Input tokens served from the provider's prompt cache.
     */
    cachedTokenReads: () => Promise<number>;
    /**
     * Input tokens written to the provider's prompt cache.
     */
    cachedTokenWrites: () => Promise<number>;
    /**
     * Uncached input tokens sent to the model.
     */
    inputTokens: () => Promise<number>;
    /**
     * Tokens received from the model, including text and tool calls.
     */
    outputTokens: () => Promise<number>;
    /**
     * Total tokens consumed, as reported by the provider.
     */
    totalTokens: () => Promise<number>;
}
/**
 * A simple key value object that represents a label.
 */
declare class Label extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    private readonly _value?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string, _value?: string);
    /**
     * A unique identifier for this Label.
     */
    id: () => Promise<ID>;
    /**
     * The label name.
     */
    name: () => Promise<string>;
    /**
     * The label value.
     */
    value: () => Promise<string>;
}
/**
 * A definition of a list type in a Module.
 */
declare class ListTypeDef extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this ListTypeDef.
     */
    id: () => Promise<ID>;
    /**
     * The type of the elements in the list.
     */
    elementTypeDef: () => TypeDef;
}
/**
 * A Dagger module.
 */
declare class Module_ extends BaseClient {
    private readonly _id?;
    private readonly _description?;
    private readonly _name?;
    private readonly _serve?;
    private readonly _sync?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _description?: string, _name?: string, _serve?: Void, _sync?: ID);
    /**
     * A unique identifier for this Module.
     */
    id: () => Promise<ID>;
    /**
     * The source used to resolve contextual files and directories, when different from source.
     */
    contextSource: () => Promise<ModuleSource | null>;
    /**
     * The dependencies of the module.
     */
    dependencies: () => Promise<Module_[]>;
    /**
     * The doc string of the module, if any
     */
    description: () => Promise<string>;
    /**
     * Enumerations served by this module.
     */
    enums: () => Promise<TypeDef[]>;
    /**
     * The generated files and directories made on top of the module source's context directory.
     */
    generatedContextDirectory: () => Directory;
    /**
     * Interfaces served by this module.
     */
    interfaces: () => Promise<TypeDef[]>;
    /**
     * The introspection schema JSON file for this module.
     *
     * This file represents the schema visible to the module's source code, including all core types and those from the dependencies.
     *
     * Note: this is in the context of a module, so some core types may be hidden.
     */
    introspectionSchemaJson: () => File;
    /**
     * The name of the module
     */
    name: () => Promise<string>;
    /**
     * Objects served by this module.
     */
    objects: () => Promise<TypeDef[]>;
    /**
     * The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.
     */
    runtime: () => Promise<Container | null>;
    /**
     * The SDK config used by this module.
     */
    sdk: () => Promise<SDKConfig | null>;
    /**
     * Serve a module's API in the current session.
     *
     * Note: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.
     * @param opts.includeDependencies Expose the dependencies of this module to the client
     * @param opts.main Install the module as the workspace main, promoting its main-object methods onto the Query root
     * @param opts.entrypoint Compatibility alias for main.
     */
    serve: (opts?: ModuleServeOpts) => Promise<void>;
    /**
     * The source for the module.
     */
    source: () => Promise<ModuleSource | null>;
    /**
     * Forces evaluation of the module, including any loading into the engine and associated validation.
     */
    sync: () => Promise<Module_>;
    /**
     * User-defined default values, loaded from local .env files.
     */
    userDefaults: () => EnvFile;
    /**
     * Retrieves the module with the given description
     * @param description The description to set
     */
    withDescription: (description: string) => Module_;
    /**
     * This module plus the given Enum type and associated values
     */
    withEnum: (enum_: TypeDef) => Module_;
    /**
     * This module plus the given Interface type and associated functions
     */
    withInterface: (iface: TypeDef) => Module_;
    /**
     * This module plus the given Object type and associated functions.
     */
    withObject: (object: TypeDef) => Module_;
    /**
     * Call the provided function with current Module.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Module_) => Module_) => Module_;
}
/**
 * The client generated for the module.
 */
declare class ModuleConfigClient extends BaseClient {
    private readonly _id?;
    private readonly _directory?;
    private readonly _generator?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _directory?: string, _generator?: string);
    /**
     * A unique identifier for this ModuleConfigClient.
     */
    id: () => Promise<ID>;
    /**
     * The directory the client is generated in.
     */
    directory: () => Promise<string>;
    /**
     * The generator to use
     */
    generator: () => Promise<string>;
}
/**
 * The names the engine gives a module's own declarations, by the naming rules of the version the module is named by.
 *
 * Every list-taking field answers in input order.
 */
declare class ModuleNaming extends BaseClient {
    private readonly _id?;
    private readonly _mainObject?;
    private readonly _name?;
    private readonly _originalName?;
    private readonly _version?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _mainObject?: string, _name?: string, _originalName?: string, _version?: string);
    /**
     * A unique identifier for this ModuleNaming.
     */
    id: () => Promise<ID>;
    /**
     * The schema names of enum members this module declares, in input order: SCREAMING_SNAKE_CASE.
     *
     * A name already written in GraphQL's enum member convention (HTTP2, P_256) is kept as it is. With the engine's identifier naming rules, Arm64 is ARM64 and GitHub is GITHUB; with the legacy strcase rules, Arm64 is ARM_64.
     *
     * Errors on a name the module's naming rules can't parse (non-ASCII, no letters or digits, or over 256 bytes). The legacy rules name anything.
     * @param names The enum member names as the module's source declares them.
     */
    enumMemberNames: (names: string[]) => Promise<string[]>;
    /**
     * The schema names of functions, object fields and function arguments this module declares, including interface functions and their arguments, in input order.
     *
     * With the engine's identifier naming rules, acronyms are written like words: getURL is getUrl, baseURL is baseUrl, HTTPClient is httpClient. With the legacy strcase rules, HTTPClient is httpclient and e2eTest is e2ETest.
     *
     * Errors on a name the module's naming rules can't parse (non-ASCII, no letters or digits, or over 256 bytes). The legacy rules name anything.
     * @param names The function, field and argument names as the module's source declares them.
     */
    fieldNames: (names: string[]) => Promise<string[]>;
    /**
     * Which of the candidates, the names of objects this module declares, the engine takes as the module's main object; null if none.
     *
     * The engine finds the main object by the module's name, in any casing: the first candidate, in input order, whose name normalizes to the same type name as the module's, or is spelled as that type name already (K8STools for module k8s-tools, by either rules: strcase spells k8s-tools K8STools, though it would normalize K8STools again to K8Stools). Failing that, with the engine's identifier naming rules only, the only candidate spelled the same once lowercased without word boundaries (Sha256Sum for module sha256sum); several such candidates are ambiguous, and give null.
     *
     * Errors on a name the module's naming rules can't parse (non-ASCII, no letters or digits, or over 256 bytes). The legacy rules name anything.
     * @param candidates The object names as the module's source declares them.
     */
    mainObject: (candidates: string[]) => Promise<string>;
    /**
     * The module's name, which namespaces its types.
     */
    name: () => Promise<string>;
    /**
     * The module's original name, which its main object and namespaced type names are spelled after. It differs from name when the module is loaded under another name, as a dependency alias.
     */
    originalName: () => Promise<string>;
    /**
     * The schema names of objects, interfaces and enums this module declares, as the engine installs them: namespaced under the module's name, in input order.
     *
     * A name is prefixed with the module's name unless it already starts with the module's name on a word boundary. With the engine's identifier naming rules: HttpClient in module my-mod is MyModHTTPClient; Postman in module post is PostPostman, while PostMan stays PostMan; K8STools in module k8s-tools is K8sTools, the module's own name. With the legacy strcase rules, HttpClient in module my-mod is MyModHttpClient.
     *
     * Errors on a name the module's naming rules can't parse (non-ASCII, no letters or digits, or over 256 bytes). The legacy rules name anything.
     * @param names The type names as the module's source declares them.
     */
    typeNames: (names: string[]) => Promise<string[]>;
    /**
     * The normalized engine version whose naming rules name the module.
     */
    version: () => Promise<string>;
}
/**
 * The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc.
 */
declare class ModuleSource extends BaseClient {
    private readonly _id?;
    private readonly _asString?;
    private readonly _cloneRef?;
    private readonly _commit?;
    private readonly _configExists?;
    private readonly _digest?;
    private readonly _engineVersion?;
    private readonly _htmlRepoUrl?;
    private readonly _htmlUrl?;
    private readonly _kind?;
    private readonly _localContextDirectoryPath?;
    private readonly _moduleName?;
    private readonly _moduleOriginalName?;
    private readonly _originalSubpath?;
    private readonly _pin?;
    private readonly _repoRootPath?;
    private readonly _sourceRootSubpath?;
    private readonly _sourceSubpath?;
    private readonly _sync?;
    private readonly _version?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _asString?: string, _cloneRef?: string, _commit?: string, _configExists?: boolean, _digest?: string, _engineVersion?: string, _htmlRepoUrl?: string, _htmlUrl?: string, _kind?: ModuleSourceKind, _localContextDirectoryPath?: string, _moduleName?: string, _moduleOriginalName?: string, _originalSubpath?: string, _pin?: string, _repoRootPath?: string, _sourceRootSubpath?: string, _sourceSubpath?: string, _sync?: ID, _version?: string);
    /**
     * A unique identifier for this ModuleSource.
     */
    id: () => Promise<ID>;
    /**
     * Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation
     */
    asModule: () => Module_;
    /**
     * A human readable ref string representation of this module source.
     */
    asString: () => Promise<string>;
    /**
     * The blueprint referenced by the module source.
     * @deprecated Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in dagger.toml instead.
     */
    blueprint: () => ModuleSource;
    /**
     * The client-facing introspection schema JSON file for this module source.
     *
     * This is the schema consumed by client codegen: unlike introspectionSchemaJson (the module-facing schema), it hides no core types and installs this module (reached via dag.<moduleName>) so a generated client can bind it. The module's dependencies are excluded: a client is generated for a single module plus core, not its dependency graph.
     * @param opts.clientVersion The engine version of the client the schema is generated for, e.g. the engineVersion of the module that depends on this one: core is described as that client sees it, capped at this engine's version.
     *
     * This module is still loaded at the engine version it declares, and so named by its own naming rules, as the engine serves it to such a client. Defaults to this module's own engine version.
     *
     * Use this rather than withEngineVersion, which makes this module declare another version and so changes its names.
     */
    clientSchemaIntrospectionJson: (opts?: ModuleSourceClientSchemaIntrospectionJsonOpts) => File;
    /**
     * The ref to clone the root of the git repo from. Only valid for git sources.
     */
    cloneRef: () => Promise<string>;
    /**
     * The resolved commit of the git repo this source points to.
     */
    commit: () => Promise<string>;
    /**
     * The clients generated for the module.
     */
    configClients: () => Promise<ModuleConfigClient[]>;
    /**
     * Whether an existing module config file was found.
     */
    configExists: () => Promise<boolean>;
    /**
     * The full directory loaded for the module source, including the source code as a subdirectory.
     */
    contextDirectory: () => Directory;
    /**
     * The dependencies of the module source.
     */
    dependencies: () => Promise<ModuleSource[]>;
    /**
     * A content-hash of the module source. Module sources with the same digest will output the same generated context and convert into the same module instance.
     */
    digest: () => Promise<string>;
    /**
     * The directory containing the module configuration and source code (source code may be in a subdir).
     * @param path A subpath from the source directory to select.
     */
    directory: (path: string) => Directory;
    /**
     * The engine version of the module.
     */
    engineVersion: () => Promise<string>;
    /**
     * Return the supplied workspace with this module's generated context applied.
     *
     * The workspace change baseline is preserved, so a later Workspace.changes call includes this generation together with any other edits made by the caller.
     * @param workspace The workspace to apply generated files to.
     */
    generate: (workspace: Workspace) => Workspace;
    /**
     * The generated files and directories made on top of the module source's context directory, returned as a Changeset.
     */
    generatedContextChangeset: () => Changeset;
    /**
     * The generated files and directories made on top of the module source's context directory.
     */
    generatedContextDirectory: () => Directory;
    /**
     * The URL to access the web view of the repository (e.g., GitHub, GitLab, Bitbucket).
     */
    htmlRepoUrl: () => Promise<string>;
    /**
     * The URL to the source's git repo in a web browser. Only valid for git sources.
     */
    htmlUrl: () => Promise<string>;
    /**
     * The introspection schema JSON file for this module source.
     *
     * This file represents the schema visible to the module's source code, including all core types and those from the dependencies.
     *
     * Note: this is in the context of a module, so some core types may be hidden.
     */
    introspectionSchemaJson: () => File;
    /**
     * The kind of module source (currently local, git or dir).
     */
    kind: () => Promise<ModuleSourceKind>;
    /**
     * The full absolute path to the context directory on the caller's host filesystem that this module source is loaded from. Only valid for local module sources.
     */
    localContextDirectoryPath: () => Promise<string>;
    /**
     * The name of the module, including any setting via the withName API.
     */
    moduleName: () => Promise<string>;
    /**
     * The original name of the module as read from the module config file (or set for the first time with the withName API).
     */
    moduleOriginalName: () => Promise<string>;
    /**
     * The names the engine gives this module's own declarations, by the naming rules of the version the module is named by: its engine version, capped at the engine's own.
     *
     * Modules declaring a version before the identifier casing cut get the legacy strcase names; later ones get the engine's identifier naming rules.
     *
     * For SDK codegen, which must reproduce these names before the module is loaded: this reads only the source's name, original name and engine version, so it answers before the module's entrypoint is generated.
     *
     * It answers for the module under its own name (moduleName). A workspace or toolchain that loads the module under another name namespaces its types under that name instead, which this doesn't reflect.
     * @experimental
     */
    naming: () => ModuleNaming;
    /**
     * The original subpath used when instantiating this module source, relative to the context directory.
     */
    originalSubpath: () => Promise<string>;
    /**
     * The pinned version of this module source.
     */
    pin: () => Promise<string>;
    /**
     * The import path corresponding to the root of the git repo this source points to. Only valid for git sources.
     */
    repoRootPath: () => Promise<string>;
    /**
     * The SDK configuration of the module.
     */
    sdk: () => Promise<SDKConfig | null>;
    /**
     * The path, relative to the context directory, that contains the module config.
     */
    sourceRootSubpath: () => Promise<string>;
    /**
     * The path to the directory containing the module's source code, relative to the context directory.
     */
    sourceSubpath: () => Promise<string>;
    /**
     * Forces evaluation of the module source, including any loading into the engine and associated validation.
     */
    sync: () => Promise<ModuleSource>;
    /**
     * The toolchains referenced by the module source.
     * @deprecated Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in dagger.toml instead.
     */
    toolchains: () => Promise<ModuleSource[]>;
    /**
     * The module's dagger.json with any in-memory edits from with* APIs applied, as a diff relative to the source's context directory.
     *
     * Unlike generatedContextDirectory, this does not run codegen and does not validate the engine version against the running engine, so it can be used to declare an engine requirement newer than the running engine. Loading or serving such a module still fails at moduleSource.asModule.
     */
    updatedConfigDirectory: () => Directory;
    /**
     * User-defined defaults read from local .env files
     */
    userDefaults: () => EnvFile;
    /**
     * The specified version of the git repo this source points to.
     */
    version: () => Promise<string>;
    /**
     * Set a blueprint for the module source.
     * @param blueprint The blueprint module to set.
     * @deprecated Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead.
     */
    withBlueprint: (blueprint: ModuleSource) => ModuleSource;
    /**
     * Update the module source with a new client to generate.
     * @param generator The generator to use
     * @param outputDir The output directory for the generated client.
     */
    withClient: (generator: string, outputDir: string) => ModuleSource;
    /**
     * Append the provided dependencies to the module source's dependency list.
     * @param dependencies The dependencies to append.
     */
    withDependencies: (dependencies: ModuleSource[]) => ModuleSource;
    /**
     * Upgrade the engine version of the module to the given value.
     * @param version The engine version to upgrade to.
     */
    withEngineVersion: (version: string) => ModuleSource;
    /**
     * Enable the experimental features for the module source.
     * @param features The experimental features to enable.
     */
    withExperimentalFeatures: (features: ModuleSourceExperimentalFeature[]) => ModuleSource;
    /**
     * Update the module source with additional include patterns for files+directories from its context that are required for building it
     * @param patterns The new additional include patterns.
     */
    withIncludes: (patterns: string[]) => ModuleSource;
    /**
     * Update the module source with a new name.
     * @param name The name to set.
     */
    withName: (name: string) => ModuleSource;
    /**
     * Update the module source with a new SDK.
     * @param source The SDK source to set.
     */
    withSdk: (source: string) => ModuleSource;
    /**
     * Update the module source with a new source subpath.
     * @param path The path to set as the source subpath. Must be relative to the module source's source root directory.
     */
    withSourceSubpath: (path: string) => ModuleSource;
    /**
     * Add toolchains to the module source.
     * @param toolchains The toolchain modules to add.
     * @deprecated Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead.
     */
    withToolchains: (toolchains: ModuleSource[]) => ModuleSource;
    /**
     * Update the blueprint module to the latest version.
     * @deprecated Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead.
     */
    withUpdateBlueprint: () => ModuleSource;
    /**
     * Update one or more module dependencies.
     * @param dependencies The dependencies to update.
     */
    withUpdateDependencies: (dependencies: string[]) => ModuleSource;
    /**
     * Update one or more toolchains.
     * @param toolchains The toolchains to update.
     * @deprecated Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead.
     */
    withUpdateToolchains: (toolchains: string[]) => ModuleSource;
    /**
     * Update one or more clients.
     * @param clients The clients to update
     */
    withUpdatedClients: (clients: string[]) => ModuleSource;
    /**
     * Remove the current blueprint from the module source.
     * @deprecated Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead.
     */
    withoutBlueprint: () => ModuleSource;
    /**
     * Remove a client from the module source.
     * @param path The path of the client to remove.
     */
    withoutClient: (path: string) => ModuleSource;
    /**
     * Remove the provided dependencies from the module source's dependency list.
     * @param dependencies The dependencies to remove.
     */
    withoutDependencies: (dependencies: string[]) => ModuleSource;
    /**
     * Disable experimental features for the module source.
     * @param features The experimental features to disable.
     */
    withoutExperimentalFeatures: (features: ModuleSourceExperimentalFeature[]) => ModuleSource;
    /**
     * Remove the provided toolchains from the module source.
     * @param toolchains The toolchains to remove.
     * @deprecated Legacy dagger.json field. Generic module loading no longer honors it; use workspace modules in `dagger.toml` instead.
     */
    withoutToolchains: (toolchains: string[]) => ModuleSource;
    /**
     * Call the provided function with current ModuleSource.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: ModuleSource) => ModuleSource) => ModuleSource;
}
/**
 * The engine's identifier naming rules at an engine version.
 */
declare class Naming extends BaseClient {
    private readonly _id?;
    private readonly _version?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _version?: string);
    /**
     * A unique identifier for this Naming.
     */
    id: () => Promise<ID>;
    /**
     * The acronyms and terms used to parse and format identifiers.
     */
    dictionary: () => Promise<NamingTerm[]>;
    /**
     * Format many names at once, for codegen. Returns them in input order.
     * @param names The names to format, in any casing.
     * @param casing The casing to format the names in.
     * @param opts.acronyms How to write acronyms and terms where a word starts with a capital.
     */
    formatIdentifiers: (names: string[], casing: Casing, opts?: NamingFormatIdentifiersOpts) => Promise<string[]>;
    /**
     * Parse a name in any casing into words.
     *
     * Known acronyms and terms come from the naming dictionary; everything else falls back to the case heuristic. Errors on non-ASCII input, input with no letters or digits, or input over 256 bytes.
     * @param name The name to parse.
     */
    identifier: (name: string) => Identifier;
    /**
     * The names the engine gives the declarations of a module named by these rules.
     * @param name The module's name, which namespaces its types.
     * @param opts.originalName The module's original name, when it's loaded under another (as a dependency alias): its main object and namespaced type names are spelled after it. Defaults to name.
     */
    module_: (name: string, opts?: NamingModuleOpts) => ModuleNaming;
    /**
     * The normalized engine version these rules answer for.
     */
    version: () => Promise<string>;
}
/**
 * An entry in the naming dictionary.
 */
declare class NamingTerm extends BaseClient {
    private readonly _id?;
    private readonly _capitalized?;
    private readonly _spelling?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _capitalized?: string, _spelling?: string);
    /**
     * A unique identifier for this NamingTerm.
     */
    id: () => Promise<ID>;
    /**
     * Spelling in the CAPITALIZED style: "Http", "Ipv6", "GitHub", "Ios".
     */
    capitalized: () => Promise<string>;
    /**
     * Standard spelling: "HTTP", "IPv6", "GitHub", "iOS".
     */
    spelling: () => Promise<string>;
}
/**
 * An object with a globally unique ID.
 */
interface Node {
    id(): Promise<ID>;
}
declare class _NodeClient extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    id: () => Promise<ID>;
}
/**
 * A definition of a custom object defined in a Module.
 */
declare class ObjectTypeDef extends BaseClient {
    private readonly _id?;
    private readonly _deprecated?;
    private readonly _description?;
    private readonly _name?;
    private readonly _sourceModuleName?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _deprecated?: string, _description?: string, _name?: string, _sourceModuleName?: string);
    /**
     * A unique identifier for this ObjectTypeDef.
     */
    id: () => Promise<ID>;
    /**
     * The function used to construct new instances of this object, if any.
     */
    constructor_: () => Promise<Function_ | null>;
    /**
     * The reason this enum member is deprecated, if any.
     */
    deprecated: () => Promise<string>;
    /**
     * The doc string for the object, if any.
     */
    description: () => Promise<string>;
    /**
     * Static fields defined on this object, if any.
     */
    fields: () => Promise<FieldTypeDef[]>;
    /**
     * Functions defined on this object, if any.
     */
    functions: () => Promise<Function_[]>;
    /**
     * The name of the object.
     */
    name: () => Promise<string>;
    /**
     * The location of this object declaration.
     */
    sourceMap: () => Promise<SourceMap | null>;
    /**
     * If this ObjectTypeDef is associated with a Module, the name of the module. Unset otherwise.
     */
    sourceModuleName: () => Promise<string>;
}
/**
 * A port exposed by a container.
 */
declare class Port extends BaseClient {
    private readonly _id?;
    private readonly _description?;
    private readonly _experimentalSkipHealthcheck?;
    private readonly _port?;
    private readonly _protocol?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _description?: string, _experimentalSkipHealthcheck?: boolean, _port?: number, _protocol?: NetworkProtocol);
    /**
     * A unique identifier for this Port.
     */
    id: () => Promise<ID>;
    /**
     * The port description.
     */
    description: () => Promise<string>;
    /**
     * Skip the health check when run as a service.
     */
    experimentalSkipHealthcheck: () => Promise<boolean>;
    /**
     * The port number.
     */
    port: () => Promise<number>;
    /**
     * The transport layer protocol.
     */
    protocol: () => Promise<NetworkProtocol>;
}
/**
 * The root of the DAG.
 */
declare class Client extends BaseClient {
    private readonly _id?;
    private readonly _currentTimestamp?;
    private readonly _defaultPlatform?;
    private readonly _serveModule?;
    private readonly _setSessionTitle?;
    private readonly _version?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _currentTimestamp?: string, _defaultPlatform?: Platform, _serveModule?: Void, _setSessionTitle?: Void, _version?: string);
    /**
     * Get the Raw GraphQL client.
     */
    getGQLClient(): GraphQLClient;
    /**
     * A unique identifier for this Query.
     */
    id: () => Promise<ID>;
    /**
     * Resolve external references only.
     */
    address: (value: string) => Address;
    /**
     * Creates a file from arbitrary binary contents.
     * @param name Name of the new file. Example: "archive.tar"
     * @param contents Binary contents of the new file, encoded as base64 at the GraphQL boundary.
     * @param opts.permissions Permissions of the new file. Example: 0600
     */
    blob: (name: string, contents: Bytes, opts?: ClientBlobOpts) => File;
    /**
     * Constructs a cache volume for a given cache key.
     * @param key A string identifier to target this cache volume (e.g., "modules-cache").
     * @param opts.source Identifier of the directory to use as the cache volume's root.
     * @param opts.sharing Sharing mode of the cache volume.
     * @param opts.owner A user:group to set for the cache volume root.
     *
     * The user and group can either be an ID (1000:1000) or a name (foo:bar).
     *
     * If the group is omitted, it defaults to the same as the user.
     */
    cacheVolume: (key: string, opts?: ClientCacheVolumeOpts) => CacheVolume;
    /**
     * Creates an empty changeset
     */
    changeset: () => Changeset;
    /**
     * Dagger Cloud configuration and state
     */
    cloud: () => Cloud;
    /**
     * Creates a scratch container, with no image or metadata.
     *
     * To pull an image, follow up with the "from" function.
     * @param opts.platform Platform to initialize the container with. Defaults to the native platform of the current engine
     */
    container: (opts?: ClientContainerOpts) => Container;
    /**
     * The FunctionCall context that the SDK caller is currently executing in.
     *
     * If the caller is not currently executing in a function, this will return an error.
     */
    currentFunctionCall: () => FunctionCall;
    /**
     * The module currently being served in the session, if any.
     */
    currentModule: () => CurrentModule;
    /**
     * The object that received the current module function call, as a Node. Errors when there is no current call, or the call is top-level (e.g. a module constructor).
     */
    currentNode: () => Node;
    /**
     * The current UTC time in RFC3339 format. Never cached.
     */
    currentTimestamp: () => Promise<string>;
    /**
     * The TypeDef representations of the objects currently being served in the session.
     * @param opts.returnAllTypes Return the full referenced typedef closure instead of only top-level served typedefs.
     * @param opts.hideCore Strip core API functions from the Query type, leaving only module-sourced functions (constructors, workspace main proxies, etc.).
     *
     * Core types (Container, Directory, etc.) are kept so return types and method chaining still work.
     */
    currentTypeDefs: (opts?: ClientCurrentTypeDefsOpts) => Promise<TypeDef[]>;
    /**
     * Detect and return the current workspace.
     * @experimental
     */
    currentWorkspace: () => Workspace;
    /**
     * The default platform of the engine.
     */
    defaultPlatform: () => Promise<Platform>;
    /**
     * Creates an empty directory.
     */
    directory: () => Directory;
    /**
     * The Dagger engine container configuration and state
     */
    engine: () => Engine;
    /**
     * Constructs an engine-managed volume backed by operator-provided storage beneath the configured engine state root.
     * @param name Canonical slash-separated volume name beneath the engine volume namespace.
     * @param opts.subdir Optional existing subdirectory within the volume payload to mount.
     */
    engineVolume: (name: string, opts?: ClientEngineVolumeOpts) => Volume;
    /**
     * Initialize an environment file
     * @param opts.expand Replace "${VAR}" or "$VAR" with the value of other vars
     */
    envFile: (opts?: ClientEnvFileOpts) => EnvFile;
    /**
     * Create a new error.
     * @param message A brief description of the error.
     */
    error: (message: string) => Error$1;
    /**
     * Creates a file with the specified contents.
     * @param name Name of the new file. Example: "foo.txt"
     * @param contents Contents of the new file. Example: "Hello world!"
     * @param opts.permissions Permissions of the new file. Example: 0600
     */
    file: (name: string, contents: string, opts?: ClientFileOpts) => File;
    /**
     * Creates a function.
     * @param name Name of the function, in its original format from the implementation language.
     * @param returnType Return type of the function.
     */
    function_: (name: string, returnType: TypeDef) => Function_;
    /**
     * Create a code generation result, given a directory containing the generated code.
     */
    generatedCode: (code: Directory) => GeneratedCode;
    /**
     * Queries a Git repository.
     * @param url URL of the git repository.
     *
     * Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.
     *
     * Suffix ".git" is optional.
     * @param opts.keepGitDir DEPRECATED: Set to true to keep .git directory.
     * @param opts.sshKnownHosts Set SSH known hosts
     * @param opts.sshAuthSocket Set SSH auth socket
     * @param opts.httpAuthUsername Username used to populate the password during basic HTTP Authorization
     * @param opts.httpAuthToken Secret used to populate the password during basic HTTP Authorization
     * @param opts.httpAuthHeader Secret used to populate the Authorization HTTP header
     * @param opts.experimentalServiceHost A service which must be started before the repo is fetched.
     */
    git: (url: string, opts?: ClientGitOpts) => GitRepository;
    /**
     * Queries the host environment.
     */
    host: () => Host;
    /**
     * Returns a file containing an http remote url content.
     * @param url HTTP url to get the content from (e.g., "https://docs.dagger.io").
     * @param opts.name File name to use for the file. Defaults to the last part of the URL.
     * @param opts.permissions Permissions to set on the file.
     * @param opts.checksum Expected digest of the downloaded content (e.g., "sha256:...").
     * @param opts.authHeader Secret used to populate the Authorization HTTP header
     * @param opts.experimentalServiceHost A service which must be started before the URL is fetched.
     */
    http: (url: string, opts?: ClientHttpOpts) => File;
    /**
     * Initialize a JSON value
     */
    json: () => JSONValue;
    /**
     * Initialize a new LLM conversation.
     * @param opts.model The model to converse with, e.g. "claude-sonnet-4-5" or "gpt-5.4". Defaults to the configured default model.
     * @param opts.provider The provider serving the model, e.g. "openai". Overrides the provider otherwise inferred from the model name — useful when the name matches no known pattern (e.g. a fine-tune), or matches the wrong one.
     * @experimental
     */
    llm: (opts?: ClientLLMOpts) => LLM;
    /**
     * Start an empty run of text and media content, independent of any conversation.
     *
     * Add blocks with withText, withFile, and withData. A function exposed as an LLM tool can return the content to give the model text and media as the tool's result, e.g. a caption and a screenshot for the model to look at.
     * @experimental
     */
    llmContent: () => LLMContent;
    /**
     * Create a new module.
     */
    module_: () => Module_;
    /**
     * Create a new module source instance from a source ref string
     * @param refString The string ref representation of the module source
     * @param opts.version Version query for a Git module source.
     * @param opts.refPin The pinned version of the module source
     * @param opts.disableFindUp If true, do not attempt to find a module config file in a parent directory of the provided path. Only relevant for local module sources.
     * @param opts.allowNotExists If true, do not error out if the provided ref string is a local path and does not exist yet. Useful when initializing new modules in directories that don't exist yet.
     * @param opts.requireKind If set, error out if the ref string is not of the provided requireKind.
     */
    moduleSource: (refString: string, opts?: ClientModuleSourceOpts) => ModuleSource;
    /**
     * The engine's identifier naming rules at an engine version.
     * @param opts.version The engine version to answer for, e.g. the engine version a module declares. Defaults to the caller's. Normalized as a module's is: a version past the engine's is capped at the engine's own.
     * @experimental
     */
    naming: (opts?: ClientNamingOpts) => Naming;
    /**
     * Load any object by its ID.
     */
    node: (id: ID) => Promise<Node | null>;
    /**
     * Load a GraphQL introspection schema for merging.
     * @param json The introspection schema JSON to load.
     */
    schema: (json: JSON) => Schema;
    /**
     * Creates a new secret.
     * @param uri The URI of the secret store
     * @param opts.cacheKey If set, the given string will be used as the cache key for this secret. This means that any secrets with the same cache key will be considered equivalent in terms of cache lookups, even if they have different URIs or plaintext values.
     *
     * For example, two secrets with the same cache key provided as secret env vars to other wise equivalent containers will result in the container withExecs hitting the cache for each other.
     *
     * If not set, the cache key for the secret will be derived from its plaintext value as looked up when the secret is constructed.
     */
    secret: (uri: string, opts?: ClientSecretOpts) => Secret;
    /**
     * Load the module at the given address and serve its API in the current session.
     *
     * A local address resolves against the caller's workspace, so a generated client can serve the module it is bound to without reaching for the workspace itself.
     * @param address A module address, or an explicit path into the caller's workspace.
     *
     * Absolute paths (e.g. "/.dagger/modules/hello") resolve from the workspace root, relative ones (e.g. "./hello") from the workspace cwd.
     *
     * Installed module names are not accepted.
     * @param opts.refPin The pinned version of a remote module address.
     */
    serveModule: (address: string, opts?: ClientServeModuleOpts) => Promise<void>;
    /**
     * Sets a secret given a user defined name to its plaintext and returns the secret.
     *
     * The plaintext value is limited to a size of 128000 bytes.
     * @param name The user defined name for this secret
     * @param plaintext The plaintext of the secret
     */
    setSecret: (name: string, plaintext: string) => Secret;
    /**
     * Name the current session.
     *
     * The title renames the session wherever its telemetry is shown (the calling client's primary span, e.g. the CLI's command span) and labels its engine archive, as listed by dagger agent --resume. The latest title wins. Only the session's main client may set it.
     * @param title The title, sanitized to a single printable line.
     * @experimental
     */
    setSessionTitle: (title: string) => Promise<void>;
    /**
     * Creates source map metadata.
     * @param filename The filename from the module source.
     * @param line The line number within the filename.
     * @param column The column number within the line.
     */
    sourceMap: (filename: string, line: number, column: number) => SourceMap;
    /**
     * Constructs an SSHFS volume.
     * @param endpoint SSHFS endpoint URL in the form sshfs://user@host[:port]/absolute/path.
     * @param privateKey Private key secret used to authenticate to the remote host.
     * @param opts.knownHosts known_hosts material used to verify the remote host key. Required unless insecureSkipHostKeyCheck is true.
     * @param opts.cacheKey Optional cache equivalence key. If set, volumes with the same cacheKey may be considered equivalent for cache lookups, still subject to their resource dependencies.
     * @param opts.insecureSkipHostKeyCheck Disable SSH host key verification. This is insecure and must be explicitly opted into.
     * @param opts.experimentalServiceHost Service to use as the SSHFS network endpoint while verifying the original host key.
     * @param opts.followSymlinks Resolve symlinks on the remote host, so they appear as the files and directories they point to. Without it, absolute symlinks and symlinks containing ".." can't be read. With it, creating a symlink in the volume fails.
     */
    sshfsVolume: (endpoint: string, privateKey: Secret, opts?: ClientSshfsVolumeOpts) => Volume;
    /**
     * Create a new TypeDef.
     */
    typeDef: () => TypeDef;
    /**
     * Get the current Dagger Engine version.
     */
    version: () => Promise<string>;
}
/**
 * An internal persistent bare git mirror.
 */
declare class RemoteGitMirror extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this RemoteGitMirror.
     */
    id: () => Promise<ID>;
}
/**
 * The SDK config of the module.
 */
declare class SDKConfig extends BaseClient {
    private readonly _id?;
    private readonly _debug?;
    private readonly _source?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _debug?: boolean, _source?: string);
    /**
     * A unique identifier for this SDKConfig.
     */
    id: () => Promise<ID>;
    /**
     * Whether to start the SDK runtime in debug mode with an interactive terminal.
     */
    debug: () => Promise<boolean>;
    /**
     * Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation.
     */
    source: () => Promise<string>;
}
/**
 * A definition of a custom scalar defined in a Module.
 */
declare class ScalarTypeDef extends BaseClient {
    private readonly _id?;
    private readonly _description?;
    private readonly _name?;
    private readonly _sourceModuleName?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _description?: string, _name?: string, _sourceModuleName?: string);
    /**
     * A unique identifier for this ScalarTypeDef.
     */
    id: () => Promise<ID>;
    /**
     * A doc string for the scalar, if any.
     */
    description: () => Promise<string>;
    /**
     * The name of the scalar.
     */
    name: () => Promise<string>;
    /**
     * If this ScalarTypeDef is associated with a Module, the name of the module. Unset otherwise.
     */
    sourceModuleName: () => Promise<string>;
}
/**
 * A GraphQL introspection schema that can be inspected and merged.
 */
declare class Schema extends BaseClient {
    private readonly _id?;
    private readonly _contents?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _contents?: JSON);
    /**
     * A unique identifier for this Schema.
     */
    id: () => Promise<ID>;
    /**
     * Serialize the schema back to introspection JSON.
     */
    contents: () => Promise<JSON>;
    /**
     * Merge a module's introspection-shaped type definitions into the schema, returning the combined schema.
     * @param moduleTypes Introspection JSON describing the types the module defines. Object, interface and enum types are appended to the schema, and a constructor field for the module is added to the Query type.
     * @param moduleName The name of the module whose types are being merged. Used to stamp the @sourceMap directive and to derive the module's constructor field.
     */
    merge: (moduleTypes: JSON, moduleName: string) => Schema;
    /**
     * Call the provided function with current Schema.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Schema) => Schema) => Schema;
}
declare class SearchResult extends BaseClient {
    private readonly _id?;
    private readonly _absoluteOffset?;
    private readonly _filePath?;
    private readonly _lineNumber?;
    private readonly _matchedLines?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _absoluteOffset?: number, _filePath?: string, _lineNumber?: number, _matchedLines?: string);
    /**
     * A unique identifier for this SearchResult.
     */
    id: () => Promise<ID>;
    /**
     * The byte offset of this line within the file.
     */
    absoluteOffset: () => Promise<number>;
    /**
     * The path to the file that matched.
     */
    filePath: () => Promise<string>;
    /**
     * The first line that matched.
     */
    lineNumber: () => Promise<number>;
    /**
     * The line content that matched.
     */
    matchedLines: () => Promise<string>;
    /**
     * Sub-match positions and content within the matched lines.
     */
    submatches: () => Promise<SearchSubmatch[]>;
}
declare class SearchSubmatch extends BaseClient {
    private readonly _id?;
    private readonly _end?;
    private readonly _start?;
    private readonly _text?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _end?: number, _start?: number, _text?: string);
    /**
     * A unique identifier for this SearchSubmatch.
     */
    id: () => Promise<ID>;
    /**
     * The match's end offset within the matched lines.
     */
    end: () => Promise<number>;
    /**
     * The match's start offset within the matched lines.
     */
    start: () => Promise<number>;
    /**
     * The matched text.
     */
    text: () => Promise<string>;
}
/**
 * A reference to a secret value, which can be handled more safely than the value itself.
 */
declare class Secret extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    private readonly _plaintext?;
    private readonly _uri?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string, _plaintext?: string, _uri?: string);
    /**
     * A unique identifier for this Secret.
     */
    id: () => Promise<ID>;
    /**
     * The name of this secret.
     */
    name: () => Promise<string>;
    /**
     * The value of this secret.
     */
    plaintext: () => Promise<string>;
    /**
     * The URI of this secret.
     */
    uri: () => Promise<string>;
}
/**
 * A content-addressed service providing TCP connectivity.
 */
declare class Service extends BaseClient {
    private readonly _id?;
    private readonly _endpoint?;
    private readonly _hostname?;
    private readonly _start?;
    private readonly _stop?;
    private readonly _sync?;
    private readonly _up?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _endpoint?: string, _hostname?: string, _start?: ID, _stop?: ID, _sync?: ID, _up?: Void);
    /**
     * A unique identifier for this Service.
     */
    id: () => Promise<ID>;
    /**
     * Retrieves an endpoint that clients can use to reach this container.
     *
     * If no port is specified, the first exposed port is used. If none exist an error is returned.
     *
     * If a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.
     * @param opts.port The exposed port number for the endpoint
     * @param opts.scheme Return a URL with the given scheme, eg. http for http://
     */
    endpoint: (opts?: ServiceEndpointOpts) => Promise<string>;
    /**
     * Retrieves a hostname which can be used by clients to reach this container.
     */
    hostname: () => Promise<string>;
    /**
     * Retrieves the list of ports provided by the service.
     * @param opts.declared Return only container ports declared before startup. Other service types return an empty list.
     */
    ports: (opts?: ServicePortsOpts) => Promise<Port[]>;
    /**
     * Start the service and wait for its health checks to succeed.
     *
     * Services bound to a Container do not need to be manually started.
     */
    start: () => Promise<Service>;
    /**
     * Stop the service.
     * @param opts.kill Immediately kill the service without waiting for a graceful exit
     */
    stop: (opts?: ServiceStopOpts) => Promise<Service>;
    /**
     * Forces evaluation of the pipeline in the engine.
     */
    sync: () => Promise<Service>;
    terminal: (opts?: ServiceTerminalOpts) => Service;
    /**
     * Creates a tunnel that forwards traffic from the caller's network to this service.
     * @param opts.ports List of frontend/backend port mappings to forward.
     *
     * Frontend is the port accepting traffic on the host, backend is the service port.
     * @param opts.random Bind each tunnel port to a random port on the host.
     */
    up: (opts?: ServiceUpOpts) => Promise<void>;
    /**
     * Configures a hostname which can be used by clients within the session to reach this container.
     * @param hostname The hostname to use.
     */
    withHostname: (hostname: string) => Service;
    /**
     * Call the provided function with current Service.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Service) => Service) => Service;
}
/**
 * A Unix or TCP/IP socket that can be mounted into a container.
 */
declare class Socket extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this Socket.
     */
    id: () => Promise<ID>;
}
/**
 * Source location information.
 */
declare class SourceMap extends BaseClient {
    private readonly _id?;
    private readonly _column?;
    private readonly _filename?;
    private readonly _line?;
    private readonly _module?;
    private readonly _url?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _column?: number, _filename?: string, _line?: number, _module?: string, _url?: string);
    /**
     * A unique identifier for this SourceMap.
     */
    id: () => Promise<ID>;
    /**
     * The column number within the line.
     */
    column: () => Promise<number>;
    /**
     * The filename from the module source.
     */
    filename: () => Promise<string>;
    /**
     * The line number within the filename.
     */
    line: () => Promise<number>;
    /**
     * The module dependency this was declared in.
     */
    module_: () => Promise<string>;
    /**
     * The URL to the file, if any. This can be used to link to the source map in the browser.
     */
    url: () => Promise<string>;
}
/**
 * A file or directory status object.
 */
declare class Stat extends BaseClient {
    private readonly _id?;
    private readonly _fileType?;
    private readonly _name?;
    private readonly _permissions?;
    private readonly _size?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _fileType?: FileType, _name?: string, _permissions?: number, _size?: number);
    /**
     * A unique identifier for this Stat.
     */
    id: () => Promise<ID>;
    /**
     * file type
     */
    fileType: () => Promise<FileType>;
    /**
     * file name
     */
    name: () => Promise<string>;
    /**
     * permission bits
     */
    permissions: () => Promise<number>;
    /**
     * file size
     */
    size: () => Promise<number>;
}
/**
 * An object that can be force-evaluated.
 *
 * Calling sync ensures that the object's entire dependency DAG has been evaluated, returning the object's ID once complete.
 */
interface Syncer {
    id(): Promise<ID>;
    sync(): Promise<Syncer>;
}
declare class _SyncerClient extends BaseClient {
    private readonly _id?;
    private readonly _sync?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _sync?: ID);
    id: () => Promise<ID>;
    sync: () => Promise<Syncer>;
}
/**
 * An interactive terminal that clients can connect to.
 */
declare class Terminal extends BaseClient {
    private readonly _id?;
    private readonly _sync?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _sync?: ID);
    /**
     * A unique identifier for this Terminal.
     */
    id: () => Promise<ID>;
    /**
     * Forces evaluation of the pipeline in the engine.
     *
     * It doesn't run the default command if no exec has been set.
     */
    sync: () => Promise<Terminal>;
}
/**
 * A definition of a parameter or return type in a Module.
 */
declare class TypeDef extends BaseClient {
    private readonly _id?;
    private readonly _kind?;
    private readonly _name?;
    private readonly _optional?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _kind?: TypeDefKind, _name?: string, _optional?: boolean);
    /**
     * A unique identifier for this TypeDef.
     */
    id: () => Promise<ID>;
    /**
     * Collection metadata, or null if this object is not a collection.
     */
    asCollection: () => Promise<CollectionTypeDef | null>;
    /**
     * If kind is ENUM, the enum-specific type definition. If kind is not ENUM, this will be null.
     */
    asEnum: () => Promise<EnumTypeDef | null>;
    /**
     * If kind is INPUT, the input-specific type definition. If kind is not INPUT, this will be null.
     */
    asInput: () => Promise<InputTypeDef | null>;
    /**
     * If kind is INTERFACE, the interface-specific type definition. If kind is not INTERFACE, this will be null.
     */
    asInterface: () => Promise<InterfaceTypeDef | null>;
    /**
     * If kind is LIST, the list-specific type definition. If kind is not LIST, this will be null.
     */
    asList: () => Promise<ListTypeDef | null>;
    /**
     * If kind is OBJECT, the object-specific type definition. If kind is not OBJECT, this will be null.
     */
    asObject: () => Promise<ObjectTypeDef | null>;
    /**
     * If kind is SCALAR, the scalar-specific type definition. If kind is not SCALAR, this will be null.
     */
    asScalar: () => Promise<ScalarTypeDef | null>;
    /**
     * The kind of type this is (e.g. primitive, list, object).
     */
    kind: () => Promise<TypeDefKind>;
    /**
     * The canonical non-optional name of the type.
     */
    name: () => Promise<string>;
    /**
     * Whether this type can be set to null. Defaults to false.
     */
    optional: () => Promise<boolean>;
    /**
     * Mark this object as a collection.
     */
    withCollection: () => TypeDef;
    /**
     * Select the field that receives changes from the original collection.
     */
    withCollectionDelta: (name: string) => TypeDef;
    /**
     * Select the item lookup function for this collection.
     */
    withCollectionGet: (name: string) => TypeDef;
    /**
     * Select the stored keys field for this collection.
     */
    withCollectionKeys: (name: string) => TypeDef;
    /**
     * Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.
     */
    withConstructor: (function_: Function_) => TypeDef;
    /**
     * Returns a TypeDef of kind Enum with the provided name.
     *
     * Note that an enum's values may be omitted if the intent is only to refer to an enum. This is how functions are able to return their own, or any other circular reference.
     * @param name The name of the enum
     * @param opts.description A doc string for the enum, if any
     * @param opts.sourceMap The source map for the enum definition.
     */
    withEnum: (name: string, opts?: TypeDefWithEnumOpts) => TypeDef;
    /**
     * Adds a static value for an Enum TypeDef, failing if the type is not an enum.
     * @param name The name of the member in the enum
     * @param opts.value The value of the member in the enum
     * @param opts.description A doc string for the member, if any
     * @param opts.sourceMap The source map for the enum member definition.
     * @param opts.deprecated If deprecated, the reason or migration path.
     */
    withEnumMember: (name: string, opts?: TypeDefWithEnumMemberOpts) => TypeDef;
    /**
     * Adds a static value for an Enum TypeDef, failing if the type is not an enum.
     * @param value The name of the value in the enum
     * @param opts.description A doc string for the value, if any
     * @param opts.sourceMap The source map for the enum value definition.
     * @param opts.deprecated If deprecated, the reason or migration path.
     * @deprecated Use withEnumMember instead
     */
    withEnumValue: (value: string, opts?: TypeDefWithEnumValueOpts) => TypeDef;
    /**
     * Adds a static field for an Object TypeDef, failing if the type is not an object.
     * @param name The name of the field in the object
     * @param typeDef The type of the field
     * @param opts.description A doc string for the field, if any
     * @param opts.sourceMap The source map for the field definition.
     * @param opts.deprecated If deprecated, the reason or migration path.
     */
    withField: (name: string, typeDef: TypeDef, opts?: TypeDefWithFieldOpts) => TypeDef;
    /**
     * Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.
     */
    withFunction: (function_: Function_) => TypeDef;
    /**
     * Returns a TypeDef of kind Interface with the provided name.
     */
    withInterface: (name: string, opts?: TypeDefWithInterfaceOpts) => TypeDef;
    /**
     * Sets the kind of the type.
     */
    withKind: (kind: TypeDefKind) => TypeDef;
    /**
     * Returns a TypeDef of kind List with the provided type for its elements.
     */
    withListOf: (elementType: TypeDef) => TypeDef;
    /**
     * Returns a TypeDef of kind Object with the provided name.
     *
     * Note that an object's fields and functions may be omitted if the intent is only to refer to an object. This is how functions are able to return their own object, or any other circular reference.
     */
    withObject: (name: string, opts?: TypeDefWithObjectOpts) => TypeDef;
    /**
     * Sets whether this type can be set to null.
     */
    withOptional: (optional: boolean) => TypeDef;
    /**
     * Returns a TypeDef of kind Scalar with the provided name.
     */
    withScalar: (name: string, opts?: TypeDefWithScalarOpts) => TypeDef;
    /**
     * Call the provided function with current TypeDef.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: TypeDef) => TypeDef) => TypeDef;
}
/**
 * A filesystem volume that can be mounted into containers.
 */
declare class Volume extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this Volume.
     */
    id: () => Promise<ID>;
}
/**
 * A Dagger workspace detected from the current working directory or constructed from a Directory.
 */
declare class Workspace extends BaseClient {
    private readonly _id?;
    private readonly _address?;
    private readonly _configFile?;
    private readonly _configRead?;
    private readonly _cwd?;
    private readonly _detectScope?;
    private readonly _entrypoint?;
    private readonly _export?;
    private readonly _findUp?;
    private readonly _main?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _address?: string, _configFile?: string, _configRead?: string, _cwd?: string, _detectScope?: string, _entrypoint?: string, _export?: Void, _findUp?: string, _main?: string);
    /**
     * A unique identifier for this Workspace.
     */
    id: () => Promise<ID>;
    /**
     * Canonical Dagger address of the workspace location, or an opaque identity for synthetic workspaces.
     */
    address: () => Promise<string>;
    /**
     * Discover static object artifacts from workspace modules without evaluating their values.
     *
     * Omits the paths that the workspace's check.skip, generate.skip, and up.skip settings skip.
     * @param opts.include Only include artifacts matching these path patterns, as with checks and services. A path selects that path and its children.
     */
    artifacts: (opts?: WorkspaceArtifactsOpts) => Artifacts;
    /**
     * Return this workspace's changes, with paths relative to its working directory.
     *
     * Pass from to compare against an earlier workspace state. Omitting it preserves the cumulative behavior used by clients from before this argument was added.
     * @param opts.from An earlier workspace state to compare against.
     */
    changes: (opts?: WorkspaceChangesOpts) => Changeset;
    /**
     * Preview which source commits withCommitsFrom would apply, skip, or report as conflicting.
     *
     * Results are ordered oldest first and account for earlier applicable commits in the same preview. The preview does not apply commits or write to the checkout.
     *
     * A local receiver is snapshotted automatically; untracked files require interactive approval. Source uncommitted changes are ignored. Exceeding maxCommits fails rather than returning a partial preview. Divergent merge commits require manual integration.
     * @param source Git-backed source workspace. For a local checkout, call snapshot on the source first and pass the returned workspace.
     * @param opts.commits Full lowercase commit hashes or unambiguous lowercase hex prefixes (4-40 characters) to select, in any order. Prefixes resolve against the frozen source's Git objects and are recorded as full hashes; duplicate selections after resolution are rejected. Empty selects all new source commits. Selected commits must be within the source's latest 10000 commits.
     * @param opts.maxCommits Maximum commits in either differing history, from 1 to 1000. Exceeding the limit fails; nothing is silently omitted.
     */
    compareCommitsFrom: (source: Workspace, opts?: WorkspaceCompareCommitsFromOpts) => Promise<WorkspaceCommitPick[]>;
    /**
     * Selected native workspace config file relative to the workspace cwd, if any.
     */
    configFile: () => Promise<string>;
    /**
     * Read a configuration value from dagger.toml.
     *
     * If key is empty, returns the full config.
     *
     * If key points to a scalar, returns the value.
     *
     * If key points to a table, returns flattened dotted-key output.
     * @param opts.key Dotted key path (e.g. modules.greeter.source). Empty for full config.
     * @param opts.effective Include the selected environment, user overrides, and legacy workspace settings.
     */
    configRead: (opts?: WorkspaceConfigReadOpts) => Promise<string>;
    /**
     * Current location within the workspace root.
     *
     * The workspace root is returned as "/".
     *
     * Relative paths in workspace APIs resolve from here.
     */
    cwd: () => Promise<string>;
    /**
     * Return the selected SDK module's current scope at this workspace location.
     * @param sdk SDK name to probe. Required.
     */
    detectScope: (sdk: string) => Promise<string>;
    /**
     * Returns a Directory from the workspace.
     *
     * Relative paths resolve from the workspace cwd. Absolute paths resolve from the workspace root.
     * @param path Location of the directory to retrieve. Relative paths (e.g., "src") resolve from the workspace cwd; absolute paths (e.g., "/src") resolve from the workspace root.
     * @param opts.exclude Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).
     * @param opts.include Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).
     * @param opts.gitignore Apply .gitignore filter rules inside the directory.
     */
    directory: (path: string, opts?: WorkspaceDirectoryOpts) => Directory;
    /**
     * Compatibility alias for main.
     * @deprecated Use main.
     */
    entrypoint: () => Promise<string>;
    /**
     * List named environments defined in the workspace configuration.
     */
    envList: () => Promise<string[]>;
    /**
     * Write this workspace's commits and pending changes to a checkout on the calling client.
     *
     * Path selects the destination; omitting it uses the calling client's current local workspace root, including when exporting a snapshot or committed workspace. Exported file paths are relative to the workspace root regardless of its working directory. This writes only to the client making the call, never the source's client.
     *
     * A live workspace exported to its own checkout applies only its overlay edits, without capturing the whole checkout. This also applies with an explicit path. Pass from with the same live base to apply only changes since that overlay state.
     *
     * Other exports integrate divergent commits by cherry-picking, preserve unrelated checkout edits, and refuse conflicts. Live inputs are snapshotted automatically; capturing untracked source files requires interactive approval. Stable inputs retain their baseline. Pass from to save only work since an earlier source value, including previously saved pending edits that are now committed.
     * @param opts.path Destination checkout path on the calling client. Relative paths start at the client's working directory. Omit to use the calling client's current local workspace root.
     * @param opts.from Earlier workspace state to compare against. For Git integration, live inputs are snapshotted at export time; use a snapshot to retain the baseline of a previous export.
     */
    export: (opts?: WorkspaceExportOpts) => Promise<void>;
    /**
     * Returns a File from the workspace.
     *
     * Relative paths resolve from the workspace cwd. Absolute paths resolve from the workspace root.
     * @param path Location of the file to retrieve. Relative paths (e.g., "go.mod") resolve from the workspace cwd; absolute paths (e.g., "/go.mod") resolve from the workspace root.
     */
    file: (path: string) => File;
    /**
     * Find project roots marked by any of the given filenames, starting from a path relative to the workspace cwd.
     *
     * Returns cwd-relative directory paths for every marked directory at or below start, plus the nearest marked ancestor when start itself is not marked.
     *
     * Each returned path is usable as-is with other workspace APIs, e.g. directory(path).
     * @param opts.start Directory to start from. Relative paths resolve from the workspace cwd.
     * @param opts.markers File basenames that mark a project root (e.g. ["go.mod"] or ["deno.json", "deno.jsonc"]).
     * @param opts.exclude Glob patterns pruning the walk below start (e.g. ["**\/node_modules/**"]).
     */
    findRoots: (opts?: WorkspaceFindRootsOpts) => Promise<string[]>;
    /**
     * Search for a file or directory by walking up from the start path within the workspace.
     *
     * Returns the absolute workspace path if found, or null if not found.
     *
     * Relative start paths resolve from the workspace cwd.
     *
     * The search stops at the workspace root and will not traverse above it.
     * @param name The name of the file or directory to search for.
     * @param opts.from Path to start the search from. Relative paths resolve from the workspace cwd; absolute paths resolve from the workspace root.
     */
    findUp: (name: string, opts?: WorkspaceFindUpOpts) => Promise<string>;
    /**
     * Git state for this workspace. Errors if the workspace is not in a git repository.
     */
    git: () => WorkspaceGit;
    /**
     * Returns a list of files and directories that match the given pattern.
     *
     * Patterns match paths relative to the workspace root.
     * @param pattern Pattern to match (e.g., "*.md").
     */
    glob: (pattern: string) => Promise<string[]>;
    /**
     * Installed name of the module selected as the workspace main, or an empty string when none is selected.
     *
     * Reflects the selected env's effective view. Fails if several modules are selected.
     */
    main: () => Promise<string>;
    /**
     * Plan the explicit migration needed for the current workspace.
     *
     * Include installed local modules and their local dependencies. Other module candidates remain unchanged unless selected.
     *
     * The returned plan has an empty changeset and no steps when no migration is needed.
     * @param opts.modules Additional local modules to migrate. Relative paths start at the workspace cwd; absolute paths start at the workspace root.
     */
    migrate: (opts?: WorkspaceMigrateOpts) => WorkspaceMigration;
    /**
     * Plan migration of one local module without migrating its dependencies or creating a workspace configuration.
     *
     * Include SDK registration when a workspace configuration exists and remove obsolete generated-file ignore rules.
     * @param opts.path Module directory. Relative paths start at the workspace cwd; absolute paths start at the workspace root.
     */
    migrateModule: (opts?: WorkspaceMigrateModuleOpts) => WorkspaceMigration;
    /**
     * Return a module defined in the workspace configuration.
     *
     * Reflects the selected env's effective view.
     * @param name Module name to inspect.
     */
    module_: (name: string) => WorkspaceModule;
    /**
     * Load a module source from a path within the workspace.
     *
     * Relative paths (e.g., "foo") resolve from the workspace cwd; absolute paths (e.g., "/foo") resolve from the workspace root.
     *
     * Fails if the path does not point to an initialized module.
     * @param path Location of the module source to load, relative to the workspace cwd or absolute from the workspace root.
     */
    moduleSource: (path: string) => ModuleSource;
    /**
     * List modules defined in the workspace configuration.
     *
     * Reflects the selected env's effective view.
     */
    modules: () => Promise<WorkspaceModule[]>;
    /**
     * Resolve an address in this workspace.
     *
     * A DAG address (dag://<path>) selects exactly one workspace artifact: artifacts.filterUri(value).one(). Its typed loaders use that artifact and never fall back to external resolution.
     *
     * A value without the dag:// scheme keeps its external meaning, such as a container image reference.
     *
     * The Address retains this workspace across module calls and ID reloads.
     * @param value A DAG address, or an external reference.
     */
    resolve: (value: string) => Address;
    /**
     * An installed SDK, by name.
     * @param name SDK name to look up.
     */
    sdk: (name: string) => WorkspaceSDK;
    /**
     * Installed SDKs.
     */
    sdks: () => Promise<WorkspaceSDK[]>;
    /**
     * Searches for content matching the given regular expression or literal string.
     *
     * Uses Rust regex syntax; escape literal ., [, ], {, }, | with backslashes.
     *
     * Runs ripgrep on the client host, falling back to grep if unavailable.
     * @param opts.paths Directory or file paths to search
     * @param opts.globs Glob patterns to match (e.g., "*.md")
     * @param opts.pattern The text to match.
     * @param opts.literal Interpret the pattern as a literal string instead of a regular expression.
     * @param opts.multiline Enable searching across multiple lines.
     * @param opts.dotall Allow the . pattern to match newlines in multiline mode.
     * @param opts.insensitive Enable case-insensitive matching.
     * @param opts.skipIgnored Honor .gitignore, .ignore, and .rgignore files.
     * @param opts.skipHidden Skip hidden files (files starting with .).
     * @param opts.filesOnly Only return matching files, not lines and content
     * @param opts.limit Limit the number of results to return
     */
    search: (opts?: WorkspaceSearchOpts) => Promise<SearchResult[]>;
    /**
     * Return a snapshot of this workspace as a stable value.
     *
     * Git capture is a progressive enhancement: if the workspace has no Git repository or commits, or the client cannot capture Git, return this workspace unchanged. Approval rejections and capture failures remain errors.
     *
     * Use the returned workspace for subsequent reads, edits, and module loading against the captured baseline. Snapshotting an existing stable value preserves its baseline; snapshot currentWorkspace again to capture later checkout changes.
     *
     * Only the owning client can capture a local checkout. Tracked changes are captured automatically; untracked files require interactive approval. Remote Git refs are pinned to their resolved commits. Capturing leaves the checkout unchanged.
     *
     * The recipe is portable when a remote can serve its base; otherwise it is frozen for this session only.
     * @experimental
     */
    snapshot: () => Workspace;
    /**
     * Return this workspace with a changeset applied, without mutating the source.
     * @param changes Changes to apply.
     */
    withChanges: (changes: Changeset) => Workspace;
    /**
     * Return this workspace with a generated module client added to one SDK scope.
     *
     * Select the deepest detected or registered scope. Fail if several SDKs have that deepest scope.
     * @param module Explicit local path or module address to generate a client for. Installed module names are not supported.
     * @param opts.sdk Optional SDK name. Inspect all installed SDKs when omitted.
     * @param opts.settings Explicit SDK-module constructor setting overrides for this scope. Requires an explicit SDK name.
     */
    withClient: (module_: string, opts?: WorkspaceWithClientOpts) => Workspace;
    /**
     * Create a Git commit from a changeset and return a stable workspace with HEAD advanced.
     *
     * The changeset is three-way merged into both HEAD and the frozen working tree. Compatible unselected edits remain uncommitted; incoming changes need not already be in the working tree. Conflicts with either tree fail without modifying the workspace. Empty changesets, or changes already present in HEAD, fail with nothing to commit.
     *
     * A local workspace is snapshotted automatically before committing; untracked files require interactive approval. The host checkout is not modified.
     *
     * Missing author fields are resolved from Git config in the calling client's working directory at commit time, then recorded explicitly for reproducible commits. Unconfigured fields default to Dagger and dagger@localhost.
     * @param changes Changeset to commit, for example git.uncommitted.filter(...). Paths are rooted at the repository; rename sides are determined by the changeset. Git metadata (.git) is ignored; metadata-only changes fail with nothing to commit.
     * @param message Commit message.
     * @param date RFC3339 author and committer date. Required for reproducible commits.
     * @param opts.authorName Author and committer name. Defaults to git config user.name in the calling client's working directory, otherwise Dagger.
     * @param opts.authorEmail Author and committer email. Defaults to git config user.email in the calling client's working directory, otherwise dagger@localhost.
     * @param opts.signoff Add a Signed-off-by trailer using the commit author's name and email.
     */
    withCommit: (changes: Changeset, message: string, date: string, opts?: WorkspaceWithCommitOpts) => Workspace;
    /**
     * Integrate source commits into this workspace and return the result, preserving this workspace's uncommitted changes and metadata.
     *
     * Fast-forward when the selected commits include all new ancestors of their tip; otherwise cherry-pick them oldest first. Already integrated commits and patches already present are skipped. Any conflict fails the operation. Source uncommitted changes are not transferred; merge them explicitly if needed. Use compareCommitsFrom to preview the integration.
     *
     * A local receiver is snapshotted automatically; untracked files require interactive approval. The checkout is not modified. Export the result with an explicit path to write it to a checkout.
     *
     * Cherry-picks preserve the source author and author date, use the calling client's Git config for committer identity, and reuse the source committer date for reproducible hashes. Origin trailers track cherry-picked commits. Divergent merge commits require manual integration.
     * @param source Git-backed source workspace. For a local checkout, call snapshot on the source first and pass the returned workspace.
     * @param opts.commits Full lowercase commit hashes or unambiguous lowercase hex prefixes (4-40 characters) to select, in any order. Prefixes resolve against the frozen source's Git objects and are recorded as full hashes; duplicate selections after resolution are rejected. Empty selects all new source commits. Selected commits must be within the source's latest 10000 commits.
     * @param opts.maxCommits Maximum commits in either differing history, from 1 to 1000. Exceeding the limit fails; nothing is silently omitted.
     */
    withCommitsFrom: (source: Workspace, opts?: WorkspaceWithCommitsFromOpts) => Workspace;
    /**
     * Return this workspace with a named config environment created.
     * @param name Environment name.
     * @param opts.here Write to the workspace config directory at the workspace cwd.
     */
    withConfigEnv: (name: string, opts?: WorkspaceWithConfigEnvOpts) => Workspace;
    /**
     * Select the config environment carried by this workspace.
     * @param name Environment name, or empty to clear the selection.
     */
    withConfigEnvironment: (name: string) => Workspace;
    /**
     * Select workspace-root-relative config and lockfile paths. Empty paths clear the selection.
     * @param configFile Config file path.
     * @param lockFile Lockfile path.
     */
    withConfigPaths: (configFile: string, lockFile: string) => Workspace;
    /**
     * Return this workspace with a configuration value written.
     *
     * When the session selects an env, the key is scoped to that env's overlay and the env is created if missing.
     * @param key Dotted key path.
     * @param value Value to set. Bools, integers, and comma-separated arrays are auto-detected.
     * @param opts.values List value to set. Elements are stored verbatim, with no auto-detection. Mutually exclusive with value.
     * @param opts.here Write to the workspace config directory at the workspace cwd.
     */
    withConfigValue: (key: string, value: string, opts?: WorkspaceWithConfigValueOpts) => Workspace;
    /**
     * Return this workspace with a directory merged into the given path, without mutating the source.
     *
     * Anything already at the path stays, and files the source carries win, as with Directory.withDirectory. Use withNewDirectory to replace the path instead.
     * @param path Path to merge into. Relative paths resolve from the workspace cwd.
     * @param source Directory to merge there.
     */
    withDirectory: (path: string, source: Directory) => Workspace;
    /**
     * Compatibility alias for withMain.
     * @param name Exact installed module name.
     * @deprecated Use withMain.
     */
    withEntrypoint: (name: string) => Workspace;
    /**
     * Return this workspace with a file added or replaced, without mutating the source.
     * @param path Destination path. Relative paths resolve from the workspace cwd.
     * @param source File to add.
     * @param opts.permissions Permissions of the added file. Defaults to the source file permissions.
     */
    withFile: (path: string, source: File, opts?: WorkspaceWithFileOpts) => Workspace;
    /**
     * Return this workspace with a location initialized as a module scope.
     *
     * The selected SDK module records the scope and generates the module source.
     * @param sdk Workspace SDK name or module entry name to use. Required.
     * @param opts.name Module name. The engine infers it from path, the active config file, or the workspace root when omitted.
     * @param opts.path Module path relative to the workspace cwd, or an absolute workspace path. Defaults to .dagger/modules/<name> beside the active workspace config.
     * @param opts.install Install the module. When omitted, install only if path is omitted.
     * @param opts.main Select this module as the workspace main and install it. False prevents automatic selection. When omitted, select only if both path and name are omitted and the module is installed.
     * @param opts.entrypoint Compatibility alias for main.
     * @param opts.settings Explicit SDK-module constructor setting overrides for this scope.
     */
    withInitModule: (sdk: string, opts?: WorkspaceWithInitModuleOpts) => Workspace;
    /**
     * Return this workspace with a native configuration, without changing an existing configuration.
     *
     * Fail if legacy configuration needs workspace migration.
     */
    withInitialized: () => Workspace;
    /**
     * Return this workspace with an installed module selected as its main.
     *
     * Every other main selection is cleared. The main module lives in the base workspace config.
     * @param name Exact installed module name.
     */
    withMain: (name: string) => Workspace;
    /**
     * Return this workspace with a module installed in its config.
     *
     * When the session selects an env, the module is recorded in that env's overlay and the env is created if missing.
     * @param ref Module reference to install.
     * @param opts.name Override name for the installed module entry.
     * @param opts.here Write to the workspace config directory at the workspace cwd.
     */
    withModule: (ref: string, opts?: WorkspaceWithModuleOpts) => Workspace;
    /**
     * Return this workspace with a directory mounted at the given path, without mutating the source.
     *
     * Mounted content is readable and editable through the normal workspace file tools but shadows the source at the mount path and stays out of the pending changeset: neither it nor edits to it appear in changes or are exported. Use withoutMount to remove the mount.
     * @param path Location of the mounted directory. Relative paths resolve from the workspace cwd.
     * @param source Directory to mount.
     */
    withMountedDirectory: (path: string, source: Directory) => Workspace;
    /**
     * Return this workspace with a file mounted at the given path, without mutating the source.
     *
     * Mounted content is readable and editable through the normal workspace file tools but shadows the source at the mount path and stays out of the pending changeset: neither it nor edits to it appear in changes or are exported. Use withoutMount to remove the mount.
     * @param path Location of the mounted file. Relative paths resolve from the workspace cwd.
     * @param source File to mount.
     */
    withMountedFile: (path: string, source: File) => Workspace;
    /**
     * Return this workspace with the given path replaced by a directory, without mutating the source.
     *
     * The source becomes the entire contents of the path: anything already there that the source does not carry is removed. Use withDirectory to keep it instead.
     * @param path Path to replace. Relative paths resolve from the workspace cwd.
     * @param source Directory to write there.
     */
    withNewDirectory: (path: string, source: Directory) => Workspace;
    /**
     * Return this workspace with a new or replaced file, without mutating the source.
     * @param path Path of the new file. Relative paths resolve from the workspace cwd.
     * @param contents Contents of the new file.
     * @param opts.permissions Permissions of the new file.
     */
    withNewFile: (path: string, contents: string, opts?: WorkspaceWithNewFileOpts) => Workspace;
    /**
     * Return this workspace with the given Git-compatible patch file applied, without mutating the source.
     *
     * Paths in the patch are relative to the workspace root, whatever its cwd, as `git diff` writes them. Sections for paths in a mount apply to the mounted content; a section cannot move a file across a mount boundary or delete a mount point.
     * @param patch File containing the patch to apply
     * @param opts.onConflict How to handle hunks that no longer apply to the target content: fail (default), or apply what fits and leave git-style conflict markers where it doesn't.
     * @experimental
     */
    withPatchFile: (patch: File, opts?: WorkspaceWithPatchFileOpts) => Workspace;
    /**
     * Move this workspace's Git HEAD to a commit and return the resulting stable workspace.
     *
     * A local workspace is snapshotted automatically before resetting; untracked files require interactive approval. The host checkout is not modified. By default the difference between the previous working tree and the target commit stays uncommitted, as with git reset --mixed, so history can be reworked and reapplied with withCommit — e.g. to amend the latest commit message, reset to its parent and commit again.
     *
     * With hard, the working tree is reset to the commit and every uncommitted change is discarded.
     *
     * Commits orphaned by the reset are not preserved: the frozen repository keeps reachable history only, so a reset cannot be undone by resetting forward again.
     * @param commit Commit to reset HEAD to, resolved against this workspace's repository like GitRepository.ref: a full commit hash, an unambiguous hex prefix (4-40 characters), or a ref name, optionally followed by revision suffixes such as HEAD~1, main^2 or abc1234~2. Only the commit it resolves to is used: a ref name selects its commit, it does not check out that ref.
     * @param opts.hard Discard uncommitted changes, resetting the working tree to the commit.
     */
    withReset: (commit: string, opts?: WorkspaceWithResetOpts) => Workspace;
    /**
     * Return this workspace with an SDK installed in its config.
     * @param ref SDK module reference to install.
     * @param opts.name Override name for the installed SDK entry.
     * @param opts.here Write to the workspace config directory at the workspace cwd.
     * @param opts.asSdkName Optional override for the SDK name conventionally derived from the installed module name.
     */
    withSdk: (ref: string, opts?: WorkspaceWithSdkOpts) => Workspace;
    /**
     * Return this workspace with the selected module clients updated.
     *
     * The engine re-reads the source of each selected client target and writes the lock entries that those targets reach.
     *
     * The selected SDK module then regenerates every scope that owns one of the targets.
     * @param opts.modules Recorded client targets to update. All targets in the selected scopes are updated when omitted.
     * @param opts.all Select clients in every scope instead of only the scopes containing the workspace cwd.
     * @param opts.sdk Optional SDK name. All installed SDK modules are selected when omitted.
     */
    withUpdatedClients: (opts?: WorkspaceWithUpdatedClientsOpts) => Workspace;
    /**
     * Return this workspace with refreshed lockfile state.
     *
     * SDK client scopes are regenerated unless noGenerate is true.
     * @param opts.noGenerate Do not regenerate SDK client scopes.
     */
    withUpdatedLock: (opts?: WorkspaceWithUpdatedLockOpts) => Workspace;
    /**
     * Return this workspace with updated module sources, versions and lockfile state.
     *
     * An SDK client scope is regenerated when it targets an updated module.
     * @param opts.names Installed module names or sources. A version suffix sets a new request. An empty list refreshes all installed modules.
     * @param opts.version New version request for exactly one selected module. Cannot be combined with a version suffix.
     * @param opts.source New source for exactly one selected module. Resolved like an install source. Cannot be combined with a version or a version suffix.
     */
    withUpdatedModules: (opts?: WorkspaceWithUpdatedModulesOpts) => Workspace;
    /**
     * Return this workspace with the calling client's user-level config re-read and applied.
     *
     * User-level config (the [workspaces.*] section of the Dagger config file) is read when a session loads its workspace, and snapshots keep that configuration. Call this to pick up edits made since, for example when an agent reloads its modules.
     *
     * The entry is matched by the workspace's git origin remote. A workspace without one, or without a matching entry, gets no user-level config.
     */
    withUserConfig: () => Workspace;
    /**
     * Return this workspace with its working directory pointed at the given workspace-relative path.
     * @param path Workspace-relative path to use as the working directory.
     */
    withWorkdir: (path: string) => Workspace;
    /**
     * Return this workspace with a module client removed from the deepest matching recorded scope.
     *
     * Fail if several SDKs have that deepest scope. The selected SDK module regenerates the complete scope.
     *
     * If invalid client targets remain, save the removal and skip generation until those targets are corrected or removed.
     * @param module The recorded target to remove.
     * @param opts.sdk Optional SDK name. Search all installed SDKs when omitted.
     */
    withoutClient: (module_: string, opts?: WorkspaceWithoutClientOpts) => Workspace;
    /**
     * Return this workspace with a named config environment removed.
     * @param name Environment name.
     * @param opts.here Write to the workspace config directory at the workspace cwd.
     */
    withoutConfigEnv: (name: string, opts?: WorkspaceWithoutConfigEnvOpts) => Workspace;
    /**
     * Return this workspace with a configuration value removed.
     *
     * Errors when the key is not currently set.
     *
     * When the session selects an env, the key is scoped to that env's overlay.
     * @param key Dotted key path (e.g. modules.greeter.settings.greeting).
     * @param opts.here Write to the workspace config directory at the workspace cwd.
     */
    withoutConfigValue: (key: string, opts?: WorkspaceWithoutConfigValueOpts) => Workspace;
    /**
     * Return this workspace with a directory removed, without mutating the source.
     * @param path Path of the directory to remove. Relative paths resolve from the workspace cwd.
     */
    withoutDirectory: (path: string) => Workspace;
    /**
     * Compatibility alias for withoutMain.
     * @deprecated Use withoutMain.
     */
    withoutEntrypoint: () => Workspace;
    /**
     * Return this workspace with a file removed, without mutating the source.
     * @param path Path of the file to remove. Relative paths resolve from the workspace cwd.
     */
    withoutFile: (path: string) => Workspace;
    /**
     * Return this workspace with files removed, without mutating the source.
     * @param paths Paths of the files to remove. Relative paths resolve from the workspace cwd.
     */
    withoutFiles: (paths: string[]) => Workspace;
    /**
     * Return this workspace with no module selected as its main.
     */
    withoutMain: () => Workspace;
    /**
     * Return this workspace with a module removed from its config.
     *
     * When the session selects an env, only that env's overlay entry is removed.
     * @param name Installed module name or source to remove. Version selectors are not accepted.
     * @param opts.here Write to the workspace config directory at the workspace cwd.
     */
    withoutModule: (name: string, opts?: WorkspaceWithoutModuleOpts) => Workspace;
    /**
     * Return this workspace with the content mounted at the given path unmounted.
     *
     * Removes directory and file mounts at or below the path, revealing the underlying workspace content. Other mounts and pending changes are preserved.
     * @param path Location of the mount to remove. Relative paths resolve from the workspace cwd. Use / to remove all mounts.
     */
    withoutMount: (path: string) => Workspace;
    /**
     * Return this workspace with an SDK removed from its config.
     * @param name Name of the installed SDK entry to remove.
     * @param opts.here Write to the workspace config directory at the workspace cwd.
     */
    withoutSdk: (name: string, opts?: WorkspaceWithoutSdkOpts) => Workspace;
    /**
     * Call the provided function with current Workspace.
     *
     * This is useful for reusability and readability by not breaking the calling chain.
     */
    with: (arg: (param: Workspace) => Workspace) => Workspace;
}
/**
 * A source commit classified against the receiving workspace.
 */
declare class WorkspaceCommitPick extends BaseClient {
    private readonly _id?;
    private readonly _reason?;
    private readonly _status?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _reason?: WorkspaceCommitPickReason, _status?: WorkspaceCommitPickStatus);
    /**
     * A unique identifier for this WorkspaceCommitPick.
     */
    id: () => Promise<ID>;
    /**
     * The commit in the source workspace.
     */
    commit: () => GitCommit;
    /**
     * Workspace-root-relative conflicting paths. Empty unless the status is CONFLICT.
     */
    conflictPaths: () => Promise<string[]>;
    /**
     * Why the commit conflicts, or NONE.
     */
    reason: () => Promise<WorkspaceCommitPickReason>;
    /**
     * Whether this commit can be applied.
     */
    status: () => Promise<WorkspaceCommitPickStatus>;
}
/**
 * Local git state for a workspace.
 */
declare class WorkspaceGit extends BaseClient {
    private readonly _id?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID);
    /**
     * A unique identifier for this WorkspaceGit.
     */
    id: () => Promise<ID>;
    /**
     * Return a self-contained Git metadata directory for this workspace's HEAD, including its full reachable history and an index matching HEAD.
     *
     * Mount this directory at .git alongside workspace.directory("/") to create a usable checkout. Pending workspace edits remain uncommitted; the original checkout's staging state is not preserved.
     *
     * This is a snapshot: Git writes to a mounted copy do not update the workspace. The workspace must have a Git repository with a HEAD commit.
     */
    directory: () => Directory;
    /**
     * The checked-out HEAD of this workspace.
     */
    head: () => GitRef;
    /**
     * Uncommitted changes in this workspace, using the same rules as GitRepository.uncommitted.
     */
    uncommitted: () => Changeset;
}
/**
 * A planned workspace migration.
 */
declare class WorkspaceMigration extends BaseClient {
    private readonly _id?;
    private readonly _configFile?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _configFile?: string);
    /**
     * A unique identifier for this WorkspaceMigration.
     */
    id: () => Promise<ID>;
    /**
     * Filesystem changes for the full migration plan.
     */
    changes: () => Changeset;
    /**
     * Native workspace config path after migration, relative to the workspace root. Empty if no workspace config exists.
     */
    configFile: () => Promise<string>;
    /**
     * Unselected legacy module directories relative to the workspace root. Candidates can include fixtures.
     */
    moduleCandidates: () => Promise<string[]>;
    /**
     * Logical migration steps, each identified by a stable code.
     */
    steps: () => Promise<WorkspaceMigrationStep[]>;
}
/**
 * A single logical part of a workspace migration.
 */
declare class WorkspaceMigrationStep extends BaseClient {
    private readonly _id?;
    private readonly _code?;
    private readonly _description?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _code?: string, _description?: string);
    /**
     * A unique identifier for this WorkspaceMigrationStep.
     */
    id: () => Promise<ID>;
    /**
     * Filesystem changes for this step.
     */
    changes: () => Changeset;
    /**
     * Stable code identifying this logical migration step.
     */
    code: () => Promise<string>;
    /**
     * Generic summary of this step's purpose and impact.
     */
    description: () => Promise<string>;
    /**
     * Non-fatal warnings raised while planning this step.
     */
    warnings: () => Promise<string[]>;
}
/**
 * A module entry in the workspace configuration.
 */
declare class WorkspaceModule extends BaseClient {
    private readonly _id?;
    private readonly _entrypoint?;
    private readonly _main?;
    private readonly _name?;
    private readonly _source?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _entrypoint?: boolean, _main?: boolean, _name?: string, _source?: string);
    /**
     * A unique identifier for this WorkspaceModule.
     */
    id: () => Promise<ID>;
    /**
     * Compatibility alias for main.
     * @deprecated Use main.
     */
    entrypoint: () => Promise<boolean>;
    /**
     * List the functions of this module's main object, in GraphQL field form.
     */
    functions: () => Promise<string[]>;
    /**
     * Whether the module is the workspace main (functions aliased to Query root).
     */
    main: () => Promise<boolean>;
    /**
     * The module name.
     */
    name: () => Promise<string>;
    /**
     * List constructor-backed settings for this module.
     */
    settings: () => Promise<WorkspaceModuleSetting[]>;
    /**
     * The module source path.
     */
    source: () => Promise<string>;
}
/**
 * A constructor-backed module setting.
 */
declare class WorkspaceModuleSetting extends BaseClient {
    private readonly _id?;
    private readonly _defaultValue?;
    private readonly _description?;
    private readonly _isList?;
    private readonly _isObject?;
    private readonly _isString?;
    private readonly _key?;
    private readonly _value?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _defaultValue?: string, _description?: string, _isList?: boolean, _isObject?: boolean, _isString?: boolean, _key?: string, _value?: string);
    /**
     * A unique identifier for this WorkspaceModuleSetting.
     */
    id: () => Promise<ID>;
    /**
     * The constructor argument's declared default, formatted like value, or empty when the argument has no default.
     */
    defaultValue: () => Promise<string>;
    /**
     * The constructor argument description.
     */
    description: () => Promise<string>;
    /**
     * Whether the setting accepts a list of values.
     */
    isList: () => Promise<boolean>;
    /**
     * Whether the setting is an object type resolved from an address string (Container, Directory, File, Secret, Service, ...), which may be a module reference.
     */
    isObject: () => Promise<boolean>;
    /**
     * Whether the setting is a string argument, stored as a TOML string even when the value reads as a number or boolean.
     */
    isString: () => Promise<boolean>;
    /**
     * The setting key.
     */
    key: () => Promise<string>;
    /**
     * The value stored in workspace config after applying the selected workspace environment, or empty when unset.
     */
    value: () => Promise<string>;
}
/**
 * An installed SDK: a module marked for scaffolding other modules and clients.
 */
declare class WorkspaceSDK extends BaseClient {
    private readonly _id?;
    private readonly _name?;
    private readonly _ref?;
    /**
     * Constructor is used for internal usage only, do not create object from it.
     */
    constructor(ctx?: Context, _id?: ID, _name?: string, _ref?: string);
    /**
     * A unique identifier for this WorkspaceSDK.
     */
    id: () => Promise<ID>;
    /**
     * Clients generated with this SDK.
     */
    clients: () => Promise<WorkspaceModule[]>;
    /**
     * Generate the modules and clients managed by this SDK.
     */
    generate: () => Changeset;
    /**
     * Modules authored with this SDK.
     */
    modules: () => Promise<WorkspaceModule[]>;
    /**
     * The user-facing SDK name.
     */
    name: () => Promise<string>;
    /**
     * The module reference this SDK was installed from.
     */
    ref: () => Promise<string>;
    /**
     * The value a setting records in this SDK's scope settings, as a native JSON value.
     *
     * Reads [sdks.<name>.scopes."<scope>".settings] from the workspace config, in the scope whose key resolves to path. The setting key is matched without regard to case.
     *
     * Null when no scope resolves to path, or the scope records no such setting. Fails when the config is invalid, path escapes the workspace root, key matches several recorded settings, or the value cannot be represented as JSON.
     *
     * Fails when this SDK is no longer installed in the workspace config, even if Workspace.sdk returned it earlier.
     * @param path Scope path, relative to the workspace cwd or absolute from the workspace root.
     * @param key Setting name, matched without regard to case.
     */
    scopeSetting: (path: string, key: string) => Promise<JSONValue | null>;
}
declare const dag: Client;

type CallbackFct = (client: Client) => Promise<void>;
/**
 * connect runs GraphQL server and initializes a
 * GraphQL client to execute query on it through its callback.
 * This implementation is based on the existing Go SDK.
 */
declare function connect(cb: CallbackFct, config?: ConnectOpts): Promise<void>;

/**
 * connection executes the given function using the default global Dagger client.
 *
 * @example
 * ```ts
 * await connection(
 *   async () => {
 *     await dag
 *       .container()
 *       .from("alpine")
 *       .withExec(["apk", "add", "curl"])
 *       .withExec(["curl", "https://dagger.io/"])
 *       .sync()
 *   }, { LogOutput: process.stderr }
 * )
 * ```
 */
declare function connection(fct: () => Promise<void>, cfg?: ConnectOpts): Promise<void>;

type Class = {
    new (...args: any[]): any;
};
type ArgumentOptions = {
    /**
     * The contextual value to use for the argument.
     *
     * This should only be used for Directory/File or GitRepository/GitRef types.
     *
     * An absolute path would be related to the context source directory (the git repo root or the module source root).
     * A relative path would be relative to the module source root.
     */
    defaultPath?: string;
    /**
     * The default container address to use for the argument.
     *
     * This should only be used for Container types.
     *
     * If the argument is not set, the container will be loaded from this address.
     */
    defaultAddress?: string;
    /**
     * Patterns to ignore when loading the contextual argument value.
     *
     * This should only be used for Directory types.
     */
    ignore?: string[];
};
type FunctionOptions = {
    /**
     * The caching behavior of this function.
     * "never" means no caching.
     * "session" means caching only for the duration of the current client's session.
     * A duration string (e.g., "5m", "1h") means persistent caching for that duration.
     * By default, caching is enabled with a long default set by the engine.
     */
    cache?: "never" | "session" | string;
    /**
     * An optional alias to use for the function when exposed on the API.
     */
    alias?: string;
};
/**
 * Retrieve a class registered via the `@object()` decorator by its name.
 *
 * Useful for generated code (e.g. the static dispatch entrypoint) when the
 * user's class isn't `export`'d but still needs to be reachable to construct
 * instances and walk its prototype.
 */
declare function getRegisteredClass(name: string): Class | undefined;

/**
 * The definition of the `@object` decorator that should be on top of any
 * class module that must be exposed to the Dagger API.
 *
 */
declare const object: () => (<T extends Class>(constructor: T) => T);
/** Declare a collection object. */
declare const collection: () => (<T extends Class>(constructor: T) => T);
/** Select the stored keys field. */
declare const keys: () => PropertyDecorator;
/** Select the item lookup method. */
declare const get: () => MethodDecorator;
/** Select the field that receives the collection delta. */
declare const delta: () => PropertyDecorator;
/**
 * The definition of @func decorator that should be on top of any
 * class' method that must be exposed to the Dagger API.
 *
 * @param alias The alias to use for the field when exposed on the API.
 * @param cache The cache setting to use for that function.
 */
declare const func: (opts?: FunctionOptions | string) => ((target: object, propertyKey: string | symbol, descriptor?: PropertyDescriptor) => void);
/**
 * The definition of @check decorator that marks a function as a check.
 * Checks are functions that return void/error to indicate pass/fail.
 */
declare const check: () => ((target: object, propertyKey: string | symbol, descriptor?: PropertyDescriptor) => void);
/**
 * The definition of @generate decorator that marks a function as a generator.
 * Generators are functions that return a Changeset representing changes to be applied.
 */
declare const generate: () => ((target: object, propertyKey: string | symbol, descriptor?: PropertyDescriptor) => void);
/**
 * The definition of @up decorator that marks a function as a service for dagger up.
 */
declare const up: () => ((target: object, propertyKey: string, descriptor: PropertyDescriptor) => PropertyDescriptor);
/**
 * The definition of @agent decorator that marks a function as an agent
 * middleware: it takes a base LLM and returns an LLM with the module's tools
 * and prompting folded onto it. `dagger agent` discovers and composes these.
 *
 * Besides the base LLM, an agent function may not declare any other required
 * argument.
 */
declare const agent: () => ((target: object, propertyKey: string | symbol, descriptor?: PropertyDescriptor) => void);
/**
 * The definition of @field decorator that should be on top of any
 * class' property that must be exposed to the Dagger API.
 *
 * @deprecated In favor of `@func`
 * @param alias The alias to use for the field when exposed on the API.
 */
declare const field: (alias?: string) => ((target: object, propertyKey: string) => void);
/**
 * The definition of the `@enumType` decorator that should be on top of any
 * class module that must be exposed to the Dagger API as enumeration.
 *
 * @deprecated In favor of using TypeScript `enum` types.
 */
declare const enumType: () => (<T extends Class>(constructor: T) => T);
/**
 * Add a `@argument` decorator to an argument of type `Directory` or `File` to load
 * its contents from the module context directory.
 *
 * The context directory is the git repository containing the module.
 * If there's no git repository, the context directory is the directory containing
 * the module source code.
 *
 * @param opts.defaultPath Only applies to arguments of type File or Directory. If the argument is not set,
 * load it from the given path in the context directory.
 * @param opts.ignore Only applies to arguments of type Directory. The ignore patterns are applied to the input directory,
 * and matching entries are filtered out, in a cache-efficient manner..
 *
 * Relative paths are relative to the current source files.
 * Absolute paths are rooted to the module context directory.
 */
declare const argument: (opts?: ArgumentOptions) => ((target: object, propertyKey: string | undefined, parameterIndex: number) => void);

export { AcronymStyle, AcronymStyleNameToValue, AcronymStyleValueToName, Address, Agent, AgentMessage, AgentMessageDelivery, AgentMessageDeliveryNameToValue, AgentMessageDeliveryValueToName, AgentState, AgentStateNameToValue, AgentStateValueToName, Artifact, ArtifactDimension, ArtifactDimensionKey, ArtifactDimensionKind, ArtifactDimensionKindNameToValue, ArtifactDimensionKindValueToName, ArtifactPath, Artifacts, BaseClient, CacheSharingMode, CacheSharingModeNameToValue, CacheSharingModeValueToName, CacheVolume, Casing, CasingNameToValue, CasingValueToName, Changeset, ChangesetMergeConflict, ChangesetMergeConflictNameToValue, ChangesetMergeConflictValueToName, ChangesetsMergeConflict, ChangesetsMergeConflictNameToValue, ChangesetsMergeConflictValueToName, Check, Client, ClientFilesyncMirror, Cloud, CollectionDelta, CollectionTypeDef, Command, Connection, Container, Context, CurrentModule, DaggerSDKError, DiffStat, DiffStatKind, DiffStatKindNameToValue, DiffStatKindValueToName, Directory, DockerImageRefValidationError, ERROR_CODES, Engine, EngineCache, EngineCacheEntry, EngineCacheEntrySet, EngineSessionConnectParamsParseError, EngineSessionConnectionTimeoutError, EngineSessionError, EnumTypeDef, EnumValueTypeDef, EnvFile, EnvVariable, Error$1 as Error, ErrorValue, ExecError, ExistsType, ExistsTypeNameToValue, ExistsTypeValueToName, Expertise, FieldTypeDef, File, FileType, FileTypeNameToValue, FileTypeValueToName, FunctionArg, FunctionCachePolicy, FunctionCachePolicyNameToValue, FunctionCachePolicyValueToName, FunctionCall, FunctionCallArgValue, FunctionNotFound, Function_, GeneratedCode, Generator, GitBundle, GitBundleRef, GitCommit, GitPushDisposition, GitPushDispositionNameToValue, GitPushDispositionValueToName, GitPushResult, GitRef, GitRemote, GitRepository, GraphQLClient, GraphQLRequestError, HTTPState, HealthcheckConfig, Host, Identifier, IdentifierWord, IdentifierWordKind, IdentifierWordKindNameToValue, IdentifierWordKindValueToName, ImageLayerCompression, ImageLayerCompressionNameToValue, ImageLayerCompressionValueToName, ImageMediaTypes, ImageMediaTypesNameToValue, ImageMediaTypesValueToName, InitEngineSessionBinaryError, InputTypeDef, InterfaceTypeDef, IntrospectionError, JSONValue, LLM, LLMContent, LLMContentBlock, LLMContentBlockKind, LLMContentBlockKindNameToValue, LLMContentBlockKindValueToName, LLMMessage, LLMMessageOrigin, LLMMessageOriginKind, LLMMessageOriginKindNameToValue, LLMMessageOriginKindValueToName, LLMMessageRole, LLMMessageRoleNameToValue, LLMMessageRoleValueToName, LLMSkill, LLMTokenUsage, Label, ListTypeDef, ModuleConfigClient, ModuleNaming, ModuleSource, ModuleSourceExperimentalFeature, ModuleSourceExperimentalFeatureNameToValue, ModuleSourceExperimentalFeatureValueToName, ModuleSourceKind, ModuleSourceKindNameToValue, ModuleSourceKindValueToName, Module_, Naming, NamingTerm, NetworkProtocol, NetworkProtocolNameToValue, NetworkProtocolValueToName, NotAwaitedRequestError, ObjectTypeDef, PatchConflict, PatchConflictNameToValue, PatchConflictValueToName, Port, RegistryProtocol, RegistryProtocolNameToValue, RegistryProtocolValueToName, RemoteGitMirror, ReturnType, ReturnTypeNameToValue, ReturnTypeValueToName, SDKConfig, ScalarTypeDef, Schema, SearchResult, SearchSubmatch, Secret, Service, Socket, SourceMap, Stat, Terminal, TooManyNestedObjectsError, TypeDef, TypeDefKind, TypeDefKindNameToValue, TypeDefKindValueToName, UnknownDaggerError, Volume, Workspace, WorkspaceCommitPick, WorkspaceCommitPickReason, WorkspaceCommitPickReasonNameToValue, WorkspaceCommitPickReasonValueToName, WorkspaceCommitPickStatus, WorkspaceCommitPickStatusNameToValue, WorkspaceCommitPickStatusValueToName, WorkspaceGit, WorkspaceMigration, WorkspaceMigrationStep, WorkspaceModule, WorkspaceModuleSetting, WorkspaceSDK, _ExportableClient, _NodeClient, _SyncerClient, agent, argument, check, collection, computeQuery, connect, connection, dag, delta, enumType, field, func, generate, get, getRegisteredClass, getTracer, globalConnection, keys, object, shared, up, withGQLClient, withSession };
export type { AddressContainerOpts, AddressDirectoryOpts, AddressFileOpts, AddressGitRefOpts, AgentNotifyOpts, AgentPauseOpts, AgentSendOpts, AgentStopOpts, ArtifactUriOpts, ArtifactValueOpts, ArtifactsFilterDirectivesOpts, ArtifactsFilterParentDirectivesOpts, ArtifactsFilterParentTypesOpts, ArtifactsFilterTypesOpts, ArtifactsPathDefinitionsOpts, BuildArg, Bytes, CallbackFct, ChangesetFilterOpts, ChangesetWithChangesetOpts, ChangesetWithChangesetsOpts, ClientBlobOpts, ClientCacheVolumeOpts, ClientContainerOpts, ClientCurrentTypeDefsOpts, ClientEngineVolumeOpts, ClientEnvFileOpts, ClientFileOpts, ClientGitOpts, ClientHttpOpts, ClientLLMOpts, ClientModuleSourceOpts, ClientNamingOpts, ClientSecretOpts, ClientServeModuleOpts, ClientSshfsVolumeOpts, ConnectOpts, ContainerAsServiceOpts, ContainerAsTarballOpts, ContainerDirectoryOpts, ContainerExistsOpts, ContainerExportImageOpts, ContainerExportOpts, ContainerFileOpts, ContainerFromOpts, ContainerImportOpts, ContainerLayerOpts, ContainerManifestOpts, ContainerPublishOpts, ContainerShellOpts, ContainerStatOpts, ContainerTerminalOpts, ContainerUpOpts, ContainerWithDefaultTerminalCmdOpts, ContainerWithDirectoryOpts, ContainerWithDockerHealthcheckOpts, ContainerWithEntrypointOpts, ContainerWithEnvVariableOpts, ContainerWithExecOpts, ContainerWithExposedPortOpts, ContainerWithFileOpts, ContainerWithFilesOpts, ContainerWithMountedCacheOpts, ContainerWithMountedDirectoryOpts, ContainerWithMountedFileOpts, ContainerWithMountedFilesOpts, ContainerWithMountedSecretOpts, ContainerWithMountedTempOpts, ContainerWithMountedVolumeOpts, ContainerWithNewFileOpts, ContainerWithRunOpts, ContainerWithShellOpts, ContainerWithSymlinkOpts, ContainerWithUnixSocketOpts, ContainerWithWorkdirOpts, ContainerWithoutDirectoryOpts, ContainerWithoutEntrypointOpts, ContainerWithoutExposedPortOpts, ContainerWithoutFileOpts, ContainerWithoutFilesOpts, ContainerWithoutMountOpts, ContainerWithoutUnixSocketOpts, CurrentModuleWorkdirOpts, DirectoryAsModuleOpts, DirectoryAsModuleSourceOpts, DirectoryAsWorkspaceOpts, DirectoryDirectoryOpts, DirectoryDockerBuildOpts, DirectoryEntriesOpts, DirectoryExistsOpts, DirectoryExportOpts, DirectoryFilterOpts, DirectorySearchOpts, DirectoryStatOpts, DirectoryTerminalOpts, DirectoryWithDirectoryOpts, DirectoryWithFileOpts, DirectoryWithFilesOpts, DirectoryWithNewDirectoryOpts, DirectoryWithNewFileOpts, DirectoryWithPatchFileOpts, DirectoryWithPatchOpts, EngineCacheEntrySetOpts, EngineCachePruneOpts, EnvFileGetOpts, EnvFileVariablesOpts, Exportable, FileAsEnvFileOpts, FileContentsOpts, FileDigestOpts, FileExportOpts, FileExtractOpts, FileSearchOpts, FileSyncOpts, FileWithReplacedOpts, FunctionWithArgOpts, FunctionWithCachePolicyOpts, FunctionWithDeprecatedOpts, GitCommitAncestorReleaseTagOpts, GitCommitChangesOpts, GitCommitReleaseTagOpts, GitCommitTreeOpts, GitRefAsWorkspaceOpts, GitRefLogOpts, GitRefPushOpts, GitRefTreeOpts, GitRefWithCommitOpts, GitRepositoryAsWorkspaceOpts, GitRepositoryBranchOpts, GitRepositoryBranchesOpts, GitRepositoryBundleOpts, GitRepositoryHeadOpts, GitRepositoryLatestOpts, GitRepositoryRefOpts, GitRepositoryTagOpts, GitRepositoryTagsOpts, GitRepositoryWithBundleOpts, GitRepositoryWithRemoteOpts, HostDirectoryOpts, HostFileOpts, HostFindUpOpts, HostServiceOpts, HostTunnelOpts, ID, IdentifierFormatOpts, JSON, JSONValueContentsOpts, LLMArtifactsOpts, LLMContentBlockInput, LLMContentWithFileOpts, LLMLoopOpts, LLMMessageOriginInput, LLMSpawnOpts, LLMStepOpts, LLMWithContentFileOpts, LLMWithContentOpts, LLMWithModelOpts, LLMWithPromptOpts, LLMWithResponseOpts, LLMWithToolResultOpts, LLMWithToolsOpts, Metadata, ModuleServeOpts, ModuleSourceClientSchemaIntrospectionJsonOpts, NamingFormatIdentifiersOpts, NamingModuleOpts, Node, PipelineLabel, Platform, PortForward, QueryTree, ServeSpec, ServiceEndpointOpts, ServicePortsOpts, ServiceStopOpts, ServiceTerminalOpts, ServiceUpOpts, Syncer, TypeDefWithEnumMemberOpts, TypeDefWithEnumOpts, TypeDefWithEnumValueOpts, TypeDefWithFieldOpts, TypeDefWithInterfaceOpts, TypeDefWithObjectOpts, TypeDefWithScalarOpts, Void, WorkspaceArtifactsOpts, WorkspaceChangesOpts, WorkspaceCompareCommitsFromOpts, WorkspaceConfigReadOpts, WorkspaceDirectoryOpts, WorkspaceExportOpts, WorkspaceFindRootsOpts, WorkspaceFindUpOpts, WorkspaceMigrateModuleOpts, WorkspaceMigrateOpts, WorkspaceSearchOpts, WorkspaceWithClientOpts, WorkspaceWithCommitOpts, WorkspaceWithCommitsFromOpts, WorkspaceWithConfigEnvOpts, WorkspaceWithConfigValueOpts, WorkspaceWithFileOpts, WorkspaceWithInitModuleOpts, WorkspaceWithModuleOpts, WorkspaceWithNewFileOpts, WorkspaceWithPatchFileOpts, WorkspaceWithResetOpts, WorkspaceWithSdkOpts, WorkspaceWithUpdatedClientsOpts, WorkspaceWithUpdatedLockOpts, WorkspaceWithUpdatedModulesOpts, WorkspaceWithoutClientOpts, WorkspaceWithoutConfigEnvOpts, WorkspaceWithoutConfigValueOpts, WorkspaceWithoutModuleOpts, WorkspaceWithoutSdkOpts, __DirectiveArgsOpts, __FieldArgsOpts, __TypeEnumValuesOpts, __TypeFieldsOpts, __TypeInputFieldsOpts, float };
