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
     *
     * @return list<ValueObject\DaggerEnum|ValueObject\DaggerObject>
     */
    public function __invoke(string $dir): array
    {
        return array_map(
            static fn($d) => $d instanceof \ReflectionEnum
                ? ValueObject\DaggerEnum::fromReflection($d)
                : ValueObject\DaggerObject::fromReflection($d),
            $this->reflectClassesInDirectory($dir),
        );
    }

    private function isDaggerObject(ReflectionClass $class): bool
    {
        return !empty($class->getAttributesByName(Attribute\DaggerObject::class));
    }

    /** @return array<\ReflectionEnum|\ReflectionClass> */
    private function reflectClassesInDirectory(string $directory): array
    {
        /**
         *  BetterReflection simplifies scanning the directory.
         *  But built-in reflections are more performant,
         *  So we swap to built-in reflections ASAP.
         */
        $reflector = new DefaultReflector(new DirectoriesSourceLocator(
            [$directory],
            (new BetterReflection())->astLocator()
        ));

        $betterReflections = array_filter(
            $reflector->reflectAllClasses(),
            fn($class) => $this->isDaggerObject($class)
        );

        return array_map(
            fn($r) => $r->isEnum() ?
                new \ReflectionEnum($r->getName()) :
                new \ReflectionClass($r->getName()),
            $betterReflections
        );
    }
}
