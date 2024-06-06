<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use ReflectionClass;
use RuntimeException;

final readonly class DaggerObject
{
    /**
     * @param DaggerFunction[] $daggerFunctions
     */
    public function __construct(
        public string $name,
        public array $daggerFunctions,
    ) {
    }

    /**
     * @throws \RuntimeException
     * - if missing DaggerObject Attribute
     * - if any DaggerFunction parameter type is unsupported
     * - if any DaggerFunction return type is unsupported
     */
    public static function fromReflection(ReflectionClass $class): self
    {
        if (empty($class->getAttributes(Attribute\DaggerObject::class))) {
            throw new RuntimeException('class is not a DaggerObject');
        }

        return new self(
            $class->name,
            self::getDaggerFunctions($class),
        );
    }

    /**
     * @return DaggerFunction[]
     */
    private static function getDaggerFunctions(ReflectionClass $daggerObject): array {
        $methods = $daggerObject->getMethods(\ReflectionMethod::IS_PUBLIC);

        $daggerFunctions = array_filter(
            $methods,
            fn($m) => !empty($m->getAttributes(Attribute\DaggerFunction::class))
        );

        return array_map(
            fn($df) => DaggerFunction::fromReflection($df),
            $daggerFunctions,
        );
    }
}
