<?php

namespace Dagger\Codegen\Introspection;

use GraphQL\Type\Definition\Type;
use Nette\PhpGenerator\ClassType;
use Nette\PhpGenerator\EnumType;
use TypeError;

class EnumVisitor extends AbstractVisitor
{
    public function generateType(Type $type): EnumType|ClassType
    {
        if (!$type instanceof \GraphQL\Type\Definition\EnumType) {
            throw new TypeError('EnumVisitor can only generate from EnumType');
        }

        $enumClass = new EnumType(Helpers::formatPhpClassName($type->name));
        $enumClass->setType('string');
        $enumClass->addComment($type->description);

        foreach ($type->getValues() as $value) {
            $enumClass
                ->addCase($value->name, $value->value)
                ->addComment($value->description);
        }

        return $enumClass;
    }
}
