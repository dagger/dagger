<?php

namespace Dagger\Codegen\Introspection;

use Dagger\Codegen\CodeWriter;
use GraphQL\Type\Definition\Type;
use GraphQL\Type\Schema;
use Nette\PhpGenerator\ClassType;
use Nette\PhpGenerator\EnumType;

abstract class AbstractVisitor extends CodeWriter
{
    public function __construct(protected readonly Schema $schema, string $targetDirectory)
    {
        parent::__construct($targetDirectory);
    }

    public function visit(Type $type): void
    {
        $this->write($this->generateType($type));
    }

    abstract public function generateType(Type $type): EnumType|ClassType;
}
