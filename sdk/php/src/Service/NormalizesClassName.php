<?php

declare(strict_types=1);

namespace Dagger\Service;

use Dagger\Attribute\GraphQLType;
use ReflectionClass;

final readonly class NormalizesClassName
{
    public static function trimLeadingNamespace(string $name): string
    {
        return preg_replace('#^\\\\?[^\\\\]+\\\\#', '', $name, 1);
    }

    public static function shorten(string $name): string
    {
        return preg_replace('#[^\\\\]+\\\\#', '', $name);
    }

    /**
     * The GraphQL type a generated class stands for, as its GraphQLType
     * attribute names it, or null when it has none.
     */
    public static function graphQLTypeName(string $className): ?string
    {
        if (
            !class_exists($className)
            && !interface_exists($className)
            && !enum_exists($className)
        ) {
            return null;
        }

        $attributes = (new ReflectionClass($className))->getAttributes(GraphQLType::class);
        if ($attributes === []) {
            return null;
        }

        return $attributes[0]->newInstance()->name;
    }
}
