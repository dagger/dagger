<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

use DateTimeImmutable;
use Dagger\Client\AbstractClient;
use Dagger\Client\AbstractInputObject;
use Dagger\Client\AbstractObject;
use Dagger\Client\AbstractScalar;
use Dagger\Client\IdAble;
use Dagger\Codegen\CodeWriter;
use Dagger\Codegen\Naming\Casing;
use Dagger\Codegen\Naming\Identifiers;
use Nette\PhpGenerator\ClassType;
use Nette\PhpGenerator\EnumType;
use Nette\PhpGenerator\InterfaceType;
use Nette\PhpGenerator\Literal;
use Nette\PhpGenerator\Method;

/**
 * Codegen visitor that works with raw introspection data,
 * supporting @expectedType directives and first-class interfaces.
 *
 * When the schema carries the engine's identifier words (engine views
 * v1.0.0 and above), PHP names are formatted from them:
 *
 *   - methods and their parameters: CAMEL / UPPERCASE (filterURI, callID);
 *   - enum cases: SCREAMING_SNAKE (PER_SESSION), keeping the schema name
 *     where that would collide with another value's case (Gzip vs GZIP).
 *
 * Otherwise every name falls back to the legacy conversion, so older
 * schemas generate byte-identical code. Class names always use the legacy
 * conversion: PHP class names are case-insensitive, so reformatting them
 * would only rename files, which breaks PSR-4 autoloading of the old
 * spelling on case-sensitive filesystems.
 *
 * Only PHP identifiers change. Everything sent over the wire (field names,
 * argument names, input object fields, enum values) and every GraphQL type
 * name stays exactly as the schema has it.
 */
class NewCodegenVisitor extends CodeWriter
{
    /**
     * @param string[] $interfaceNames names of the schema's interface types
     * @param ?Identifiers $identifiers the words of the schema's names, if it has them
     */
    public function __construct(
        string $targetDirectory,
        private readonly bool $supportsNullableObjects = true,
        private readonly array $interfaceNames = [],
        private readonly ?Identifiers $identifiers = null,
    ) {
        parent::__construct($targetDirectory);
    }

    public function visitScalar(IntrospectionType $type): void
    {
        $phpClassName = $this->formatPhpClassName($type->name);

        $scalarClass = new ClassType($phpClassName);
        $scalarClass->setReadOnly(true);
        if ($type->description !== null) {
            $scalarClass->addComment($type->description);
        }

        $scalarClass->setExtends(AbstractScalar::class);

        $this->write($scalarClass);
    }

    public function visitInput(IntrospectionType $type): void
    {
        $phpClassName = $this->formatPhpClassName($type->name);

        $inputObjectClass = new ClassType($phpClassName);
        $inputObjectClass->setExtends(AbstractInputObject::class);
        if ($type->description !== null) {
            $inputObjectClass->addComment($type->description);
        }

        $constructor = $inputObjectClass->addMethod('__construct');

        foreach ($type->inputFields as $field) {
            $fieldType = $field->type;
            if ($fieldType->isList()) {
                $phpParameterType = 'array';
            } else {
                $phpParameterType = $fieldType->isBuiltinScalar()
                    ? $this->formatScalarType($fieldType)
                    : $this->formatPhpFqcn($this->formatOutputTypeName($fieldType));
            }

            $constructorParameter = $constructor->addPromotedParameter($field->name);
            $constructorParameter->setType($phpParameterType);
            $constructorParameter->setNullable(!$field->isRequired());
            $constructorParameter->setPublic();

            if ($field->defaultValue !== null && $fieldType->isBuiltinScalar()) {
                $constructorParameter->setDefaultValue(json_decode($field->defaultValue, true));
            }
        }

        $this->write($inputObjectClass);
    }

