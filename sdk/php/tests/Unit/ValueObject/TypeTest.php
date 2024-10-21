<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\ValueObject;

use Closure;
use Countable;
use Dagger;
use Dagger\ValueObject\Type;
use Dagger\Tests\Unit\Fixture;
use DateTimeImmutable;
use Generator;
use Iterator;
use PHPUnit\Framework\Attributes\CoversClass;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Group;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionClass;
use ReflectionFunction;
use ReflectionNamedType;
use ReflectionType;

#[Group('unit')]
#[CoversClass(Type::class)]
class TypeTest extends TestCase
{
    #[Test]
    public function itCannotSupportFloat(): void
    {
        self::expectException(Dagger\Exception\UnsupportedType::class);

        new Type('float');
    }

    #[Test]
    public function itCannotSupportArray(): void
    {
        self::expectException(\DomainException::class);

        new Type('array');
    }

    #[Test]
    #[DataProvider('provideUnsupportedReflectionTypes')]
    public function itCannotBuildFromUnsupportedReflectionType(
        ReflectionType $unsupportedReflectionType
    ): void {
        self::expectException(Dagger\Exception\UnsupportedType::class);

        Type::fromReflection($unsupportedReflectionType);
    }

    #[Test]
    #[DataProvider('provideReflectionNamedTypes')]
    public function itBuildsFromReflectionNamedType(
        Type $expected,
        ReflectionNamedType $reflectionType,
    ): void {
        self::assertEquals($expected, Type::fromReflection($reflectionType));
    }

    #[Test]
    #[DataProvider('provideIdAbleTypes')]
    #[DataProvider('provideNonIdAbleTypes')]
    public function itSaysIfItIsIdAble(bool $expected, string $type): void
    {
        self::assertSame($expected, (new Type($type))->isIdable());
    }

    #[Test]
    #[DataProvider('provideTypeDefKinds')]
    public function itHasTypeDefKind(Dagger\TypeDefKind $expected, string $type): void
    {
        self::assertEquals($expected, (new Type($type))->typeDefKind);
    }

    /** @return Generator<array{0:ReflectionType}> */
    public static function provideUnsupportedReflectionTypes(): Generator
    {
        yield 'union type' => [
            (new ReflectionFunction(function(): Iterator&Countable {}))
                ->getReturnType(),
        ];

        yield 'intersection type' => [
            (new ReflectionFunction(function(): Iterator|Countable {}))
                ->getReturnType(),
        ];

        yield 'custom reflection type' => [
            (new class () extends ReflectionType {}),
        ];
    }

    /** @return Generator<array{ 0:Type, 1:ReflectionNamedType }> */
    public static function provideReflectionNamedTypes(): Generator
    {
        $reflectReturnType = fn(Closure $fn) => (
            new ReflectionFunction($fn)
        )->getReturnType();

        yield 'bool' =>  [
            new Type('bool', false),
            $reflectReturnType(fn(): bool => true),
        ];

        yield 'nullable bool' =>  [
            new Type('bool', true),
            $reflectReturnType(fn(): ?bool => false),
        ];

        yield 'int' =>  [
            new Type('int', false),
            $reflectReturnType(fn(): int => 1),
        ];

        yield 'nullable int' =>  [
            new Type('int', true),
            $reflectReturnType(fn(): ?int => 1),
        ];

        yield 'string' =>  [
            new Type('string', false),
            $reflectReturnType(fn(): string => ''),
        ];

        yield 'nullable string' =>  [
            new Type('string', true),
            $reflectReturnType(fn(): ?string => ''),
        ];

        yield Dagger\Container::class => [
            new Type(Dagger\Container::class, false),
            $reflectReturnType(fn(): Dagger\Container => self
                ::createStub(Dagger\Container::class)),
        ];

        yield sprintf('nullable %s', Dagger\Container::class) => [
            new Type(Dagger\Container::class, true),
            $reflectReturnType(fn(): ?Dagger\Container => null),
        ];

        yield Dagger\Directory::class => [
            new Type(Dagger\Directory::class, false),
            $reflectReturnType(fn(): Dagger\Directory => self
                ::createStub(Dagger\Directory::class)),
        ];

        yield sprintf('nullable %s', Dagger\Directory::class) => [
            new Type(Dagger\Directory::class, true),
            $reflectReturnType(fn(): ?Dagger\Directory => null),
        ];

        yield Dagger\File::class => [
            new Type(Dagger\File::class, false),
            $reflectReturnType(fn(): Dagger\File => self
                ::createStub(Dagger\File::class)),
        ];

        yield sprintf('nullable %s', Dagger\File::class) => [
            new Type(Dagger\File::class, true),
            $reflectReturnType(fn(): ?Dagger\File => null),
        ];

        yield Dagger\NetworkProtocol::class => [
            new Type(Dagger\NetworkProtocol::class, false),
            $reflectReturnType(fn(): Dagger\NetworkProtocol => self
                ::createStub(Dagger\NetworkProtocol::class)),
        ];

        yield sprintf('nullable %s', Dagger\NetworkProtocol::class) => [
            new Type(Dagger\NetworkProtocol::class, true),
            $reflectReturnType(fn(): ?Dagger\NetworkProtocol => null),
        ];

        yield Fixture\Enum\StringBackedDummy::class => [
            new Type(Fixture\Enum\StringBackedDummy::class, false),
            $reflectReturnType(fn(): Fixture\Enum\StringBackedDummy => self
                ::createStub(Fixture\Enum\StringBackedDummy::class)),
        ];

        yield sprintf('nullable %s', Fixture\Enum\StringBackedDummy::class) => [
            new Type(Fixture\Enum\StringBackedDummy::class, true),
            $reflectReturnType(fn(): ?Fixture\Enum\StringBackedDummy => null),
        ];
    }

