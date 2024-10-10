<?php

declare(strict_types=1);

namespace Dagger\Service;

use Dagger\Attribute;
use Dagger\ValueObject;
use Roave\BetterReflection\BetterReflection;
use Roave\BetterReflection\Reflection\ReflectionClass;
use Roave\BetterReflection\Reflector\DefaultReflector;
use Roave\BetterReflection\SourceLocator\Type\DirectoriesSourceLocator;

final class FindsDaggerObjects
{
    /**
     * Finds all classes with the DaggerObject attribute.
     * Only looks within the given directory.
     * @return ValueObject\DaggerObject[]
     */
    public function __invoke(string $dir): array
    {
        $reflector = new DefaultReflector(new DirectoriesSourceLocator(
            [$dir],
            (new BetterReflection())->astLocator()
        ));

        $betterReflections =  array_filter(
            $reflector->reflectAllClasses(),
            fn($o) => $this->isDaggerObject($o),
        );

        $builtinReflections = array_map(
            fn($r) => $r->isEnum() ?
                new \ReflectionEnum($r->getName()) :
                new \ReflectionClass($r->getName()),
            $betterReflections
        );

        return array_map(
            fn($d) => $d instanceof \ReflectionEnum ?
                ValueObject\DaggerEnum::fromReflection($d) :
                ValueObject\DaggerClass::fromReflection($d),
            $builtinReflections
        );
    }

    private function isDaggerObject(ReflectionClass $class): bool
    {
        return !empty($class->getAttributesByName(Attribute\DaggerObject::class));
    }
}
