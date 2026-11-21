<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use Dagger\Exception\UnsupportedType;

/** @internal Value Object used for enums to expose to Dagger. */
final readonly class DaggerEnum
{
    /**
     * @param list<DaggerEnumCase> $cases
     */
    public function __construct(
        public string $name,
        public string $description = '',
        public array $cases = [],
    ) {
    }

    /**
     * @throws \RuntimeException if missing the DaggerObject Attribute
     * @throws UnsupportedType if the enum declares no cases
     */
    public static function fromReflection(\ReflectionEnum $enum): self
    {
        if (empty($enum->getAttributes(Attribute\DaggerObject::class))) {
            throw new \RuntimeException('class is not a DaggerObject');
        }

        if ($enum->getCases() === []) {
            throw new UnsupportedType(sprintf(<<<'TEXT'
                Dagger cannot register an enum without cases.
                %s declares none.
                TEXT, $enum->getName()));
        }

        $cases = [];
        foreach ($enum->getCases() as $case) {
            $cases[] = DaggerEnumCase::fromReflection($case);
        }

        return new self(
            name: $enum->getName(),
            description: (current($enum
                ->getAttributes(Attribute\Doc::class)) ?: null)
                ?->newInstance()
                ->description
                ?? '',
            cases: $cases
        );
    }
}
