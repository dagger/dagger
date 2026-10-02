<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use ReflectionEnumBackedCase;
use ReflectionEnumUnitCase;

/** @internal represents a single case of an enum exposed to Dagger. */
final readonly class DaggerEnumCase
{
    public function __construct(
        public string $name,
        public string $value,
        public string $description,
    ) {
    }

    public static function fromReflection(ReflectionEnumUnitCase $case): self
    {
        return new self(
            name: $case->getName(),
            // A pure enum has no value. Leaving it empty stops the engine
            // emitting an @enumValue directive claiming one.
            value: $case instanceof ReflectionEnumBackedCase
                ? (string) $case->getBackingValue()
                : '',
            description: (current($case
                ->getAttributes(Attribute\Doc::class)) ?: null)
                ?->newInstance()
                ->description
                ?? '',
        );
    }
}
