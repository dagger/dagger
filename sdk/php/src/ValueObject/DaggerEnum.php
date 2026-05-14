<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use Dagger\Exception\UnsupportedType;
use ReflectionEnum;
use ReflectionEnumBackedCase;

/** @internal Value Object used for backed enums to expose to Dagger. */
final readonly class DaggerEnum
{
    /**
     * @param DaggerEnumCase[] $cases
     */
    public function __construct(
        public string $name,
        public string $description,
        public array $cases,
    ) {
    }

    /**
     * @throws UnsupportedType if the enum is not a backed enum (string or int).
     */
    public static function fromReflection(ReflectionEnum $enum): self
    {
        if (!$enum->isBacked()) {
            throw new UnsupportedType(sprintf(
                'Enum "%s" is not a backed enum. Only backed enums (e.g. "enum %s: string { ... }")'
                    . ' can be exposed as Dagger enum types.',
                $enum->getName(),
                $enum->getShortName(),
            ));
        }

        $descriptionAttr = current($enum->getAttributes(Attribute\Doc::class)) ?: null;
        $description = $descriptionAttr !== null
            ? $descriptionAttr->newInstance()->description
            : '';

        $cases = array_map(
            fn(ReflectionEnumBackedCase $case) => DaggerEnumCase::fromReflection($case),
            $enum->getCases(),
        );

        return new self(
            name: $enum->getName(),
            description: $description,
            cases: array_values($cases),
        );
    }
}