    public function visitEnum(IntrospectionType $type): void
    {
        $enumClass = new EnumType($this->formatPhpClassName($type->name));
        $enumClass->setType('string');
        if ($type->description !== null) {
            $enumClass->addComment($type->description);
        }

        $caseNames = $this->enumCaseNames($type);
        foreach ($type->enumValues as $value) {
            // The case's value is what goes over the wire: always the schema name.
            $case = $enumClass->addCase($caseNames[$value->name], $value->name);
            if ($value->description !== null) {
                $case->addComment($value->description);
            }
        }

        // Keep renamed cases reachable under their old name.
        foreach ($type->enumValues as $value) {
            $caseName = $caseNames[$value->name];
            if ($caseName === $value->name) {
                continue;
            }
            $enumClass->addConstant($value->name, new Literal('self::' . $caseName))
                ->addComment("@deprecated Use {$caseName} instead.");
        }

        $this->write($enumClass);
    }

    public function visitObject(IntrospectionType $type): void
    {
        $parentClass = $type->name === 'Query' ? AbstractClient::class : AbstractObject::class;
        $className = $type->name === 'Query' ? 'Client' : $type->name;

        $objectClass = new ClassType($this->formatPhpClassName($className));
        $objectClass->setExtends($parentClass);
        if ($type->description !== null) {
            $objectClass->addComment($type->description);
        }

        if ($type->hasField('id')) {
            $objectClass->addImplement(IdAble::class);
        }

        // Add implements for any interfaces this object implements
        foreach ($type->interfaces as $iface) {
            $ifaceName = $iface->leafName();
            if ($ifaceName !== null && $ifaceName !== 'Object') {
                $objectClass->addImplement($this->formatPhpFqcn($this->formatPhpClassName($ifaceName)));
            }
        }

        // Set parentTypeName on fields for ConvertID detection
        foreach ($type->fields as $field) {
            $field->parentTypeName = $type->name;
        }

        foreach ($type->fields as $field) {
            $this->generateObjectMethod($objectClass, $field, $type);
        }
        $this->addLegacyMethods($objectClass, $type->fields);

        $this->write($objectClass);
    }

    public function visitInterface(IntrospectionType $type): void
    {
        // Generate PHP interface
        $interfaceClass = new InterfaceType($this->formatPhpClassName($type->name));
        if ($type->description !== null) {
            $interfaceClass->addComment($type->description);
        }

        // Set parentTypeName on fields for ConvertID detection
        foreach ($type->fields as $field) {
            $field->parentTypeName = $type->name;
        }

        foreach ($type->fields as $field) {
            $this->generateInterfaceMethod($interfaceClass, $field, $type);
        }
        $this->addLegacyMethods($interfaceClass, $type->fields);

        $this->write($interfaceClass);

        // Generate FooClient class that implements the interface
        $clientClass = new ClassType($this->formatPhpClassName($type->name) . 'Client');
        $clientClass->setExtends(AbstractObject::class);
        $clientClass->addImplement($this->formatPhpFqcn($this->formatPhpClassName($type->name)));
        $clientClass->addImplement(IdAble::class);
        if ($type->description !== null) {
            $clientClass->addComment("Query-builder client for the {$type->name} interface.");
        }

        foreach ($type->fields as $field) {
            $this->generateObjectMethod($clientClass, $field, $type, true);
        }
        $this->addLegacyMethods($clientClass, $type->fields);

        $this->write($clientClass);
    }

    // ---- Method generation ----

