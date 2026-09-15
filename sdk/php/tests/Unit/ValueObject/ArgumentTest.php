<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\Exception\RegistrationError\MissingAttribute;
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
use ReflectionClass;
use ReflectionMethod;
use ReflectionNamedType;
use ReflectionParameter;
use RuntimeException;

#[Group('unit')]
#[CoversClass(Argument::class)]
class ArgumentTest extends TestCase
{
    #[Test]
    #[DataProvider('provideInvalidArguments')]
    public function itCannotTakeInvalidArguments(
        ReflectionParameter $reflection,
        RuntimeException $expected,
    ): void {
        self::expectExceptionObject($expected);

        Argument::fromReflection($reflection);
    }

    #[Test]
    public function itCannotDefaultToNullIfNonNullable(): void
    {
        $nonNullableType = new Type('string', false);
        $nullDefault = new Json('null');

        self::expectException(RuntimeException::class);

        new Argument('sut', '', $nonNullableType, $nullDefault);
    }

    #[Test]
    #[DataProvider('provideReflectionParameters')]
    public function itBuildsFromReflectionParameter(
        Argument $expected,
        ReflectionParameter $reflectionParameter,
    ): void {
        $actual = Argument::fromReflection($reflectionParameter);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{0: ReflectionParameter, 1: RuntimeException}> */
    public static function provideInvalidArguments(): Generator
    {
        yield 'Missing typehint for an argument' => [
            (new ReflectionClass(new #[DaggerObject] class () {
                #[DaggerFunction]
                public function func($noType): void {}
            }))->getMethod('func')->getParameters()[0],
            new RuntimeException('Argument "noType" cannot be supported without a typehint')
        ];

        yield 'Missing ListOfType on array argument' => [
            (new ReflectionClass(new #[DaggerObject] class () {
                #[DaggerFunction]
                public function func(array $noSubtype): void {}
            }))->getMethod('func')->getParameters()[0],
            MissingAttribute::listOfType('func', 'noSubtype')
        ];
    }

    /** @return Generator<array{ 0: Argument, 1:ReflectionNamedType}> */
    public static function provideReflectionParameters(): Generator
    {
        yield 'bool' => [
            new Argument('value', '', new Type('bool'), null),
            self::getReflectionParameter(
                DaggerObjectWithDaggerFunctions::class,
                'requiredBool',
                'value',
            ),
        ];

        yield 'implicitly optional string' => [
            new Argument(
                'value',
                '',
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
                '',
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
                '',
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
                '',
                new Type(File::class, true),
                new Json('null'),
            ),
            self::getReflectionParameter(
                DaggerObjectWithDaggerFunctions::class,
                'explicitlyOptionalFile',
                'value',
            )
        ];

        yield 'File with default path' => [
            new Argument(
                'value',
                '',
                new Type(File::class, false),
                null,
                './test',
            ),
            self::getReflectionParameter(
                DaggerObjectWithDaggerFunctions::class,
                'fileWithDefaultPath',
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
