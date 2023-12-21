<?php

namespace Dagger\Codegen\Introspection;

use GraphQL\Type\Definition\EnumType;
use GraphQL\Type\Definition\InputObjectType;
use GraphQL\Type\Definition\ObjectType;
use GraphQL\Type\Definition\ScalarType;

interface SchemaVisitor
{
    public function visitScalar(ScalarType $type): void;

    public function visitObject(ObjectType $type): void;

    public function visitInput(InputObjectType $type): void;

    public function visitEnum(EnumType $type): void;
}
