<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use ReflectionEnumBackedCase;

/** @internal Value Object representing a single case of a backed enum exposed to Dagger. */
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
        $descriptionAttr = current($case->getAttributes(Attribute\Doc::class)) ?: null;
        $description = $descriptionAttr !== null
            ? $descriptionAttr->newInstance()->description
            : '';

        return new self(
            name: $case->getName(),
            value: (string) $case->getBackingValue(),
            description: $description,
        );
    }
}
