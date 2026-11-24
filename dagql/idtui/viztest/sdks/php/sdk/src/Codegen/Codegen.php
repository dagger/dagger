<?php

namespace Dagger\Codegen;

use Dagger\Codegen\Introspection\IntrospectionDirective;
use Dagger\Codegen\Introspection\IntrospectionField;
use Dagger\Codegen\Introspection\IntrospectionSchema;
use Dagger\Codegen\Introspection\IntrospectionType;
use Dagger\Codegen\Introspection\NewCodegenVisitor;
use RuntimeException;
use Symfony\Component\Console\Style\SymfonyStyle;

class Codegen
{
    private const RESERVED_CLASS_NAMES = [
        'abstract', 'and', 'array', 'as', 'bool', 'break', 'callable', 'case', 'catch', 'class', 'clone',
        'const', 'continue', 'declare', 'default', 'die', 'do', 'echo', 'else', 'elseif', 'empty',
        'enddeclare', 'endfor', 'endforeach', 'endif', 'endswitch', 'endwhile', 'eval', 'exit', 'extends',
        'false', 'final', 'finally', 'float', 'fn', 'for', 'foreach', 'global', 'goto', 'if', 'implements',
        'include', 'include_once', 'instanceof', 'insteadof', 'int', 'interface', 'isset', 'iterable', 'list',
        'match', 'mixed', 'namespace', 'never', 'new', 'null', 'object', 'or', 'parent', 'print', 'private',
        'protected', 'public', 'readonly', 'require', 'require_once', 'return', 'self', 'static', 'string',
        'switch', 'throw', 'trait', 'true', 'try', 'unset', 'use', 'var', 'void', 'while', 'xor', 'yield',
    ];

    public function __construct(
        private readonly IntrospectionSchema $schema,
        private readonly string $writeDir,
        private readonly SymfonyStyle $io,
        private readonly ?string $module = null,
        private readonly ?string $address = null,
        private readonly ?string $pin = null,
    ) {
    }

    public function generate(): void
    {
        $interfaceNames = [];
        foreach ($this->schema->types as $type) {
            if ($type->kind === 'INTERFACE') {
                $interfaceNames[] = $type->name;
            }
        }
        $filteredTypes = $this->ownTypes();
        [$namespace, $writeDir] = $this->destination();
        $visitor = new NewCodegenVisitor(
            $writeDir,
            $this->schema->supportsNullableObjects(),
            $interfaceNames,
            $namespace,
            $this->classNamespaces(),
            $this->address,
            $this->pin,
        );

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

    /** @return array{string, string} the namespace and the directory to generate into */
    private function destination(): array
    {
        if ($this->module === null) {
            return [CodeWriter::NAMESPACE, $this->writeDir];
        }
        $name = self::clientName($this->module);
        return [
            "Dagger\\Clients\\$name",
            $this->writeDir . DIRECTORY_SEPARATOR . 'Clients' . DIRECTORY_SEPARATOR . $name,
        ];
    }

    /** @return IntrospectionType[] the types the target owns, with only the fields it owns */
    private function ownTypes(): array
    {
        $ownTypes = [];
        foreach ($this->schema->types as $type) {
            $owner = self::sourceModule($type->directives);
            $ownType = clone $type;
            $ownType->fields = array_values(array_filter(
                $type->fields,
                fn (IntrospectionField $field) => (self::sourceModule($field->directives) ?? $owner) === $this->module,
            ));
            if (str_starts_with($type->name, '_')) {
                continue;
            }
            if ($owner !== $this->module) {
                if ($ownType->fields === []) {
                    continue;
                }
                if ($type->name !== 'Query') {
                    throw new RuntimeException(
                        "The client {$this->module} contributes {$type->name}.{$ownType->fields[0]->name}, " .
                        "and a client can only contribute fields to Query"
                    );
                }
            }
            $className = NewCodegenVisitor::formatPhpClassName($type->name);
            if ($this->module !== null && in_array(strtolower($className), self::RESERVED_CLASS_NAMES, true)) {
                throw new RuntimeException(
                    "The type {$type->name} would generate the class $className, which PHP reserves"
                );
            }
            $ownTypes[] = $ownType;
        }
        return $ownTypes;
    }

    /** @return array<string, string> the namespace of each class a client owns, by class name */
    private function classNamespaces(): array
    {
        $classNamespaces = [];
        foreach ($this->schema->types as $type) {
            $owner = self::sourceModule($type->directives);
            if ($owner === null) {
                continue;
            }
            $className = NewCodegenVisitor::formatPhpClassName($type->name);
            $classNamespaces[$className] = 'Dagger\\Clients\\' . self::clientName($owner);
            if ($type->isInterface()) {
                // visitInterface also writes <Name>Client, which generated code references by that class name.
                $classNamespaces[$className . 'Client'] = $classNamespaces[$className];
            }
        }
        return $classNamespaces;
    }

    /**
     * @param IntrospectionDirective[] $directives
     */
    private static function sourceModule(array $directives): ?string
    {
        foreach ($directives as $directive) {
            if ($directive->name === 'sourceMap') {
                $raw = $directive->arg('module');
                return $raw === null ? null : (json_decode($raw, true) ?: null);
            }
        }
        return null;
    }

    private static function clientName(string $module): string
    {
        $pascalCase = str_replace(' ', '', ucwords(preg_replace('/[^A-Za-z0-9]+/', ' ', $module)));
        return NewCodegenVisitor::formatPhpClassName($pascalCase);
    }
}
