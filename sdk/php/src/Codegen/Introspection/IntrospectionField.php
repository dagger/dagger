<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

class IntrospectionField
{
    public string $name;
    public ?string $description;
    public IntrospectionTypeRef $type;
    /** @var IntrospectionInputValue[] */
    public array $args = [];
    public bool $isDeprecated = false;
    public ?string $deprecationReason = null;
    /** @var IntrospectionDirective[] */
    public array $directives = [];

    /** @var string|null Set during codegen to track the parent type name */
    public ?string $parentTypeName = null;

    public static function fromArray(array $data): self
    {
        $field = new self();
        $field->name = $data['name'];
        $field->description = $data['description'] ?? null;
        $field->type = IntrospectionTypeRef::fromArray($data['type']);
        $field->isDeprecated = $data['isDeprecated'] ?? false;
        $field->deprecationReason = $data['deprecationReason'] ?? null;

        foreach ($data['args'] ?? [] as $argData) {
            $field->args[] = IntrospectionInputValue::fromArray($argData);
        }
        foreach ($data['directives'] ?? [] as $dirData) {
            $field->directives[] = IntrospectionDirective::fromArray($dirData);
        }

        return $field;
    }

    public function expectedType(): ?string
    {
        return IntrospectionDirective::getExpectedType($this->directives);
    }

    /**
     * Returns true if this field returns an ID handle: an ID the generated
     * method loads as an object instead of exposing (see idHandleType()).
     */
    public function isConvertID(): bool
    {
        return $this->idHandleType() !== null;
    }

    /**
     * The GraphQL type an ID-returning field loads, or null when the ID is
     * returned as-is (including the id field itself).
     *
     * The @expectedType directive names it: sync-likes return their parent,
     * and LLM.spawn returns an Agent rather than its ID.
     */
    public function idHandleType(): ?string
    {
        if ($this->name === 'id') {
            return null;
        }
        $ref = $this->type;
        if ($ref->kind === 'NON_NULL') {
            $ref = $ref->ofType;
        }
        if ($ref->kind !== 'SCALAR') {
            return null;
        }
        return $this->expectedType();
    }
}
