<?php

namespace Dagger\Codegen\Introspection;

use Dagger\Client\AbstractId;
use Dagger\Client\AbstractScalar;
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
            $scalarClass->setExtends(AbstractId::class);
        } else {
            $scalarClass->setExtends(AbstractScalar::class);
        }

        return $scalarClass;
    }
}
