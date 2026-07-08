<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use ReflectionEnumBackedCase;

/** @internal represents a single case of a string-backed enum exposed to Dagger. */
final readonly class DaggerEnumCase
{
    public function __construct(
        public string $name,
        public string $value,
        public string $description,
    ) {
    }

    public static function fromReflection(ReflectionEnumBackedCase $case): self
    {
        return new self(
            name: $case->getName(),
            value: (string) $case->getBackingValue(),
            description: (current($case
                ->getAttributes(Attribute\Doc::class)) ?: null)
                ?->newInstance()
                ->description
                ?? '',
        );
    }
}
