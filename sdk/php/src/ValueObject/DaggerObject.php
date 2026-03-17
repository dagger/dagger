<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;

final readonly class DaggerObject
{
    /**
     * @var array<string, DaggerFunction>
     *            name => DaggerFunction
     */
    public array $daggerFunctions;

    /** @param DaggerFunction[] $daggerFunctions */
    public function __construct(
        public string $name,
        public string $description = '',
        array $daggerFunctions = [],
    ) {
        $this->daggerFunctions = array_combine(
            array_map(fn($f) => $f->name, $daggerFunctions),
            $daggerFunctions,
        );
    }

    /**
     * @throws \RuntimeException
     * - if missing DaggerObject Attribute
     * - if any DaggerFunction parameter type is unsupported
     * - if any DaggerFunction return type is unsupported
     */
    public static function fromReflection(\ReflectionClass $class): self
    {
        if (empty($class->getAttributes(Attribute\DaggerObject::class))) {
            throw new \RuntimeException('class is not a DaggerObject');
        }

        $description = (current($class
            ->getAttributes(Attribute\Doc::class)) ?: null)
            ?->newInstance()
            ->description
            ?? '';

        $methodReflections = array_filter(
            $class->getMethods(\ReflectionMethod::IS_PUBLIC),
            fn($m) => !empty($m->getAttributes(Attribute\DaggerFunction::class))
        );

        $daggerFunctions = array_map(
            fn($r) => DaggerFunction::fromReflection($r),
            $methodReflections,
        );

        return new self(
            name: $class->name,
            description: $description,
            daggerFunctions: $daggerFunctions
        );
    }
}
