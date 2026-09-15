<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

class IntrospectionInputValue
{
    public string $name;
    public ?string $description;
    public IntrospectionTypeRef $type;
    public ?string $defaultValue = null;
    public bool $isDeprecated = false;
    public ?string $deprecationReason = null;
    /** @var IntrospectionDirective[] */
    public array $directives = [];

    public static function fromArray(array $data): self
    {
        $input = new self();
        $input->name = $data['name'];
        $input->description = $data['description'] ?? null;
        $input->type = IntrospectionTypeRef::fromArray($data['type']);
        $input->defaultValue = $data['defaultValue'] ?? null;
        $input->isDeprecated = $data['isDeprecated'] ?? false;
        $input->deprecationReason = $data['deprecationReason'] ?? null;

        foreach ($data['directives'] ?? [] as $dirData) {
            $input->directives[] = IntrospectionDirective::fromArray($dirData);
        }

        return $input;
    }

    public function isRequired(): bool
    {
        return $this->type->kind === 'NON_NULL' && $this->defaultValue === null;
    }

    public function expectedType(): ?string
    {
        return IntrospectionDirective::getExpectedType($this->directives);
    }
}
