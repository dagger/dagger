# Dagger PHP SDK #

You will require the Dagger engine setup locally, and it is currently up and running.

## Quickstart ##

### Initialise the module ###

To initialise the module, call the following:

```
dagger init --sdk="sdk/php" <path-to-module>
```

This will create a `dagger.json` configuration file.

The `path-to-module` argument can be omitted and the module will be initialised where the command was called from.

### Setup The Development Environment ###

To setup the development environment, call the following:

```
dagger develop --mod=<path-to-module>
```

This will generate a copy of the classes available to you from the SDK. Making it easier to see what you have to work with. It will also generate a `src/` directory, this is required. This is where Dagger will search for available functions.

The `--mod` option must be a git url, or relative file path. The argument may be omitted if the command is called within the directory containing your module's `dagger.json` 

### Check Module Functions ###

You can find out what methods are available by calling the following:

```
dagger functions --mod=<path-to-module>
```

The `--mod` option must be a git url, or relative file path. The argument may be omitted if the command is called within the directory containing your module's `dagger.json` 

You should find two functions:
- echo
- grep-dir

These functions are written within `src/Example.php`.  
This aptly named file demonstrates how DaggerFunctions should be written.


### Call Module Functions ###

```text
dagger call echo  --value="hello world" -mod=<path-to-your-module>
```

The `--mod` option must be a git url, or relative file path. The argument may be omitted if the command is called within the directory containing your module's `dagger.json` 

## How Dagger Finds Available Functions ##

Dagger searches your `src/` directory for `Dagger Objects`, when it finds `Dagger Objects`, it searches them for `Dagger Functions`.

That's it.  
So you only need to know how to define a `Dagger Object` and a `Dagger Function`.

### Dagger Objects ###

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

That's a `Dagger Object` technically, albeit a useless one as it has no `Dagger Functions`

### Dagger Functions ###

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

### Dagger Arguments ###

All parameters on a `Dagger Function` are considered to be an argument, but if you want to add metadata or simply be more explicit; you will need to add use the `#[DaggerArgument]` Attribute.

Use the following examples for reference:

```php
<?php

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;

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
         #[DaggerArgument]
         string $value,
     ): string {
         // do something...
     }
     
     #[DaggerFunction('documentation for function')]
     public function myWellDocumentedDaggerFunction(
         #[DaggerArgument('documentation for argument')]
         string $value,
     ): string {
         // do something...
     }
     
     // ...
}
```
