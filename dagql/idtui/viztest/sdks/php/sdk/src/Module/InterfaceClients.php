<?php

declare(strict_types=1);

namespace Dagger\Module;

use Dagger\Attribute;
use Dagger\Client\AbstractObject;
use Dagger\Client\IdAble;
use Dagger\Codegen\CodeWriter;
use Dagger\Id;
use Dagger\ModuleNaming;
use Dagger\Service\NormalizesClassName;
use Dagger\TypeDefKind;
use Dagger\ValueObject\DaggerObject;
use Dagger\ValueObject\ListOfType;
use Dagger\ValueObject\Type;
use Nette\PhpGenerator\ClassType;
use Nette\PhpGenerator\Method;
use ReflectionClass;
use RuntimeException;

/**
 * PHP cannot implement an interface at run time without eval, so generation
 * writes one client class per module interface, which the codec loads an
 * implementation into by its ID.
 */
final readonly class InterfaceClients
{
    private const NAMESPACE = 'Dagger\\ModuleInterfaces';

    /** @return class-string */
    public static function className(string $interface): string
    {
        return self::NAMESPACE . '\\' . NormalizesClassName::trimLeadingNamespace($interface);
    }

    public static function write(Description $description, string $directory, ModuleNaming $naming): void
    {
        $interfaces = array_filter($description->objects, static fn($object) => $object->isInterface());
        if ($interfaces === []) {
            return;
        }

        $writer = new CodeWriter($directory, self::NAMESPACE);
        foreach ($interfaces as $interface) {
            $writer->write(self::render($interface, $naming));
        }
    }

    public static function render(DaggerObject $interface, ModuleNaming $naming): ClassType
    {
        self::refuseUnimplementable($interface->name);

        $name = NormalizesClassName::trimLeadingNamespace($interface->name);
        $class = (new ClassType($name))
            ->setFinal()
            ->setExtends(AbstractObject::class)
            ->addImplement($interface->name)
            ->addImplement(IdAble::class);
        $class->addConstant('GRAPHQL_TYPE', $naming->typeNames([$name])[0]);
        $class->addMethod('id')
            ->setReturnType(Id::class)
            ->setBody('return new \Dagger\Id((string) $this->queryLeaf('
                . 'new \Dagger\Client\QueryBuilder(\'id\'), \'id\'));');

        foreach ($interface->daggerFunctions as $function) {
            $qualified = "$interface->name::$function->name";
            $type = $function->returnType;
            self::refuseUnsupported($type, $qualified);
            foreach ($function->arguments as $argument) {
                self::refuseUnsupported($argument->type, $qualified);
            }

            $arguments = array_map(static fn($argument) => $argument->name, $function->arguments);
            $schemaNames = $naming->fieldNames([$function->name, ...$arguments]);
            $field = array_shift($schemaNames);

            // One expression and no locals, so no argument name can shadow a variable of the body.
            $query = '(new \Dagger\Client\QueryBuilder(?))';
            $values = [$field];
            foreach ($arguments as $i => $argument) {
                $query .= '->setArgument(?, $?)';
                array_push($values, $schemaNames[$i], $argument);
            }

            $method = Method::from([$interface->name, $function->name])
                ->setPublic()
                ->setAbstract(false)
                ->setAttributes([]);
            match ($type->typeDefKind) {
                TypeDefKind::VOID_KIND => $method->addBody("\$this->queryLeaf($query, ?);", [...$values, $field]),
                TypeDefKind::OBJECT_KIND => $method->addBody(
                    'return new \\' . ltrim($type->name, '\\')
                        . "(\$this->client, \$this->queryBuilderChain->chain($query));",
                    $values,
                ),
                default => $method->addBody("return \$this->queryLeaf($query, ?);", [...$values, $field]),
            };
            $class->addMember($method);
        }

        return $class;
    }

    /**
     * The generated class implements the whole interface with instance methods
     * of an AbstractObject, so every method must be a function it can query.
     * A variadic method would send its arguments as one list.
     */
    private static function refuseUnimplementable(string $interface): void
    {
        $reflection = new ReflectionClass($interface);
        if ($reflection->hasConstant('GRAPHQL_TYPE')) {
            throw new RuntimeException(
                "The module interface $interface has the constant GRAPHQL_TYPE, which its generated class declares",
            );
        }

        foreach ($reflection->getMethods() as $method) {
            $problem = match (true) {
                $method->getAttributes(Attribute\DaggerFunction::class) === [] => 'has no #[DaggerFunction]',
                $method->isStatic() => 'is static',
                $method->isVariadic() => 'is variadic',
                method_exists(AbstractObject::class, $method->name), strcasecmp($method->name, 'id') === 0
                    => 'has a name its generated class uses',
                default => null,
            };
            if ($problem !== null) {
                throw new RuntimeException("The method $interface::$method->name of a module interface $problem");
            }
        }
    }

    /**
     * A chained object cannot be null, so a nullable object is refused.
     *
     * @phpstan-assert Type $type
     */
    private static function refuseUnsupported(ListOfType|Type $type, string $qualified): void
    {
        $supported = $type instanceof Type && match ($type->typeDefKind) {
            TypeDefKind::BOOLEAN_KIND,
            TypeDefKind::FLOAT_KIND,
            TypeDefKind::INTEGER_KIND,
            TypeDefKind::STRING_KIND,
            TypeDefKind::VOID_KIND => true,
            TypeDefKind::OBJECT_KIND => $type->isIdable() && !$type->nullable,
            default => false,
        };

        if (!$supported) {
            throw new RuntimeException(sprintf(
                'The interface function %s cannot take or return %s yet',
                $qualified,
                $type instanceof Type ? $type->name : 'a list',
            ));
        }
    }
}
