<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

class IntrospectionDirectiveArg
{
    public string $name;
    public ?string $value;

    public static function fromArray(array $data): self
    {
        $arg = new self();
        $arg->name = $data['name'];
        $arg->value = $data['value'] ?? null;
        return $arg;
    }
}
