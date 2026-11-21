<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

class IntrospectionDirective
{
    public string $name;
    /** @var IntrospectionDirectiveArg[] */
    public array $args = [];

    public static function fromArray(array $data): self
    {
        $dir = new self();
        $dir->name = $data['name'];

        foreach ($data['args'] ?? [] as $argData) {
            $dir->args[] = IntrospectionDirectiveArg::fromArray($argData);
        }

        return $dir;
    }

    public function arg(string $name): ?string
    {
        foreach ($this->args as $arg) {
            if ($arg->name === $name) {
                return $arg->value;
            }
        }
        return null;
    }

    /**
     * @param IntrospectionDirective[] $directives
     */
    public static function getExpectedType(array $directives): ?string
    {
        foreach ($directives as $directive) {
            if ($directive->name === 'expectedType') {
                $value = $directive->arg('name');
                if ($value !== null) {
                    // The value comes JSON-encoded (with quotes)
                    $decoded = json_decode($value, true);
                    return is_string($decoded) ? $decoded : $value;
                }
            }
        }
        return null;
    }
}
