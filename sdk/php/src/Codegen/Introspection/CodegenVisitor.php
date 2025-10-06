<?php

namespace Dagger\Codegen\Introspection;

use GraphQL\Type\Definition\EnumType;
use GraphQL\Type\Definition\InputObjectType;
use GraphQL\Type\Definition\ObjectType;
use GraphQL\Type\Definition\ScalarType;
use GraphQL\Type\Schema;

class CodegenVisitor implements SchemaVisitor
{
    private ScalarVisitor $scalarVisitor;
    private InputVisitor $inputVisitor;
    private EnumVisitor $enumVisitor;
    private ObjectVisitor $objectVisitor;

    public function __construct(
        Schema $schema,
        string $targetDirectory
    ) {
        $this->scalarVisitor = new ScalarVisitor($schema, $targetDirectory);
        $this->inputVisitor = new InputVisitor($schema, $targetDirectory);
        $this->enumVisitor = new EnumVisitor($schema, $targetDirectory);
        $this->objectVisitor = new ObjectVisitor($schema, $targetDirectory);
    }

    public function visitScalar(ScalarType $type): void
    {
        $this->scalarVisitor->visit($type);
    }

    public function visitObject(ObjectType $type): void
    {
        $this->objectVisitor->visit($type);
    }

    public function visitInput(InputObjectType $type): void
    {
        $this->inputVisitor->visit($type);
    }

    public function visitEnum(EnumType $type): void
    {
        $this->enumVisitor->visit($type);
    }
}
