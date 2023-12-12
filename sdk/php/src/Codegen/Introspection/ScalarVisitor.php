<?php

namespace Dagger\Codegen\Introspection;

use Dagger\Client\DaggerId;
use Dagger\Client\DaggerScalar;
use GraphQL\Type\Definition\ScalarType;
use GraphQL\Type\Definition\Type;
use Nette\PhpGenerator\ClassType;
use Nette\PhpGenerator\EnumType;
use TypeError;

class ScalarVisitor extends AbstractVisitor
{
    public function generateType(Type $type): EnumType|ClassType
    {
        if (!$type instanceof ScalarType) {
            throw new TypeError('ScalarVisitor can only generate from ScalarType');
        }

        $typeName = $type->name;
        $phpClassName = Helpers::formatPhpClassName($typeName);

        $scalarClass = new ClassType($phpClassName);
        $scalarClass->setReadOnly(true);
        $scalarClass->addComment($type->description);

        if (str_ends_with($typeName, 'ID')) {
            $scalarClass->setExtends(DaggerId::class);
        } else {
            $scalarClass->setExtends(DaggerScalar::class);
        }

        return $scalarClass;
    }
}
