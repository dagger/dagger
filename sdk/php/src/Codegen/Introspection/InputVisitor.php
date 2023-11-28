<?php

namespace DaggerIo\Codegen\Introspection;

use DaggerIo\Client\AbstractDaggerInputObject;
use GraphQL\Type\Definition\InputObjectType;
use GraphQL\Type\Definition\Type;
use Nette\PhpGenerator\ClassType;
use Nette\PhpGenerator\EnumType;
use TypeError;

class InputVisitor extends AbstractVisitor
{
    public function generateType(Type $type): EnumType|ClassType
    {
        if (!$type instanceof InputObjectType) {
            throw new TypeError('InputVisitor can only generate from InputObjectType');
        }

        $typeName = $type->name;

        $phpClassName = Helpers::formatPhpClassName($typeName);

        $inputObjectClass = new ClassType($phpClassName);
        $inputObjectClass->setExtends(AbstractDaggerInputObject::class);
        $inputObjectClass->addComment($type->description);

        $constructor = $inputObjectClass->addMethod('__construct');

        foreach ($type->getFields() as $field) {
            $fieldType = $field->getType();

            $constructorParameter = $constructor->addPromotedParameter($field->name);
            $constructorParameter->setType(Helpers::formatType($fieldType));
            $constructorParameter->setNullable(!$field->isRequired());
            $constructorParameter->setPublic();

            if ($field->defaultValueExists() && Helpers::isBuiltinScalar($fieldType)) {
                $constructorParameter->setDefaultValue($field->defaultValue);
            }
        }

        return $inputObjectClass;
    }
}
