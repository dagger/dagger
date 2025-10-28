<?php

declare(strict_types=1);

namespace Dagger\Client;

abstract readonly class AbstractScalar implements \Stringable
{
    final public function __construct(
        private string $value,
    ) {
    }

    public function getValue(): string
    {
        return $this->value;
    }

    public function __toString(): string
    {
        return $this->value;
    }

    final public static function from(string $value): static
    {
        return new static($value);
    }
}
