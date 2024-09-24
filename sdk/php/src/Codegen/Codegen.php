<?php

namespace Dagger\Codegen;

use Dagger\Codegen\Introspection\CodegenVisitor;
use Dagger\Codegen\Introspection\Helpers;
use GraphQL\Type\Definition\CustomScalarType;
use GraphQL\Type\Definition\EnumType;
use GraphQL\Type\Definition\InputObjectType;
use GraphQL\Type\Definition\ObjectType;
use GraphQL\Type\Schema;
use Symfony\Component\Console\Style\SymfonyStyle;

class Codegen
{
    public function __construct(
        private readonly Schema $schema,
        private readonly string $writeDir,
        private readonly SymfonyStyle $io
    ) {
    }

    public function generate(): void
    {
        $schemaVisitor = new CodegenVisitor($this->schema, $this->writeDir);

        $filteredTypes = array_filter($this->schema->getTypeMap(), function ($type) {
            return !str_starts_with($type->name ?? '', '_');
        });

        $scalarTypes = array_filter($filteredTypes, function ($type) {
            return $type instanceof CustomScalarType
                && !Helpers::isVoidType($type)
                && 'DateTime' !== $type->name;
        });

        $inputObjectTypes = array_filter($filteredTypes, function ($type) {
            return $type instanceof InputObjectType;
        });

        $enumObjectTypes = array_filter($filteredTypes, function ($type) {
            return $type instanceof EnumType;
        });

        $objectTypes = array_filter($filteredTypes, function ($type) {
            return $type instanceof ObjectType;
        });

        foreach ($scalarTypes as $type) {
            $this->io->info("Generating scalar '{$type->name}'");
            $schemaVisitor->visitScalar($type);
        }

        foreach ($inputObjectTypes as $type) {
            $this->io->info("Generating input object '{$type->name}'");
            $schemaVisitor->visitInput($type);
        }

        foreach ($enumObjectTypes as $type) {
            $this->io->info("Generating enum object '{$type->name}'");
            $schemaVisitor->visitEnum($type);
        }

        foreach ($objectTypes as $type) {
            $this->io->info("Generating object '{$type->name}'");
            $schemaVisitor->visitObject($type);
        }
    }
}
