<?php

namespace Dagger\Client;

use GraphQL\Util\StringLiteralFormatter;
use Stringable;

abstract class AbstractInputObject implements Stringable
{
    /**
     * The schema names of the properties named otherwise: a renamed field
     * keeps its old property name, and is sent under its schema name.
     *
     * @var array<string, string>
     */
    protected const FIELD_NAMES = [];

    public function __toString(): string
    {
        $objectString = '{';
        $first = true;

        foreach ($this as $name => $value) {
            if (empty($value)) {
                continue;
            }

            // Append space at the beginning if it's not the first item on the list
            if ($first) {
                $first = false;
            } else {
                $objectString .= ', ';
            }

            // Convert argument values to graphql string literal equivalent
            if (is_scalar($value)) {
                // Convert scalar value to its literal in graphql
                $value = StringLiteralFormatter::formatValueForRHS($value);
            } elseif (is_array($value)) {
                // Convert PHP array to its array representation in graphql arguments
                $value = StringLiteralFormatter::formatArrayForGQLQuery($value);
            }
            $objectString .= (static::FIELD_NAMES[$name] ?? $name) . ': ' . $value;
        }
        $objectString .= '}';

        return $objectString;
    }
}
