<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use ReflectionParameter;

final readonly class DaggerArgument
{
    public function __construct(
        public string $name,
        public ?string $description,
        public Type $type,
    ) {
    }

    public static function fromReflection(ReflectionParameter $parameter): self
    {
        $attribute = (current($parameter
            ->getAttributes(Attribute\DaggerArgument::class)) ?: null)
            ?->newInstance();

        return new self(
            $parameter->name,
            $attribute?->description,
            Type::fromReflection($parameter->getType()),
        );
    }
}
