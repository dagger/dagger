<?php

declare(strict_types=1);

namespace Dagger\Codegen\Introspection;

use DateTimeImmutable;
use Dagger\Client\AbstractClient;
use Dagger\Client\AbstractId;
use Dagger\Client\AbstractInputObject;
use Dagger\Client\AbstractObject;
use Dagger\Client\AbstractScalar;
use Dagger\Client\IdAble;
use Dagger\Codegen\CodeWriter;
use Nette\PhpGenerator\ClassType;
use Nette\PhpGenerator\EnumType;
use Nette\PhpGenerator\InterfaceType;
use Nette\PhpGenerator\Method;

/**
 * Codegen visitor that works with raw introspection data,
 * supporting @expectedType directives and first-class interfaces.
 */
class NewCodegenVisitor extends CodeWriter
{
    public function __construct(
        private readonly IntrospectionSchema $schema,
        string $targetDirectory
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

        if (str_ends_with($type->name, 'ID')) {
            $scalarClass->setExtends(AbstractId::class);
        } else {
            $scalarClass->setExtends(AbstractScalar::class);
        }

        $this->write($scalarClass);
    }

    /**
     * Generate a per-type ID class (e.g. ContainerId) for types that have an id field.
     * Under the unified ID scalar, these are no longer in the schema as separate scalars,
     * but we still generate them for type safety and backward compatibility.
     */
    public function visitIdType(IntrospectionType $type): void
    {
        $phpClassName = $this->formatPhpClassName($type->name) . 'Id';

        $idClass = new ClassType($phpClassName);
        $idClass->setReadOnly(true);
        $idClass->setExtends(AbstractId::class);
        $idClass->addComment(sprintf(
            'The `%sID` scalar type represents an identifier for an object of type %s.',
            $type->name,
            $type->name,
        ));

        $this->write($idClass);
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
            $phpParameterType = $fieldType->isBuiltinScalar()
                ? $this->formatScalarType($fieldType)
                : $this->formatPhpFqcn($this->formatOutputTypeName($fieldType));

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

        foreach ($type->enumValues as $value) {
            $case = $enumClass->addCase($value->name, $value->name);
            if ($value->description !== null) {
                $case->addComment($value->description);
            }
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

        $this->write($clientClass);
    }

    // ---- Method generation ----

    private function generateObjectMethod(
        ClassType $class,
        IntrospectionField $field,
        IntrospectionType $parentType,
        bool $isInterfaceClient = false,
    ): void {
        $method = $class->addMethod($field->name);
        if ($field->description !== null) {
            $method->addComment($field->description);
        }

        $returnType = $field->type;
        $isConvertID = $field->isConvertID();

        // Determine the PHP return type
        if ($isConvertID) {
            // sync()-like: returns the parent object type
            $returnTypeName = $this->formatPhpFqcn($this->formatPhpClassName(
                $parentType->name === 'Query' ? 'Client' : $parentType->name
            ));
            $method->setReturnType($returnTypeName);
            $method->setReturnNullable(false);
        } else {
            $phpReturnType = $this->resolveReturnType($returnType, $field);
            if ($returnType->isNonNull()) {
                $method->setReturnNullable(false);
            }
            $method->setReturnType($phpReturnType);
        }

        // Generate parameters
        $sortedArgs = $this->sortMethodArguments($field->args);
        foreach ($sortedArgs as $arg) {
            $this->generateMethodParameter($arg, $method);
        }

        // Generate method body
        if ($isConvertID) {
            // sync()-like: execute the query to force evaluation, return self
            $method->addBody('$leafQueryBuilder = new \Dagger\Client\QueryBuilder(?);', [$field->name]);
            $this->generateMethodArgsBody($method, $sortedArgs, 'leafQueryBuilder');
            $method->addBody(
                '$this->queryLeaf($leafQueryBuilder, ?);',
                [$field->name]
            );
            $method->addBody('return $this;');
        } elseif ($this->isLeafReturn($returnType, $field)) {
            // Scalar/list/enum return: use queryLeaf
            $method->addBody('$leafQueryBuilder = new \Dagger\Client\QueryBuilder(?);', [$field->name]);
            $this->generateMethodArgsBody($method, $sortedArgs, 'leafQueryBuilder');

            $unwrapped = $this->unwrapNonNull($returnType);

            if ($unwrapped->isIDScalar()) {
                // ID scalar: wrap in the typed ID class
                $expectedType = $field->expectedType();
                // For `id` fields, infer from parent type name
                if ($expectedType === null && $field->name === 'id' && $field->parentTypeName !== null) {
                    $expectedType = $field->parentTypeName;
                }
                if ($expectedType !== null) {
                    $idTypeName = $this->formatPhpFqcn($this->formatPhpClassName($expectedType) . 'Id');
                    $method->addBody(
                        'return new ' . $idTypeName . '((string)$this->queryLeaf($leafQueryBuilder, ?));',
                        [$field->name]
                    );
                } else {
                    $method->addBody(
                        'return (string)$this->queryLeaf($leafQueryBuilder, ?);',
                        [$field->name]
                    );
                }
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
        $method = $interface->addMethod($field->name);
        if ($field->description !== null) {
            $method->addComment($field->description);
        }

        $returnType = $field->type;
        $isConvertID = $field->isConvertID();

        if ($isConvertID) {
            $returnTypeName = $this->formatPhpFqcn($this->formatPhpClassName($parentType->name));
            $method->setReturnType($returnTypeName);
            $method->setReturnNullable(false);
        } else {
            $phpReturnType = $this->resolveReturnType($returnType, $field);
            if ($returnType->isNonNull()) {
                $method->setReturnNullable(false);
            }
            $method->setReturnType($phpReturnType);
        }

        $sortedArgs = $this->sortMethodArguments($field->args);
        foreach ($sortedArgs as $arg) {
            $this->generateMethodParameter($arg, $method);
        }
    }

    // ---- Type resolution ----

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
            // ID scalar: determine the expected type from directive or parent context
            $expectedType = $field->expectedType();
            if ($expectedType !== null) {
                return $this->formatPhpFqcn($this->formatPhpClassName($expectedType) . 'Id');
            }
            // For `id` fields, infer the type from the parent
            if ($field->name === 'id' && $field->parentTypeName !== null) {
                return $this->formatPhpFqcn($this->formatPhpClassName($field->parentTypeName) . 'Id');
            }
            // Bare ID without expectedType
            return 'string';
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
    private function resolveArgType(IntrospectionInputValue $arg): string
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
            // Check for @expectedType directive
            $expectedType = $arg->expectedType();
            if ($expectedType !== null) {
                // Accept both the ID type and the object type
                $idClass = $this->formatPhpFqcn($this->formatPhpClassName($expectedType) . 'Id');
                $objClass = $this->formatPhpFqcn($this->formatPhpClassName(
                    $expectedType === 'Query' ? 'Client' : $expectedType
                ));

                // Check if the expected type is an interface
                $schemaType = $this->schema->getType($expectedType);
                if ($schemaType !== null && $schemaType->isInterface()) {
                    // For interface args, just accept the interface type
                    return $objClass;
                }

                return $idClass . '|' . $objClass;
            }
            // Bare ID
            return 'string';
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

    private function generateMethodParameter(IntrospectionInputValue $arg, Method $method): void
    {
        $parameter = $method->addParameter($arg->name);

        if (!$arg->isRequired()) {
            $parameter->setNullable();
            $parameter->setDefaultValue(null);
        }

        $argType = $this->resolveArgType($arg);
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
            if (!$arg->isRequired()) {
                $method->addBody('if (null !== $?) {', [$arg->name]);
            }
            $method->addBody('$?->setArgument(?, $?);', [$targetVar, $arg->name, $arg->name]);
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
}
