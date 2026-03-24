<?php

namespace Dagger\Codegen;

use Dagger\Codegen\Introspection\IntrospectionSchema;
use Dagger\Codegen\Introspection\IntrospectionType;
use Dagger\Codegen\Introspection\NewCodegenVisitor;
use Symfony\Component\Console\Style\SymfonyStyle;

class Codegen
{
    public function __construct(
        private readonly IntrospectionSchema $schema,
        private readonly string $writeDir,
        private readonly SymfonyStyle $io
    ) {
    }

    public function generate(): void
    {
        $visitor = new NewCodegenVisitor($this->schema, $this->writeDir);

        $filteredTypes = array_filter($this->schema->types, function (IntrospectionType $type) {
            return !str_starts_with($type->name, '_')
                && !str_starts_with($type->name, '__');
        });

        // Scalars (custom, not Void, not DateTime, not ID)
        $scalarTypes = array_filter($filteredTypes, function (IntrospectionType $type) {
            return $type->isScalar()
                && $type->name !== 'Void'
                && $type->name !== 'DateTime'
                && !in_array($type->name, ['String', 'Int', 'Float', 'Boolean'], true);
        });

        // Input objects
        $inputObjectTypes = array_filter($filteredTypes, function (IntrospectionType $type) {
            return $type->isInputObject();
        });

        // Enums
        $enumTypes = array_filter($filteredTypes, function (IntrospectionType $type) {
            return $type->isEnum();
        });

        // Objects
        $objectTypes = array_filter($filteredTypes, function (IntrospectionType $type) {
            return $type->isObject();
        });

        // Interfaces
        $interfaceTypes = array_filter($filteredTypes, function (IntrospectionType $type) {
            return $type->isInterface();
        });

        foreach ($scalarTypes as $type) {
            $this->io->info("Generating scalar '{$type->name}'");
            $visitor->visitScalar($type);
        }

        foreach ($inputObjectTypes as $type) {
            $this->io->info("Generating input object '{$type->name}'");
            $visitor->visitInput($type);
        }

        foreach ($enumTypes as $type) {
            $this->io->info("Generating enum object '{$type->name}'");
            $visitor->visitEnum($type);
        }

        foreach ($objectTypes as $type) {
            $this->io->info("Generating object '{$type->name}'");
            $visitor->visitObject($type);
        }

        foreach ($interfaceTypes as $type) {
            $this->io->info("Generating interface '{$type->name}'");
            $visitor->visitInterface($type);
        }
    }
}
