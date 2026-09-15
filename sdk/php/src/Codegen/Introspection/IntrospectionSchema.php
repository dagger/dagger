<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

class IntrospectionSchema
{
    public ?string $version = null;

    /** @var IntrospectionType[] */
    public array $types = [];

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
