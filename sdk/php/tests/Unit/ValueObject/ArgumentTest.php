<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Container;
use Dagger\File;
use Dagger\Json;
use Dagger\Tests\Unit\Fixture\DaggerObjectWithDaggerFunctions;
use Dagger\ValueObject\Argument;
use Dagger\ValueObject\Type;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionMethod;
use ReflectionNamedType;
use ReflectionParameter;

#[Group('unit')]
#[CoversClass(Argument::class)]
class ArgumentTest extends TestCase
{
    #[Test]
    #[DataProvider('provideReflectionParameters')]
    public function ItBuildsFromReflectionParameter(
        Argument $expected,
        ReflectionParameter $reflectionParameter,
    ): void {
        $actual = Argument::fromReflection($reflectionParameter);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{ 0: Argument, 1:ReflectionNamedType}> */
    public static function provideReflectionParameters(): Generator
    {
        yield 'bool' => [
            new Argument('value', null, new Type('bool'), null),
            self::getReflectionParameter(
                DaggerObjectWithDaggerFunctions::class,
                'requiredBool',
                'value',
            ),
        ];

        yield 'implicitly optional string' => [
            new Argument(
                'value',
                null,
                new Type('string', true),
                new Json('null'),
            ),
            self::getReflectionParameter(
                DaggerObjectWithDaggerFunctions::class,
                'implicitlyOptionalString',
                'value',
            )
        ];

        yield 'explicitly optional string' => [
            new Argument(
                'value',
                null,
                new Type('string', true),
                new Json('null'),
            ),
            self::getReflectionParameter(
                DaggerObjectWithDaggerFunctions::class,
                'explicitlyOptionalString',
                'value',
            )
        ];

        yield 'annotated string' => [
            new Argument(
                'value',
                'this value should have a description',
                new Type('string', false),
                null,
            ),
            self::getReflectionParameter(
                DaggerObjectWithDaggerFunctions::class,
                'annotatedString',
                'value',
            ),
        ];

        yield 'implicitly optional Container' => [
            new Argument(
                'value',
                null,
                new Type(Container::class, true),
                new Json('null'),
            ),
            self::getReflectionParameter(
                DaggerObjectWithDaggerFunctions::class,
                'implicitlyOptionalContainer',
                'value',
            )
        ];

        yield 'explicitly optional File' => [
            new Argument(
                'value',
                null,
                new Type(File::class, true),
                new Json('null'),
            ),
            self::getReflectionParameter(
                DaggerObjectWithDaggerFunctions::class,
                'explicitlyOptionalFile',
                'value',
            )
        ];
    }

    private static function getReflectionParameter(
        string $class,
        string $method,
        string $parameter,
    ): ReflectionParameter {
        $parameters = (new ReflectionMethod($class, $method))->getParameters();

        return array_filter($parameters, fn($p) => $p->name === $parameter)[0];
    }
}
