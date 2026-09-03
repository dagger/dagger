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
    public function isConvertID(bool $supportsIdHandles = false): bool
    {
        return $this->idHandleType($supportsIdHandles) !== null;
    }

    /**
     * The GraphQL type an ID-returning field loads, or null when the ID is
     * returned as-is (including the id field itself).
     *
     * The @expectedType directive names it. Fields returning their parent's
     * own ID (sync-likes) have always been loaded as the parent, and once the
     * schema supports ID handles every expected type is loaded, so LLM.spawn
     * returns an Agent rather than its ID. Older views keep the parent-only
     * rule so their generated signatures do not move.
     */
    public function idHandleType(bool $supportsIdHandles = false): ?string
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
        $expectedType = $this->expectedType();
        if ($expectedType === null) {
            return null;
        }
        if ($expectedType === $this->parentTypeName || $supportsIdHandles) {
            return $expectedType;
        }
        return null;
    }
}
