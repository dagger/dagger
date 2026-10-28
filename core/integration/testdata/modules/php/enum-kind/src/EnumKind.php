<?php

declare(strict_types=1);

namespace DaggerModule;

use Dagger\Attribute\{DaggerFunction, DaggerObject};
use Dagger\NetworkProtocol;

#[DaggerObject] class EnumKind
{
    #[DaggerFunction] public function oppositeNetworkProtocol(
        NetworkProtocol $arg,
    ): NetworkProtocol {
        return match ($arg) {
            NetworkProtocol::TCP => NetworkProtocol::UDP,
            NetworkProtocol::UDP => NetworkProtocol::TCP,
        };
    }

    #[DaggerFunction] public function toggleTodo(
        Task $task,
    ): Task {
        return match ($task) {
            Task::Todo => Task::Done,
            Task::Done => Task::Todo,
        };
    }

    #[DaggerFunction] public function backingValue(
        Task $task,
    ): string {
        return $task->value;
    }

    #[DaggerFunction] public function defaultedTask(
        Task $task = Task::Done,
    ): Task {
        return $task;
    }

    #[DaggerFunction] public function optionalTask(
        ?Task $task,
    ): string {
        return $task?->name ?? 'none';
    }

    #[DaggerFunction] public function raiseLevel(
        Level $level,
    ): Level {
        return match ($level) {
            Level::Low => Level::High,
            Level::High => Level::High,
        };
    }

    #[DaggerFunction] public function doubleWeight(
        Weight $weight,
    ): int {
        return $weight->value * 2;
    }
}
