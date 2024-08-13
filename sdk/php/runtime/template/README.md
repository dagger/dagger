# Dagger PHP SDK

You will require the Dagger engine setup locally.
This guide assumes you have dagger up and running.

## Word of Warning

Do not submit your modules to packagist.
As composer dependencies; they will not work the way you expect.

The intended workflow for reusable modules is to install them through the Dagger CLI.

## Quickstart

### Initialise the module

To initialise the module, call the following:

```
dagger init --sdk="github.com/dagger/dagger/sdk/php" <path-to-module>
```

This will create a `dagger.json` configuration file.

The `path-to-module` argument can be omitted and the module will be initialised where the command was called from.

### Setup The Development Environment

To set up the development environment, call the following:

```
dagger develop -m <path-to-module>
```

This will generate a copy of the classes available to you from the SDK. Making it easier to see what you have to work with. It will also generate a `src/` directory if it does not exist, this is required; this is where Dagger searches for available functions.

The `-m` flag must be a git url or relative file path. The argument may be omitted if the command is called within the directory containing your module's `dagger.json`

### Check Module Functions

You can find out what methods are available by calling the following:

```
dagger functions --m <path-to-module>
```

You should find two functions:
- echo
- grep-dir

These functions are written within `src/Example.php`.
This aptly named file demonstrates how DaggerFunctions should be written.

```php
<?php

// ...

use function Dagger\dag;

#[DaggerObject]
class Example
{
     #[DaggerFunction('Echo the value to standard output')]
     public function containerEcho(string $stringArg): Container
     {
         return dag()
             ->container()
             ->from('alpine:latest')
             ->withExec(['echo', $value]);
     }

     // ...
}
```

You'll notice it uses a function called `dag`; this is how you get the currently connected instance of the Dagger Client.

### Call Module Functions

```text
dagger call -m <path-to-module> container-echo --string-arg="hello world"
```

## How Dagger Finds Available Functions

Dagger searches your `src/` directory for `Dagger Objects`, when it finds `Dagger Objects`, it searches them for `Dagger Functions`.

That's it.
So you only need to know how to define a `Dagger Object` and a `Dagger Function`.

### Dagger Objects

A `Dagger Object` is any class with the `#[DaggerObject]` Attribute.

```php
<?php

namespace DaggerModule;

use Dagger\Attribute\DaggerObject;

#[DaggerObject]
class Example
{
}
```

That's a `Dagger Object` technically, albeit useless without any `Dagger Functions`.

### Dagger Functions

A `Dagger Function` is a **public** method on a `Dagger Object` with the `#[DaggerFunction]` Attribute.

Use the following examples for reference:

```php
<?php

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;

#[DaggerObject]
class Example
{
    #[DaggerFunction]
    public function myCoolDaggerFunction(): void
    {
        /**
         * This is a Dagger Function:
         * - It has public visibility.
         * - It has the DaggerFunction Attribute.
         */
    }

    #[DaggerFunction('documentation for the function')]
    public function myDocumentedDaggerFunction(): void
    {
        /**
         * This is a Dagger Function:
         * - It has public visibility.
         * - It has the DaggerFunction Attribute.
         */
    }

    private function myPublicMethod(): void
    {
        /**
         * This is not a Dagger Function:
         * - It is missing the DaggerFunction Attribute.
         */
    }

    #[DaggerFunction]
    private function myPrivateMethodWithAPointlessAttribute(): void
    {
        /**
         * This is not a Dagger Function:
         * - It has private visibility.
         */
    }

    private function myPrivateMethod(): void
    {
        /**
         * This is not a Dagger Function:
         * - It has private visibility.
         * - It is missing the DaggerFunction Attribute.
         */
    }
}
```

### Arguments

All parameters on a `Dagger Function` are considered providable arguments,
if you want additional metadata on an argument, such as a doc-string; 
use the `#[Argument]` Attribute.

If any of your arguments, or return values, are arrays. Please see the section on [Lists](#lists)

Use the following examples for reference:

```php
<?php

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Argument;

#[DaggerObject]
class Example
{
     //...

     #[DaggerFunction]
     public function myCoolDaggerFunction(
         string $value,
     ): string {
         // do something...
     }

     #[DaggerFunction]
     public function myEquallyCoolDaggerFunction(
         #[Argument]
         string $value,
     ): string {
         // do something...
     }

     #[DaggerFunction('documentation for function')]
     public function myWellDocumentedDaggerFunction(
         #[Argument('documentation for argument')]
         string $value,
     ): string {
         // do something...
     }

     // ...
}
```

#### Lists

Lists must have their subtype specified.
Specifying subtypes on arguments MUST be done using the `ListOfType` attribute, do not rely on annotations for this.

```php
<?php

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Argument;
use Dagger\Attribute\ListOfType;

#[DaggerObject]
class Example
{
     #[DaggerFunction('The subtype of an array MUST be specified')]
     public function myInvalidList(
         array $value,
     ): string {
         // do something...
     }

     /**
     * @param int[] $value
     */
     #[DaggerFunction('Annotations are not supported')]
     public function myStillInvalidList(
         array $value,
     ): string {
         // do something...
     }

     #[DaggerFunction('ListOfType attribute is supported')]
     public function myValidList(
         #[ListOfType('int')]
         array $value,
     ): string {
         // do something...
     }

     // ...
}
```

Specifying subtypes on return values MUST be done using the `ReturnsListOfType` attribute, do not rely on annotations for this.

```php
<?php

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Argument;
use Dagger\Attribute\ReturnsListOfType;

#[DaggerObject]
class Example
{
     #[DaggerFunction('The subtype of an array MUST be specified')]
     public function myInvalidList(): array
     {
         // do something...
     }

     /**
     * @return int[]
     */
     #[DaggerFunction('Annotations are not supported')]
     public function myStillInvalidList(): array
     {
         // do something...
     }

     #[DaggerFunction('ReturnsListOfType attribute is supported')]
     #[ReturnsListOfType('int')]
     public function myValidList(): array 
     {
         // do something...
     }

     // ...
}
```
