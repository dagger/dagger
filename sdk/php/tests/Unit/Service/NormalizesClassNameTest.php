<?php

namespace Dagger\Tests\Unit\Service;

use Dagger\ValueObject\Type;
use Generator;
use Dagger\Service\NormalizesClassName;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

#[Group('unit')]
#[CoversClass(NormalizesClassName::class)]
final class NormalizesClassNameTest extends TestCase
{
    #[Test]
    #[DataProvider('provideNamesToTrim')]
    public function itTrimsLeadingNamespace(
        string $expected,
        string $name,
    ): void {
        self::assertSame(
            $expected,
            NormalizesClassName::trimLeadingNamespace($name),
        );
    }

    #[Test]
    #[DataProvider('provideNamesToShorten')]
    public function itShortensNames(
        string $expected,
        string $name,
    ): void {
        self::assertSame(
            $expected,
            NormalizesClassName::shorten($name),
        );
    }

    /** @return \Generator<array{ 1:string, 2:string }> */
    public static function provideNamesToTrim(): Generator
    {
        $cases = [
            'Dagger' => 'Dagger',
            'Class' => 'Class',
            'Dagger\\Class' => 'Class',
            'DaggerModule\\Class' => 'Class',
            'MyModule\\Class' => 'Class',
            'Dagger\\Tests\\Unit' => 'Tests\\Unit',
            'DaggerModule\\Tests\\Unit' => 'Tests\\Unit',
            'MyModule\\Tests\\Unit\\MyTest' => 'Tests\\Unit\\MyTest',
        ];

        foreach ($cases as $name => $trimmedName) {
            yield $name => [$trimmedName, $name];
        }
    }

    /** @return \Generator<array{ 1:string, 2:string }> */
    public static function provideNamesToShorten(): Generator
    {
        $cases = [
            'Dagger' => 'Dagger',
            'Class' => 'Class',
            'Dagger\\Class' => 'Class',
            'DaggerModule\\Class' => 'Class',
            'MyModule\\Class' => 'Class',
            'Dagger\\Tests\\Unit' => 'Unit',
            'DaggerModule\\Tests\\Unit' => 'Unit',
            'MyModule\\Tests\\Unit\\MyTest' => 'MyTest',
        ];

        foreach ($cases as $name => $shortenedName) {
            yield $name => [$shortenedName, $name];
        }
    }
}
