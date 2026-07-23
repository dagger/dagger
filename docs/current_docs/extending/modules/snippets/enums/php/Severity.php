<?php

namespace DaggerModule;

use Dagger\Attribute\{DaggerObject, Doc};

#[DaggerObject]
#[Doc('Vulnerability severity levels.')]
enum Severity: string
{
    #[Doc('Undetermined risk; analyze further.')]
    case Unknown = 'UNKNOWN';

    #[Doc('Minimal risk; routine fix.')]
    case Low = 'LOW';

    #[Doc('Moderate risk; timely fix.')]
    case Medium = 'MEDIUM';

    #[Doc('Serious risk; quick fix needed.')]
    case High = 'HIGH';

    #[Doc('Severe risk; immediate action.')]
    case Critical = 'CRITICAL';
}