    private function generateObjectMethod(
        ClassType $class,
        IntrospectionField $field,
        IntrospectionType $parentType,
        bool $isInterfaceClient = false,
    ): void {
        $method = $class->addMethod($this->methodName($field->name));
        if ($field->description !== null) {
            $method->addComment($field->description);
        }

        $returnType = $field->type;
        $isConvertID = $field->isConvertID();

        // Determine the PHP return type
        if ($isConvertID) {
            // ID handle: returns the object the ID names
            [, $handleReturnType] = $this->resolveIdHandle($field, $parentType);
            $method->setReturnType($handleReturnType);
            $method->setReturnNullable(false);
        } else {
            $phpReturnType = $this->resolveReturnType($returnType, $field);
            if ($returnType->isNonNull()) {
                $method->setReturnNullable(false);
            } elseif ($this->supportsNullableObjects && ($returnType->isObject() || $returnType->isInterface())) {
                $method->setReturnNullable(true);
            }
            $method->setReturnType($phpReturnType);
        }

        // Generate parameters
        $sortedArgs = $this->sortMethodArguments($field->args);
        foreach ($sortedArgs as $arg) {
            $this->generateMethodParameter($arg, $method, $field);
        }

        // Generate method body
        if ($isConvertID) {
            // ID handle: resolve the ID, then load the object it names
            [$handleType, , $handleLoadClass] = $this->resolveIdHandle($field, $parentType);
            $method->addBody('$leafQueryBuilder = new \Dagger\Client\QueryBuilder(?);', [$field->name]);
            $this->generateMethodArgsBody($method, $sortedArgs, 'leafQueryBuilder');
            $method->addBody('$id = $this->queryLeaf($leafQueryBuilder, ?);', [$field->name]);
            $method->addBody(
                'return $this->client->loadObjectFromId(' . $handleLoadClass
                . '::class, new \Dagger\Id((string)$id), ?);',
                [$handleType]
            );
        } elseif (
            $this->supportsNullableObjects
            && !$returnType->isNonNull()
            && ($returnType->isObject() || $returnType->isInterface())
        ) {
            $method->addBody('$objectQueryBuilder = new \Dagger\Client\QueryBuilder(?);', [$field->name]);
            $this->generateMethodArgsBody($method, $sortedArgs, 'objectQueryBuilder');
            $method->addBody('$objectQueryBuilder->selectField(?);', ['id']);
            $method->addBody('$id = $this->queryLeaf($objectQueryBuilder, ?);', ['id']);
            $method->addBody('if ($id === null) {');
            $method->addBody('    return null;');
            $method->addBody('}');
            $returnClassName = $this->resolveReturnClassName($returnType, $field);
            $graphQLTypeName = $this->unwrapNonNull($returnType)->leafName();
            $method->addBody(
                'return $this->client->loadObjectFromId(' . $returnClassName
                . '::class, new \Dagger\Id((string)$id), ?);',
                [$graphQLTypeName]
            );
        } elseif ($this->isLeafReturn($returnType, $field)) {
            // Scalar/list/enum return: use queryLeaf
            $method->addBody('$leafQueryBuilder = new \Dagger\Client\QueryBuilder(?);', [$field->name]);
            $this->generateMethodArgsBody($method, $sortedArgs, 'leafQueryBuilder');

            $unwrapped = $this->unwrapNonNull($returnType);

            if ($unwrapped->isIDScalar()) {
                $method->addBody(
                    'return new \Dagger\Id((string)$this->queryLeaf($leafQueryBuilder, ?));',
                    [$field->name]
                );
            } elseif ($unwrapped->isCustomScalar() && !$unwrapped->isVoid()) {
                $typeName = $this->formatPhpFqcn($this->formatPhpClassName($unwrapped->leafName()));
                $method->addBody(
                    'return new ' . $typeName . '((string)$this->queryLeaf($leafQueryBuilder, ?));',
                    [$field->name]
                );
            } elseif ($unwrapped->isEnum()) {
                $enumClass = $this->formatPhpFqcn($this->formatPhpClassName($unwrapped->leafName()));
                $method->addBody(
                    'return ' . $enumClass . '::from((string)$this->queryLeaf($leafQueryBuilder, ?));',
                    [$field->name]
                );
            } elseif ($unwrapped->isVoid()) {
                $method->addBody(
                    '$this->queryLeaf($leafQueryBuilder, ?);',
                    [$field->name]
                );
            } elseif ($unwrapped->isList()) {
                $method->addBody(
                    'return (array)$this->queryLeaf($leafQueryBuilder, ?);',
                    [$field->name]
                );
            } else {
                // Built-in scalar
                $castType = $this->formatScalarType($unwrapped);
                $method->addBody(
                    'return (' . $castType . ')$this->queryLeaf($leafQueryBuilder, ?);',
                    [$field->name]
                );
            }
        } else {
            // Object/interface return: chain query builder
            $method->addBody('$innerQueryBuilder = new \Dagger\Client\QueryBuilder(?);', [$field->name]);
            $this->generateMethodArgsBody($method, $sortedArgs, 'innerQueryBuilder');

            $returnClassName = $this->resolveReturnClassName($returnType, $field);
            $method->addBody(
                'return new ' . $returnClassName .
                '($this->client, $this->queryBuilderChain->chain($innerQueryBuilder));',
                []
            );
        }
    }

