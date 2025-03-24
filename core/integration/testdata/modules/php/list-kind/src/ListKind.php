<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject, ListOfType, ReturnsListOfType};

#[DaggerObject] class ListKind
{
    /**
     * @param list<bool> $arg
     * @return list<bool>
     */
    #[DaggerFunction, ReturnsListOfType('bool')]
    public function oppositeBools(#[ListOfType('bool')] array $arg): array
    {
        return array_map(fn(bool $element) => !$element, $arg);
    }

    /**
     * @param list<float> $arg
     * @return list<float>
     */
    #[DaggerFunction, ReturnsListOfType('float')]
    public function halfFloats(#[ListOfType('float')] array $arg): array
    {
        return array_map(fn(float $element) => $element / 2, $arg);
    }

    /**
     * @param list<float> $arg
     * @return list<float>
     */
    #[DaggerFunction, ReturnsListOfType('int')]
    public function doubleInts(#[ListOfType('int')] array $arg): array
    {
        return array_map(fn(int $element) => $element * 2, $arg);
    }

    /**
     * @param list<string> $arg
     * @return list<string>
     */
    #[DaggerFunction, ReturnsListOfType('string')]
    public function capitalizeStrings(#[ListOfType('string')] array $arg): array
    {
        return array_map(fn(string $element) => ucwords($element), $arg);
    }
}