    /** @return Generator<array{ 0:true, 1:class-string }> */
    public static function provideIdAbleTypes(): Generator
    {
        foreach (self::provideIdAbles() as $idAble) {
            yield $idAble => [true, $idAble];
        }
    }

    /** @return Generator<array{ 0:false, 1:string }> */
    public static function provideNonIdAbleTypes(): Generator
    {
        $nonIdAbles = [
            'bool',
            'int',
            'null',
            'string',
            'void',
            DateTimeImmutable::class,
        ];

        foreach ($nonIdAbles as $nonIdAble) {
            yield $nonIdAble => [false, $nonIdAble];
        }
    }

    /** @return Generator<array{ 0:Dagger\TypeDefKind, 1:string }> */
    public static function provideTypeDefKinds(): Generator
    {
        yield 'bool' => [Dagger\TypeDefKind::BOOLEAN_KIND, 'bool'];
        yield 'int' => [Dagger\TypeDefKind::INTEGER_KIND, 'int'];
        yield 'null' => [Dagger\TypeDefKind::VOID_KIND, 'null'];
        yield 'string' => [Dagger\TypeDefKind::STRING_KIND, 'string'];
        yield 'void' => [Dagger\TypeDefKind::VOID_KIND, 'void'];
        yield DateTimeImmutable::class => [
            Dagger\TypeDefKind::OBJECT_KIND,
            DateTimeImmutable::class
        ];

        foreach (self::provideIdAbles() as $idable) {
            yield $idable => [Dagger\TypeDefKind::OBJECT_KIND, $idable];
        }

        foreach (self::provideAbstractScalars() as $scalar) {
            yield $scalar => [Dagger\TypeDefKind::SCALAR_KIND, $scalar];
        }

        foreach (self::provideEnums() as $enum) {
            yield $enum => [Dagger\TypeDefKind::ENUM_KIND, $enum];
        }
    }

    /** @return class-string[] */
    private static function provideIdAbleClasses(): array
    {
        return [
            Dagger\CacheVolume::class,
            Dagger\Container::class,
            Dagger\CurrentModule::class,
            Dagger\Directory::class,
            Dagger\EnvVariable::class,
            Dagger\FieldTypeDef::class,
            Dagger\File::class,
            Dagger\Function_::class,
            Dagger\FunctionArg::class,
            Dagger\FunctionCall::class,
            Dagger\FunctionCallArgValue::class,
            Dagger\GeneratedCode::class,
            Dagger\GitModuleSource::class,
            Dagger\GitRef::class,
            Dagger\GitRepository::class,
//            Dagger\Host::class, //Host has deprecated code
            Dagger\InputTypeDef::class,
            Dagger\InterfaceTypeDef::class,
            Dagger\Label::class,
            Dagger\ListTypeDef::class,
            Dagger\LocalModuleSource::class,
            Dagger\Module::class,
            Dagger\ModuleDependency::class,
            Dagger\ModuleSource::class,
            Dagger\ModuleSourceView::class,
            Dagger\ObjectTypeDef::class,
            Dagger\Port::class,
            Dagger\ScalarTypeDef::class,
            Dagger\Secret::class,
            Dagger\Socket::class,
            Dagger\Terminal::class,
            Dagger\TypeDef::class,
        ];
    }

    /** @return class-string[] */
    private static function provideAbstractScalars(): array
    {
        return [
            Dagger\Platform::class,
            Dagger\Json::class,
        ];
    }

    /** @return class-string[] */
    private static function provideEnums(): array
    {
        return [
            Dagger\CacheSharingMode::class,
            Dagger\ImageLayerCompression::class,
            Dagger\ImageMediaTypes::class,
            Dagger\ModuleSourceKind::class,
            Dagger\NetworkProtocol::class,
            Dagger\TypeDefKind::class,
            Fixture\Enum\StringBackedDummy::class,
        ];
    }
}
