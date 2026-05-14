<?php

declare(strict_types=1);

namespace Dagger\Tests\Unit\Fixture\Enums;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\ValueObject;

#[DaggerObject]
class ObjectUsingEnum
{
    #[DaggerFunction]
    public function setStatus(Status $status): Status
    {
        return $status;
    }

    #[DaggerFunction]
    public function setPriority(Priority $priority): Priority
    {
        return $priority;
    }

    public static function getValueObjectEquivalent(): ValueObject\DaggerObject
    {
        $statusType = new ValueObject\Type(Status::class);
        $priorityType = new ValueObject\Type(Priority::class);

        return new ValueObject\DaggerObject(
            name: self::class,
            description: '',
            fields: [],
            functions: [
                new ValueObject\DaggerFunction(
                    name: 'setStatus',
                    description: null,
                    arguments: [
                        new ValueObject\Argument(
                            name: 'status',
                            description: '',
                            type: $statusType,
                        ),
                    ],
                    returnType: $statusType,
                ),
                new ValueObject\DaggerFunction(
                    name: 'setPriority',
                    description: null,
                    arguments: [
                        new ValueObject\Argument(
                            name: 'priority',
                            description: '',
                            type: $priorityType,
                        ),
                    ],
                    returnType: $priorityType,
                ),
            ],
        );
    }
}
