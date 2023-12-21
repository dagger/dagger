<?php

namespace DaggerIo\Client;

use Stringable;

readonly class DaggerScalar implements Stringable
{
    public function __construct(private string $value)
    {
    }

    public function getValue(): string
    {
        return $this->value;
    }

    public function __toString(): string
    {
        return $this->value;
    }

    public static function from(string $value): static
    {
        return new static($value);
    }
}
