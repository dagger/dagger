<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;

final readonly class DaggerEnum implements DaggerObject
{
    /** @param DaggerFunction[] $daggerFunctions */
    public function __construct(
        private string $name,
        private string $description = '',
        private array $cases = [],
    ) {
    }

    public function getName(): string
    {
        return $this->name;
    }

    public function getDescription(): string
    {
        return $this->description;
    }

    /**
     * @return array<string,string> case => description
     */
    public function getCases(): array
    {
        return $this->cases;
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

        $description = (current($enum
            ->getAttributes(Attribute\Doc::class)) ?: null)
            ?->newInstance()
            ?->description
            ?? '';

        $cases = [];
        foreach ($enum->getCases() as $case) {
            $cases[$case->getName()] = (current($case
            ->getAttributes(Attribute\Doc::class)) ?: null)
            ?->newInstance()
            ?->description
            ?? '';
        }

        return new self(
            name: $enum->getName(),
            description: $description,
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
