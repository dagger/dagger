<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use Dagger\Exception\UnsupportedType;

/** @internal Value Object used for backed enums to expose to Dagger. */
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
     * @throws \RuntimeException
     * - if missing DaggerObject Attribute
     * - if any DaggerFunction parameter type is unsupported
     * - if any DaggerFunction return type is unsupported
     */
    public static function fromReflection(\ReflectionEnum $enum): self
    {
        if (empty($enum->getAttributes(Attribute\DaggerObject::class))) {
            throw new \RuntimeException('class is not a DaggerObject');
        }

        if (! $enum->isBacked()) {
            throw new UnsupportedType(sprintf(<<<'TEXT'
                Dagger only supports string-backed enums.
                %s is a unit enum.
                TEXT, $enum->getName()));
        }

        if ($enum->getBackingType()->getName() !== 'string') {
            throw new UnsupportedType(sprintf(<<<'TEXT'
                Dagger only supports string-backed enums.
                %s is an int-backed enum.
                TEXT, $enum->getName()));
        }

        $cases = [];
        foreach ($enum->getCases() as $case) {
            $cases[] = new DaggerEnumCase(
                name: $case->getName(),
                value: $case->getBackingValue(),
                description: (current($case
                    ->getAttributes(Attribute\Doc::class)) ?: null)
                    ?->newInstance()
                    ->description
                    ?? '',
            );
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

    public function getNormalisedName(): string
    {
        $result = explode('\\', $this->name);
        array_shift($result);
        return implode('\\', $result);
    }
}
