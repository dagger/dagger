<?php

declare(strict_types=1);

namespace Dagger\Attribute;

use Attribute;

/**
 * Names the GraphQL type a generated class stands for, when the PHP class
 * name differs from it (JsonValue for JSONValue, Client for Query).
 *
 * Codegen adds it for schemas that carry identifier words. Without it, the
 * runtime assumes the GraphQL type is named like the class.
 */
#[Attribute(Attribute::TARGET_CLASS)]
final readonly class GraphQLType
{
    public function __construct(
        public string $name,
    ) {
    }
}
