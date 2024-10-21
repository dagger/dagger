<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture\Enum;

use Dagger\Attribute;
use Dagger\ValueObject;

#[Attribute\DaggerObject]
#[Attribute\Doc('A unit enum created for testing purposes')]
enum UnitDummy
{
    #[Attribute\Doc('This case name is "North"')]
    case North;

    #[Attribute\Doc('This case name is "East"')]
    case East;

    #[Attribute\Doc('This case name is "South"')]
    case South;

    #[Attribute\Doc('This case name is "West"')]
    case West;


    public static function getValueObjectEquivalent(): ValueObject\DaggerEnum
    {
        return new ValueObject\DaggerEnum(
            self::class,
            'A unit enum created for testing purposes',
            [
                'North' => 'This case name is "North"',
                'East' => 'This case name is "East"',
                'South' => 'This case name is "South"',
                'West' => 'This case name is "West"',

            ]
        );
    }
}
