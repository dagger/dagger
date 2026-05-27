<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

class IntrospectionSchema
{
    /** @var IntrospectionType[] */
    public array $types = [];

    public static function fromArray(array $data): self
    {
        $schema = new self();
        foreach ($data['__schema']['types'] ?? [] as $typeData) {
            $schema->types[] = IntrospectionType::fromArray($typeData);
        }
        return $schema;
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
