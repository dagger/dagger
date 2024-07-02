<?php

declare(strict_types=1);

namespace Dagger\ValueObject;

use Dagger\Attribute;
use Dagger\Client;
use Dagger\Json;
use ReflectionClass;
use RuntimeException;

final readonly class DaggerObject
{
    /**
     *@var array<string,DaggerFunction[]
     * name => DaggerFunction pairs
     */
    public array $daggerFunctions;

    /**
     * @param DaggerFunction[] $daggerFunctions
     */
    public function __construct(
        public string $name,
        array $daggerFunctions,
    ) {
        $this->daggerFunctions = array_combine(
            array_map(fn($v) => $v->name, $daggerFunctions),
            $daggerFunctions,
        );
    }

    /**
     * @throws \RuntimeException
     * - if missing DaggerObject Attribute
     * - if any DaggerFunction parameter type is unsupported
     * - if any DaggerFunction return type is unsupported
     * @param ReflectionClass<object> $class
     */
    public static function fromReflection(ReflectionClass $class): self
    {
        if (empty($class->getAttributes(Attribute\DaggerObject::class))) {
            throw new RuntimeException('class is not a DaggerObject');
        }

        $methodReflections = array_filter(
            $class->getMethods(\ReflectionMethod::IS_PUBLIC),
            fn($m) => !empty($m->getAttributes(Attribute\DaggerFunction::class))
        );

        $daggerFunctions = array_map(
            fn($r) => DaggerFunction::fromReflection($r),
            $methodReflections,
        );

        return new self($class->name, $daggerFunctions);
    }
}
