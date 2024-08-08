<?php

namespace Dagger\Codegen\Introspection;

use Dagger\Codegen\CodeWriter;
use DateTimeImmutable;
use GraphQL\Type\Definition\CustomScalarType;
use GraphQL\Type\Definition\EnumType;
use GraphQL\Type\Definition\InputObjectType;
use GraphQL\Type\Definition\ListOfType;
use GraphQL\Type\Definition\NamedType;
use GraphQL\Type\Definition\NonNull;
use GraphQL\Type\Definition\ObjectType;
use GraphQL\Type\Definition\ScalarType;
use GraphQL\Type\Definition\Type;
use TypeError;

class Helpers
{
    public static function formatPhpFqcn(string $className): string
    {
        return '\\' . CodeWriter::NAMESPACE . '\\' . $className;
    }

    public static function formatPhpClassName(string $objectName): string
    {
        $objectName = str_replace(['ID', 'JSON'], ['Id', 'Json'], $objectName);

        return match ($objectName) {
            'Function' => 'Function_',
            default => $objectName,
        };
    }

    public static function formatType(Type|NamedType $type): string
    {
        $typeName = null;

        if ($type instanceof NonNull) {
            return static::formatType($type->getWrappedType());
        }

        if ($type instanceof ListOfType) {
            return 'array';
        }

        if ($type instanceof ScalarType) {
            switch ($type->toString()) {
                case 'String':
                    return 'string';
                case 'Boolean':
                    return 'bool';
                case 'Int':
                    return 'int';
                case 'Float':
                    return 'float';
                case 'Void':
                    return 'void';
                case 'DateTime':
                    return DateTimeImmutable::class;
            }
        }

        if ($type instanceof NamedType) {
            $typeName = $type->name();
        }

        if ($type instanceof EnumType) {
            return Helpers::formatPhpClassName($typeName);
        }

        if ($type instanceof InputObjectType) {
            return Helpers::formatPhpClassName($typeName);
        }

        if ($type instanceof ObjectType) {
            if ('Query' === $typeName) {
                return 'Client';
            }

            return Helpers::formatPhpClassName($typeName);
        }

        if ($type instanceof CustomScalarType) {
            return Helpers::formatPhpClassName($typeName);
        }

        throw new TypeError("Cannot handle type {$type}");
    }

    public static function isNullable(Type $type): bool
    {
        return !($type instanceof NonNull);
    }

    public static function isDaggerIdType(Type $type): bool
    {
        if (!self::isCustomScalar($type)) {
            return false;
        }

        $type = $type instanceof NonNull ? $type->getWrappedType() : $type;

        return $type instanceof CustomScalarType && str_ends_with($type->name, 'ID');
    }

    public static function isCustomScalar(Type $type): bool
    {
        return $type instanceof NonNull
            ? $type->getWrappedType() instanceof CustomScalarType
            : $type instanceof CustomScalarType;
    }

    public static function isBuiltinScalar(Type $type): bool
    {
        if ($type instanceof NonNull) {
            $type = $type->getWrappedType();
        }

        if ($type instanceof ScalarType) {
            return in_array($type->name, array_keys(Type::getStandardTypes()));
        }

        return false;
    }

    public static function isScalar(Type $type): bool
    {
        return $type instanceof NonNull
            ? $type->getWrappedType() instanceof ScalarType
            : $type instanceof ScalarType;
    }

    public static function isList(Type $type): bool
    {
        return $type instanceof NonNull
            ? $type->getWrappedType() instanceof ListOfType
            : $type instanceof ListOfType;
    }

    public static function isEnumType(Type $type): bool
    {
        return $type instanceof NonNull
            ? $type->getWrappedType() instanceof EnumType
            : $type instanceof EnumType;
    }

    public static function isVoidType(Type $returnType): bool
    {
        return
            $returnType instanceof CustomScalarType
            && 'Void' === $returnType->name;
    }
}
