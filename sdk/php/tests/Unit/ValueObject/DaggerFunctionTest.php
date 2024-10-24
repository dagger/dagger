<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\File;
use Dagger\Json;
use Dagger\NetworkProtocol;
use Dagger\Tests\Unit\Fixture\DaggerObject\HandlingEnums;
use Dagger\Tests\Unit\Fixture\DaggerObjectWithDaggerFunctions;
use Dagger\Tests\Unit\Fixture\Enum\StringBackedDummy;
use Dagger\ValueObject\Argument;
use Dagger\ValueObject\DaggerFunction;
use Dagger\ValueObject\Type;
use Generator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionClass;
use ReflectionMethod;
use RuntimeException;

#[Group('unit')]
#[CoversClass(DaggerFunction::class)]
class DaggerFunctionTest extends TestCase
{
    #[Test]
    public function itOnlyReflectsDaggerFunctions(): void
    {
        $reflection = new ReflectionMethod(
            self::class,
            'itOnlyReflectsDaggerFunctions',
        );

        self::expectException(RuntimeException::class);

        DaggerFunction::fromReflection($reflection);
    }

    #[Test]
    public function itMustHaveAReturnType(): void
    {
        $reflection = (new ReflectionClass(new #[DaggerObject] class () {
                #[\Dagger\Attribute\DaggerFunction]
                public function noReturnType() {return 'hello world';}
            }))->getMethod('noReturnType');

        self::expectExceptionMessage(
            'DaggerFunction "noReturnType" cannot be supported without a return type',
        );

        DaggerFunction::fromReflection($reflection);
    }

    #[Test]
    #[DataProvider('provideNamesThatMayBeConstructors')]
    public function itMayBeAConstructor(
        bool $expected,
        string $name,
    ): void {
        $sut = new DaggerFunction($name, null, [], new Type('void'));

        self::assertSame($expected, $sut->isConstructor());
    }

    #[DataProvider('provideReflectionMethods')]
    #[DataProvider('provideMethodsHandlingEnums')]
    public function itBuildsFromReflectionMethod(
        DaggerFunction $expected,
        ReflectionMethod $reflectionMethod,
    ): void {
        $actual = DaggerFunction::fromReflection($reflectionMethod);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{ 0: bool, 1:string }> */
    public static function provideNamesThatMayBeConstructors(): Generator
    {
        $cases = [
            [true, ''],
            [false, '__construct'],
            [false, '_construct'],
            [false, 'construct'],
            [false, '__toString'],
        ];

        foreach ($cases as [$isConstructor, $name]) {
            yield $name => [$isConstructor, $name];
        }
    }

    /** @return Generator<array{ 0: DaggerFunction, 1:ReflectionMethod}> */
    public static function provideReflectionMethods(): Generator
    {
        yield 'no parameters' => [
            new DaggerFunction(
                'returnBool',
                null,
                [],
                new Type('bool'),
            ),
            new ReflectionMethod(
                DaggerObjectWithDaggerFunctions::class,
                'returnBool',
            ),
        ];

        yield 'no parameters, method description' => [
            new DaggerFunction(
                'returnInt',
                'this method returns 1',
                [],
                new Type('int'),
            ),
            new ReflectionMethod(
                DaggerObjectWithDaggerFunctions::class,
                'returnInt',
            )
        ];

        yield 'one parameter' => [
            new DaggerFunction(
                'requiredString',
                null,
                [new Argument('value', '', new Type('string'))],
                new Type('void'),
            ),
            new ReflectionMethod(
                DaggerObjectWithDaggerFunctions::class,
                'requiredString',
            ),
        ];

        yield 'annotated parameter' => [
            new DaggerFunction(
                'annotatedString',
                null,
                [new Argument(
                    'value',
                    'this value should have a description',
                    new Type('string')
                )],
                new Type('void'),
            ),
            new ReflectionMethod(
                DaggerObjectWithDaggerFunctions::class,
                'annotatedString',
            ),
        ];

        yield 'implicitly optional parameter' => [
            new DaggerFunction(
                'implicitlyOptionalContainer',
                null,
                [new Argument(
                    'value',
                    '',
                    new Type(Container::class, true),
                    new Json('null'),
                )],
                new Type('void'),
            ),
            new ReflectionMethod(
                DaggerObjectWithDaggerFunctions::class,
                'implicitlyOptionalContainer',
            ),
        ];

        yield 'explicitly optional parameter' => [
            new DaggerFunction(
                'explicitlyOptionalFile',
                null,
                [new Argument(
                    'value',
                    '',
                    new Type(File::class, true),
                    new Json('null'),
                )],
                new Type('void'),
            ),
            new ReflectionMethod(
                DaggerObjectWithDaggerFunctions::class,
                'explicitlyOptionalFile',
            ),
        ];
    }

    /** @return Generator<array{ 0: DaggerFunction, 1:ReflectionMethod}> */
    public static function provideMethodsHandlingEnums(): Generator
    {
        yield 'built-in enum' => [
            new DaggerFunction(
                'requiredNetworkProtocol',
                'Test handling of built-in enums',
                [
                    new Argument(
                        'arg',
                        null,
                        new Type(NetworkProtocol::class, false),
                        null,
                    ),
                ],
                new Type(NetworkProtocol::class),
            ),
            new ReflectionMethod(
                HandlingEnums::class,
                'requiredNetworkProtocol',
            ),
        ];

        yield 'default built-in enum' => [
            new DaggerFunction(
                'defaultNetworkProtocol',
                'Test handling of defaults for built-in enums',
                [
                    new Argument(
                        'arg',
                        null,
                        new Type(NetworkProtocol::class, false),
                        new Json('"TCP"'),
                    ),
                ],
                new Type(NetworkProtocol::class),
            ),
            new ReflectionMethod(
                HandlingEnums::class,
                'defaultNetworkProtocol',
            ),
        ];

        yield 'custom enum' => [
            new DaggerFunction(
                'requiredStringBackedEnum',
                'Test handling of custom string-backed enums',
                [
                    new Argument(
                        'arg',
                        null,
                        new Type(StringBackedDummy::class, false),
                        null,
                    ),
                ],
                new Type(StringBackedDummy::class),
            ),
            new ReflectionMethod(
                HandlingEnums::class,
                'requiredStringBackedEnum',
            ),
        ];

        yield 'default custom enum' => [
            new DaggerFunction(
                'defaultStringBackedEnum',
                'Test handling of defaults for custom string-backed enums',
                [
                    new Argument(
                        'arg',
                        null,
                        new Type(StringBackedDummy::class, false),
                        new Json('"hello"'),
                    ),
                ],
                new Type(StringBackedDummy::class),
            ),
            new ReflectionMethod(
                HandlingEnums::class,
                'defaultStringBackedEnum',
            ),
        ];
    }
}
