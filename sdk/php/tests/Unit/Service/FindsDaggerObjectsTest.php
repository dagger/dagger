<?php

namespace Dagger\tests\Unit\Service;

use Dagger\Service\FindsDaggerObjects;
use Dagger\Tests\Unit\Fixture\ButterKnife;
use Dagger\ValueObject\DaggerArgument;
use Dagger\ValueObject\DaggerFunction;
use Dagger\ValueObject\DaggerObject;
use Dagger\ValueObject\Type;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[CoversClass(FindsDaggerObjects::class)]
class FindsDaggerObjectsTest extends TestCase
{
    #[Test]
    public function itFindsDaggerObjects(): void {
        $expected = [
            new DaggerObject(ButterKnife::class, [
                new DaggerFunction(
                    'spread',
                    null,
                    [
                        new DaggerArgument(
                            'spread',
                            null,
                            new Type('string')
                        ),
                        new DaggerArgument(
                            'surface',
                            'The surface on which to spread',
                            new Type('string')
                        ),
                    ],
                    new Type('bool'),
                ),
                new DaggerFunction(
                    'sliceBread',
                    'Nothing better',
                    [],
                    new Type('string'),
                ),
            ])
        ];
        $fixture = __DIR__ . '/../Fixture';

        $actual = (new FindsDaggerObjects())($fixture);


        self::assertEquals($expected, $actual);
    }
}
