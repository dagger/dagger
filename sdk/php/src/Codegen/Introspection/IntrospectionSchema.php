<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

use Dagger\Codegen\Naming\FormattedNames;

class IntrospectionSchema
{
    public ?string $version = null;

    /** @var IntrospectionType[] */
    public array $types = [];

    /**
     * The schema's names as the engine formatted them. Null when they
     * weren't formatted: the schema has no Query.formatIdentifiers (engine
     * views before v1.0.0), or codegen got no names for it.
     */
    public ?FormattedNames $names = null;

    public static function fromArray(array $data): self
    {
        $schema = new self();
        $schema->version = $data['__schemaVersion'] ?? null;
        foreach ($data['__schema']['types'] ?? [] as $typeData) {
            $schema->types[] = IntrospectionType::fromArray($typeData);
        }
        return $schema;
    }

    public function supportsNullableObjects(): bool
    {
        if ($this->version === null || $this->version === '') {
            return true;
        }

        $version = ltrim($this->version, 'v');
        if (preg_match('/^\d+\.\d+\.\d+/', $version) !== 1) {
            return true;
        }
        if (preg_match('/^(\d+\.\d+\.\d+-beta\.\d+)/', $version, $matches) === 1) {
            $version = $matches[1];
        }

        return version_compare($version, '1.0.0-beta.10', '>=');
    }

    /**
     * Whether the schema has Query.formatIdentifiers: the gate for
     * formatting its names through the engine.
     */
    public function hasFormatIdentifiers(): bool
    {
        return $this->getType('Query')?->hasField('formatIdentifiers') ?? false;
    }

    /**
     * The distinct type, field, argument, input field and enum value names
     * in the schema, sorted, leaving out introspection names ("__" prefix)
     * and names with no letters or digits: the names codegen has the engine
     * format.
     *
     * @return list<string>
     */
    public function names(): array
    {
        $names = [];
        $add = static function (string $name) use (&$names): void {
            if (str_starts_with($name, '__') || preg_match('/[A-Za-z0-9]/', $name) !== 1) {
                return;
            }
            $names[$name] = true;
        };

        foreach ($this->types as $type) {
            if (str_starts_with($type->name, '__')) {
                continue;
            }
            $add($type->name);
            foreach ($type->fields as $field) {
                $add($field->name);
                foreach ($field->args as $arg) {
                    $add($arg->name);
                }
            }
            foreach ($type->inputFields as $inputField) {
                $add($inputField->name);
            }
            foreach ($type->enumValues as $enumValue) {
                $add($enumValue->name);
            }
        }

        $names = array_map('strval', array_keys($names));
        sort($names, SORT_STRING);

        return $names;
    }

    public function getType(string $name): ?IntrospectionType
    {
        foreach ($this->types as $type) {
            if ($type->name === $name) {
                return $type;
            }
        }
        return null;
    }
}
