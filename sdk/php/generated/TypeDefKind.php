<?php

/**
 * This class has been generated by dagger-php-sdk. DO NOT EDIT.
 */

declare(strict_types=1);

namespace Dagger\Dagger;

/**
 * Distinguishes the different kinds of TypeDefs.
 */
enum TypeDefKind: string
{
    /** A boolean value */
    case BooleanKind = 'BooleanKind';

    /** An integer value */
    case IntegerKind = 'IntegerKind';

    /**
     * A list of values all having the same type.
     *
     * Always paired with a ListTypeDef.
     */
    case ListKind = 'ListKind';

    /**
     * A named type defined in the GraphQL schema, with fields and functions.
     *
     * Always paired with an ObjectTypeDef.
     */
    case ObjectKind = 'ObjectKind';

    /** A string value */
    case StringKind = 'StringKind';

    /**
     * A special kind used to signify that no value is returned.
     *
     * This is used for functions that have no return value. The outer TypeDef
     * specifying this Kind is always Optional, as the Void is never actually
     * represented.
     */
    case VoidKind = 'VoidKind';
}
