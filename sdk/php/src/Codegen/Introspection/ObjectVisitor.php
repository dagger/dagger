<?php

namespace DaggerIo\Codegen\Introspection;

use DaggerIo\Client\AbstractDaggerClient;
use DaggerIo\Client\AbstractDaggerObject;
use DaggerIo\Client\IdAble;
use GraphQL\Type\Definition\Argument;
use GraphQL\Type\Definition\FieldDefinition;
use GraphQL\Type\Definition\ListOfType;
use GraphQL\Type\Definition\NonNull;
use GraphQL\Type\Definition\ObjectType;
use GraphQL\Type\Definition\Type;
use Nette\PhpGenerator\ClassType;
use Nette\PhpGenerator\EnumType;
use Nette\PhpGenerator\Method;
use TypeError;

class ObjectVisitor extends AbstractVisitor
{
    public function generateType(Type $type): EnumType|ClassType
    {
        if (!$type instanceof ObjectType) {
            throw new TypeError('ObjectVisitor can only generate from ObjectType');
        }

        $parentClass = 'Query' === $type->name ? AbstractDaggerClient::class : AbstractDaggerObject::class;
        $className = 'Query' === $type->name ? 'DaggerClient' : $type->name;

        $objectClass = new ClassType(Helpers::formatPhpClassName($className));
        $objectClass->setExtends($parentClass);
        $objectClass->addComment($type->description);

        if ($type->hasField('id')) {
            $objectClass->addImplement(IdAble::class);
        }

        /**
         * @var FieldDefinition $field
         */
        foreach ($type->getFields() as $field) {
            $returnType = $field->getType();
            $returnTypeClassName = $this->generateOutputType($returnType);
            $fieldName = $field->name;

            $method = $objectClass->addMethod($fieldName);
            $method->addComment($field->description);

            if ($returnType instanceof NonNull) {
                $method->setReturnNullable(false);
                $returnType = $returnType->getWrappedType();
            }

            $method->setReturnType($returnTypeClassName);

            foreach ($field->args as $arg) {
                $this->generateMethodArgument($arg, $method);
            }

            // @TODO refactor

            if (Helpers::isScalar($returnType) || Helpers::isList($returnType) || Helpers::isEnumType($returnType)) {
                $method->addBody('$leafQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder(?);', [$fieldName]);
                $this->generateMethodArgumentsBody($method, $field->args, 'leafQueryBuilder');
                if (Helpers::isCustomScalar($returnType)) {
                    $method->addBody(
                        'return $this->queryLeafDaggerScalar($leafQueryBuilder, ?, '.$returnTypeClassName.'::class);',
                        [$fieldName]
                    );
                } elseif (Helpers::isEnumType($returnType)) {
                    $enumClass = Helpers::formatPhpFqcn(Helpers::formatType($returnType));
                    $method->addBody(
                        'return '.$enumClass.'::from($this->queryLeaf($leafQueryBuilder, ?));',
                        [$fieldName]
                    );
                } else {
                    $method->addBody(
                        'return $this->queryLeaf($leafQueryBuilder, ?);',
                        [$fieldName]
                    );
                }
            } else {
                $method->addBody('$innerQueryBuilder = new \DaggerIo\Client\DaggerQueryBuilder(?);', [$fieldName]);
                $this->generateMethodArgumentsBody($method, $field->args, 'innerQueryBuilder');
                $method->addBody(
                    'return new '.
                    Helpers::formatPhpFqcn(Helpers::formatType($returnType)).
                    '($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));',
                    []
                );
            }
        }

        return $objectClass;
    }

    private function generateOutputType(Type $type): string
    {
        if ($type instanceof ListOfType) {
            return 'array';
        } elseif (Helpers::isBuiltinScalar($type)) {
            return Helpers::formatType($type);
        } else {
            return Helpers::formatPhpFqcn(Helpers::formatType($type));
        }
    }

    private function generateInputType(Type $type): string
    {
        if ($type instanceof ListOfType) {
            return 'array';
        } elseif (Helpers::isBuiltinScalar($type)) {
            return Helpers::formatType($type);
        } elseif (Helpers::isDaggerIdType($type)) {
            $daggerIdTypeName = Helpers::formatPhpClassName(Helpers::formatType($type));
            $objectTypeName = Helpers::formatPhpClassName(str_replace('Id', '', $daggerIdTypeName));

            return Helpers::formatPhpFqcn($daggerIdTypeName).'|'.Helpers::formatPhpFqcn($objectTypeName);
        } else {
            return Helpers::formatPhpFqcn(Helpers::formatType($type));
        }
    }

    private function generateMethodArgument(Argument $arg, Method $method): void
    {
        $parameter = $method->addParameter($arg->name);

        if (!$arg->isRequired()) {
            $parameter->setNullable();
            $parameter->setDefaultValue(null);
        }

        if ($arg->defaultValueExists() && Helpers::isBuiltinScalar($arg->getType())) {
            $parameter->setDefaultValue($arg->defaultValue);
        }

        $parameter->setType($this->generateInputType($arg->getType()));
    }

    /**
     * @param array<Argument> $args
     */
    private function generateMethodArgumentsBody(Method $method, array $args, string $targetVar): void
    {
        foreach ($args as $arg) {
            $argName = $arg->name;
            if (!$arg->isRequired()) {
                $method->addBody('if (null !== $?) {', [$argName]);
            }
            $method->addBody('$?->setArgument(?, $?);', [$targetVar, $argName, $argName]);
            if (!$arg->isRequired()) {
                $method->addBody('}');
            }
        }
    }
}
