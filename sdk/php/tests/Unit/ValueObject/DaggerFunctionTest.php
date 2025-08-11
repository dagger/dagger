<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Dagger\Attribute\DaggerObject;
use Dagger\Container;
use Dagger\Exception\RegistrationError\MissingAttribute;
use Dagger\File;
use Dagger\Json;
use Dagger\Tests\Unit\Fixture\DaggerObjectWithDaggerFunctions;
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
    #[DataProvider('provideInvalidDaggerFunctions')]
    public function itCannotTakeInvalidDaggerFunctions(
        ReflectionMethod $reflection,
        \RuntimeException $expected,
    ): void {
        self::expectExceptionObject($expected);

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

    #[Test, DataProvider('provideReflectionMethods')]
    public function ItBuildsFromReflectionMethod(
        DaggerFunction $expected,
        ReflectionMethod $reflectionMethod,
    ): void {
        $actual = DaggerFunction::fromReflection($reflectionMethod);

        self::assertEquals($expected, $actual);
    }

    /** @return Generator<array{0: ReflectionMethod, 1: RuntimeException}> */
    public static function provideInvalidDaggerFunctions(): Generator
    {
        yield 'DaggerFunction attribute missing' => [
            (new ReflectionClass(new #[DaggerObject] class () {
                public function noAttribute(): string {return 'hello world';}
            }))->getMethod('noAttribute'),
            new RuntimeException(sprintf(
                'Method "noAttribute" is not considered a dagger function without the %s attribute',
                \Dagger\Attribute\DaggerFunction::class,
            )),
        ];

        yield 'return typehint missing' => [
            (new ReflectionClass(new #[DaggerObject] class () {
                #[\Dagger\Attribute\DaggerFunction]
                public function noReturnType() {return 'hello world';}
            }))->getMethod('noReturnType'),
            new RuntimeException(
                'DaggerFunction "noReturnType" cannot be supported without a return type'
            ),
        ];

        yield 'missing attribute for returning arrays' => [
            (new ReflectionClass(new #[DaggerObject] class () {
                #[\Dagger\Attribute\DaggerFunction]
                public function returnsArrayWithoutAttribute(): array {return ['hello', 'world'];}
            }))->getMethod('returnsArrayWithoutAttribute'),
            MissingAttribute::returnsListOfType('returnsArrayWithoutAttribute'),
        ];
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
}
