<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

class IntrospectionType
{
    public string $kind;
    public string $name;
    public ?string $description;
    /** @var IntrospectionField[] */
    public array $fields = [];
    /** @var IntrospectionInputValue[] */
    public array $inputFields = [];
    /** @var IntrospectionEnumValue[] */
    public array $enumValues = [];
    /** @var IntrospectionTypeRef[] */
    public array $interfaces = [];
    /** @var IntrospectionTypeRef[] */
    public array $possibleTypes = [];
    /** @var IntrospectionDirective[] */
    public array $directives = [];

    public static function fromArray(array $data): self
    {
        $type = new self();
        $type->kind = $data['kind'];
        $type->name = $data['name'] ?? '';
        $type->description = $data['description'] ?? null;

        foreach ($data['fields'] ?? [] as $fieldData) {
            $type->fields[] = IntrospectionField::fromArray($fieldData);
        }
        foreach ($data['inputFields'] ?? [] as $inputFieldData) {
            $type->inputFields[] = IntrospectionInputValue::fromArray($inputFieldData);
        }
        foreach ($data['enumValues'] ?? [] as $enumValueData) {
            $type->enumValues[] = IntrospectionEnumValue::fromArray($enumValueData);
        }
        foreach ($data['interfaces'] ?? [] as $ifaceData) {
            $type->interfaces[] = IntrospectionTypeRef::fromArray($ifaceData);
        }
        foreach ($data['possibleTypes'] ?? [] as $ptData) {
            $type->possibleTypes[] = IntrospectionTypeRef::fromArray($ptData);
        }
        foreach ($data['directives'] ?? [] as $dirData) {
            $type->directives[] = IntrospectionDirective::fromArray($dirData);
        }

        return $type;
    }

    public function isObject(): bool
    {
        return $this->kind === 'OBJECT';
    }

    public function isInterface(): bool
    {
        return $this->kind === 'INTERFACE';
    }

    public function isScalar(): bool
    {
        return $this->kind === 'SCALAR';
    }

    public function isEnum(): bool
    {
        return $this->kind === 'ENUM';
    }

    public function isInputObject(): bool
    {
        return $this->kind === 'INPUT_OBJECT';
    }

    public function hasField(string $name): bool
    {
        foreach ($this->fields as $field) {
            if ($field->name === $name) {
                return true;
            }
        }
        return false;
    }
}
