<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

class IntrospectionEnumValue
{
    public string $name;
    public ?string $description;
    public bool $isDeprecated = false;
    public ?string $deprecationReason = null;
    /** @var IntrospectionDirective[] */
    public array $directives = [];

    public static function fromArray(array $data): self
    {
        $val = new self();
        $val->name = $data['name'];
        $val->description = $data['description'] ?? null;
        $val->isDeprecated = $data['isDeprecated'] ?? false;
        $val->deprecationReason = $data['deprecationReason'] ?? null;

        foreach ($data['directives'] ?? [] as $dirData) {
            $val->directives[] = IntrospectionDirective::fromArray($dirData);
        }

        return $val;
    }
}