    private function generateInterfaceMethod(
        InterfaceType $interface,
        IntrospectionField $field,
        IntrospectionType $parentType,
    ): void {
        $method = $interface->addMethod($this->methodName($field->name));
        if ($field->description !== null) {
            $method->addComment($field->description);
        }

        $returnType = $field->type;
        $isConvertID = $field->isConvertID();

        if ($isConvertID) {
            [, $handleReturnType] = $this->resolveIdHandle($field, $parentType);
            $method->setReturnType($handleReturnType);
            $method->setReturnNullable(false);
        } else {
            $phpReturnType = $this->resolveReturnType($returnType, $field);
            if ($returnType->isNonNull()) {
                $method->setReturnNullable(false);
            } elseif ($this->supportsNullableObjects && ($returnType->isObject() || $returnType->isInterface())) {
                $method->setReturnNullable(true);
            }
            $method->setReturnType($phpReturnType);
        }

        $sortedArgs = $this->sortMethodArguments($field->args);
        foreach ($sortedArgs as $arg) {
            $this->generateMethodParameter($arg, $method, $field);
        }
    }

    // ---- Type resolution ----

    /**
     * Resolve an ID handle field: the GraphQL type it loads, the PHP return
     * type (the object, or the interface it names), and the class to load
     * it through (the interface's FooClient class for interface handles).
     *
     * @return array{0: string, 1: string, 2: string}
     */
    private function resolveIdHandle(IntrospectionField $field, IntrospectionType $parentType): array
    {
        $handleType = $field->idHandleType();
        $isInterface = $handleType === $parentType->name
            ? $parentType->kind === 'INTERFACE'
            : in_array($handleType, $this->interfaceNames, true);
        $className = $this->formatPhpClassName($handleType === 'Query' ? 'Client' : $handleType);
        $returnType = $this->formatPhpFqcn($className);
        $loadClass = $isInterface ? $this->formatPhpFqcn($className . 'Client') : $returnType;

        return [$handleType, $returnType, $loadClass];
    }

    /**
     * Resolve the PHP return type for a field.
     */
    private function resolveReturnType(IntrospectionTypeRef $typeRef, IntrospectionField $field): string
    {
        $unwrapped = $this->unwrapNonNull($typeRef);

        if ($unwrapped->isList()) {
            return 'array';
        }

        if ($unwrapped->isBuiltinScalar()) {
            return $this->formatScalarType($unwrapped);
        }

        if ($unwrapped->isVoid()) {
            return 'void';
        }

        if ($unwrapped->isDateTime()) {
            return DateTimeImmutable::class;
        }

        if ($unwrapped->isIDScalar()) {
            return $this->formatPhpFqcn('Id');
        }

        if ($unwrapped->isInterface()) {
            // Interface return: use the interface type name directly
            return $this->formatPhpFqcn($this->formatPhpClassName($unwrapped->leafName()));
        }

        // Object, enum, custom scalar, input object
        $name = $unwrapped->leafName();
        if ($name === 'Query') {
            $name = 'Client';
        }
        return $this->formatPhpFqcn($this->formatPhpClassName($name));
    }

