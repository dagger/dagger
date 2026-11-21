<?php

declare(strict_types=1);

namespace Dagger\Module;

use Dagger\Attribute;
use Dagger\ValueObject\Argument;
use Dagger\ValueObject\DaggerFunction;
use Dagger\ValueObject\Type;
use InvalidArgumentException;
use ReflectionClass;
use ReflectionMethod;

final readonly class CallDispatcher
{
    public function __construct(
        private ValueCodec $codec,
        private string $namespace = 'DaggerModule',
    ) {
    }

    /**
     * @param string $receiverType the name the entrypoint's types gave the object
     * @param ?string $receiverValue the object's state as JSON text
     * @param string $functionName empty for the constructor
     * @param ?string $arguments the arguments, by name, as JSON text
     *
     * @return string the result as JSON text
     */
    public function call(
        string $receiverType,
        ?string $receiverValue,
        string $functionName,
        ?string $arguments,
    ): string {
        $class = sprintf('%s\\%s', $this->namespace, $receiverType);
        if (!class_exists($class)) {
            throw new InvalidArgumentException("No class $class exists; run dagger generate");
        }

        $values = self::decodeObjectText($arguments, 'fnArgs');

        return $functionName === ''
            ? $this->construct(new ReflectionClass($class), $values)
            : $this->invoke(new ReflectionClass($class), $receiverValue, $functionName, $values);
    }

    /** @param array<string, mixed> $values */
    private function construct(ReflectionClass $class, array $values): string
    {
        $constructor = $class->getConstructor();

        // A constructor without the attribute is not part of the module, so the
        // engine has no arguments to give it.
        $arguments = $constructor === null || $constructor->getAttributes(Attribute\DaggerFunction::class) === []
            ? []
            : $this->arguments($constructor, $values);

        return $this->codec->encode($class->newInstanceArgs($arguments));
    }

    /** @param array<string, mixed> $values */
    private function invoke(
        ReflectionClass $class,
        ?string $receiverValue,
        string $functionName,
        array $values,
    ): string {
        if (!$class->hasMethod($functionName)) {
            throw new InvalidArgumentException(
                "$class->name has no function $functionName; run dagger generate",
            );
        }

        $method = $class->getMethod($functionName);
        $function = DaggerFunction::fromReflection($method);
        $receiver = $this->codec->decode(
            self::decodeObjectText($receiverValue, 'receiverValue'),
            new Type($class->name),
        );

        return $this->codec->encode(
            $method->invokeArgs($receiver, $this->arguments($method, $values)),
            $function->returnType,
        );
    }

    /**
     * An omitted argument is left out, so PHP applies the default it declares,
     * while a null given by the caller reaches the function as null.
     *
     * @param array<string, mixed> $values
     * @return array<string, mixed>
     */
    private function arguments(ReflectionMethod $method, array $values): array
    {
        $unknown = array_diff(
            array_keys($values),
            array_map(static fn($parameter) => $parameter->getName(), $method->getParameters()),
        );
        if ($unknown !== []) {
            throw new InvalidArgumentException(sprintf(
                '%s::%s has no argument %s; run dagger generate',
                $method->class,
                $method->getName(),
                implode(', ', $unknown),
            ));
        }

        $arguments = [];
        foreach ($method->getParameters() as $parameter) {
            $argument = Argument::fromReflection($parameter);

            if (array_key_exists($argument->name, $values)) {
                $arguments[$argument->name] = $this->codec->decode($values[$argument->name], $argument->type);
            } elseif (!$parameter->isDefaultValueAvailable()) {
                $arguments[$argument->name] = $argument->type->nullable
                    ? null
                    : throw new InvalidArgumentException(sprintf(
                        '%s::%s is missing the required argument %s',
                        $method->class,
                        $method->getName(),
                        $argument->name,
                    ));
            }
        }

        return $arguments;
    }

    /** @return array<string, mixed> */
    private static function decodeObjectText(?string $text, string $what): array
    {
        if ($text === null || trim($text) === '') {
            return [];
        }

        try {
            $decoded = json_decode($text, true, flags: JSON_THROW_ON_ERROR);
        } catch (\JsonException $e) {
            throw new InvalidArgumentException("$what is not valid JSON: {$e->getMessage()}", previous: $e);
        }

        return match (true) {
            $decoded === null => [],
            is_array($decoded) => $decoded,
            default => throw new InvalidArgumentException("$what is not a JSON object: $text"),
        };
    }
}