    /**
     * Resolve the PHP class name to instantiate for a field return.
     */
    private function resolveReturnClassName(IntrospectionTypeRef $typeRef, IntrospectionField $field): string
    {
        $unwrapped = $this->unwrapNonNull($typeRef);
        $name = $unwrapped->leafName();

        if ($unwrapped->isInterface()) {
            // For interface returns, instantiate FooClient
            return $this->formatPhpFqcn($this->formatPhpClassName($name) . 'Client');
        }

        if ($name === 'Query') {
            $name = 'Client';
        }
        return $this->formatPhpFqcn($this->formatPhpClassName($name));
    }

    /**
     * Determine the PHP type for an argument, using @expectedType.
     */
    private function resolveArgType(IntrospectionInputValue $arg, ?IntrospectionField $field = null): string
    {
        $typeRef = $arg->type;
        $unwrapped = $this->unwrapNonNull($typeRef);

        if ($unwrapped->isList()) {
            return 'array';
        }

        if ($unwrapped->isBuiltinScalar()) {
            return $this->formatScalarType($unwrapped);
        }

        if ($unwrapped->isIDScalar()) {
            $expectedType = $arg->expectedType();
            if ($expectedType !== null) {
                // loadFooFromID takes an Id — it's the conversion point
                if ($this->isLoadFromIDField($field)) {
                    return $this->formatPhpFqcn('Id');
                }
                // All other args accept the object type directly
                $name = $expectedType === 'Query' ? 'Client' : $expectedType;
                return $this->formatPhpFqcn($this->formatPhpClassName($name));
            }
            return $this->formatPhpFqcn('Id');
        }

        // Enum, input object, custom scalar, etc.
        return $this->formatPhpFqcn($this->formatOutputTypeName($unwrapped));
    }

    /**
     * Is this a "leaf" return type (scalar, list, enum)?
     */
    private function isLeafReturn(IntrospectionTypeRef $typeRef, IntrospectionField $field): bool
    {
        $unwrapped = $this->unwrapNonNull($typeRef);

        if ($unwrapped->isList()) {
            return true;
        }
        if ($unwrapped->isScalar()) {
            return true;
        }
        if ($unwrapped->isEnum()) {
            return true;
        }
        return false;
    }

    // ---- Parameter generation ----

    private function generateMethodParameter(
        IntrospectionInputValue $arg,
        Method $method,
        ?IntrospectionField $field = null,
    ): void {
        $parameter = $method->addParameter($this->argName($arg->name));

        if (!$arg->isRequired()) {
            $parameter->setNullable();
            $parameter->setDefaultValue(null);
        }

        $argType = $this->resolveArgType($arg, $field);
        $parameter->setType($argType);

        if ($arg->defaultValue !== null && $this->unwrapNonNull($arg->type)->isBuiltinScalar()) {
            $parameter->setDefaultValue(json_decode($arg->defaultValue, true));
        }
    }

    /**
     * @param IntrospectionInputValue[] $args
     */
    private function generateMethodArgsBody(Method $method, array $args, string $targetVar): void
    {
        foreach ($args as $arg) {
            // The argument goes over the wire under its schema name.
            $phpName = $this->argName($arg->name);
            if (!$arg->isRequired()) {
                $method->addBody('if (null !== $?) {', [$phpName]);
            }
            $method->addBody('$?->setArgument(?, $?);', [$targetVar, $arg->name, $phpName]);
            if (!$arg->isRequired()) {
                $method->addBody('}');
            }
        }
    }

    /**
     * @param IntrospectionInputValue[] $args
     * @return IntrospectionInputValue[]
     */
    private function sortMethodArguments(array $args): array
    {
        usort($args, static function (IntrospectionInputValue $a, IntrospectionInputValue $b) {
            return $b->isRequired() <=> $a->isRequired();
        });
        return $args;
    }

    // ---- Formatting helpers ----

    /**
     * The PHP name of the method generated for a field.
     */
    private function methodName(string $fieldName): string
    {
        return $this->identifiers?->format($fieldName, Casing::CAMEL) ?? $fieldName;
    }

    /**
     * The PHP name of an argument's parameter.
     */
    private function argName(string $argName): string
    {
        return $this->identifiers?->format($argName, Casing::CAMEL) ?? $argName;
    }

    /**
     * The PHP case names of an enum's values, keyed by value.
     *
     * A value whose formatted name would collide with another value's
     * (legacy values like Gzip next to their GZIP alias) keeps its schema
     * name, so every value keeps a case.
     *
     * @return array<string, string>
     */
    private function enumCaseNames(IntrospectionType $type): array
    {
        $values = array_map(static fn(IntrospectionEnumValue $value) => $value->name, $type->enumValues);

        $formatted = [];
        foreach ($values as $value) {
            $formatted[$value] = $this->identifiers?->format($value, Casing::SCREAMING_SNAKE) ?? $value;
        }
        $counts = array_count_values($formatted);

        $names = [];
        foreach ($formatted as $value => $name) {
            $collides = $name !== $value
                && ($counts[$name] > 1 || in_array($name, $values, true));
            $names[$value] = $collides ? $value : $name;
        }

        return $names;
    }

    /**
     * Keeps the methods of fields whose PHP name changed beyond letter case
     * as deprecated forwarders, under the name they had before identifier
     * words. PHP method names are case-insensitive, so a case-only rename
     * needs none: the old spelling still calls the new method.
     *
     * @param IntrospectionField[] $fields
     */
    private function addLegacyMethods(ClassType|InterfaceType $class, array $fields): void
    {
        foreach ($fields as $field) {
            $name = $this->methodName($field->name);
            if (strtolower($name) === strtolower($field->name) || $class->hasMethod($field->name)) {
                continue;
            }

            $method = $class->getMethod($name);
            $legacy = $method->cloneWithName($field->name);
            $legacy->setComment("@deprecated Use {$name}() instead.");
            if ($class instanceof ClassType) {
                $call = '$this->' . $name . '(' . implode(', ', array_map(
                    static fn(string $param) => '$' . $param,
                    array_keys($method->getParameters()),
                )) . ');';
                $legacy->setBody($method->getReturnType() === 'void' ? $call : 'return ' . $call);
            }
            $class->setMethods([...array_values($class->getMethods()), $legacy]);
        }
    }

    private function formatPhpClassName(string $objectName): string
    {
        $objectName = str_replace(['ID', 'JSON'], ['Id', 'Json'], $objectName);

        return match ($objectName) {
            'Function' => 'Function_',
            default => $objectName,
        };
    }

    private function formatPhpFqcn(string $className): string
    {
        return '\\' . CodeWriter::NAMESPACE . '\\' . $className;
    }

    private function formatScalarType(IntrospectionTypeRef $typeRef): string
    {
        $name = $typeRef->leafName();
        return match ($name) {
            'String' => 'string',
            'Boolean' => 'bool',
            'Int' => 'int',
            'Float' => 'float',
            'Void' => 'void',
            default => $this->formatPhpFqcn($this->formatPhpClassName($name)),
        };
    }

    private function formatOutputTypeName(IntrospectionTypeRef $typeRef): string
    {
        $name = $typeRef->leafName();
        if ($name === null) {
            return 'mixed';
        }

        return match ($name) {
            'String' => 'string',
            'Boolean' => 'bool',
            'Int' => 'int',
            'Float' => 'float',
            'Void' => 'void',
            'DateTime' => DateTimeImmutable::class,
            'Query' => 'Client',
            default => $this->formatPhpClassName($name),
        };
    }

    private function unwrapNonNull(IntrospectionTypeRef $typeRef): IntrospectionTypeRef
    {
        if ($typeRef->kind === 'NON_NULL' && $typeRef->ofType !== null) {
            return $typeRef->ofType;
        }
        return $typeRef;
    }

    /**
     * Returns true if the field is a loadFooFromID method on Query.
     * These are the only ID-typed args that should accept Id directly
     * (rather than the object type), since their purpose is to convert
     * an Id into an object.
     */
    private function isLoadFromIDField(?IntrospectionField $field): bool
    {
        if ($field === null) {
            return false;
        }
        return str_starts_with($field->name, 'load') && str_ends_with($field->name, 'FromID');
    }
}
