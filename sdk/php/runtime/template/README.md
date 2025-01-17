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
dagger init --sdk="php" <path-to-module>
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
dagger functions -m <path-to-module>
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
     #[DaggerFunction]
     #[Doc('Echo the value to standard output')]
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
If you want to add a doc-string for your function use the `#[Doc]` Attribute.

Use the following examples for reference:

```php
<?php

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;

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

    #[DaggerFunction]
    #[Doc('documentation for the function')]
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

#### Constructor

You can expose PHP's magic method `__construct` as a `DaggerFunction`.
If you do this, Dagger will use it as a constructor.

This is only applicable to the base class of your Module: the class named identically to your Module.

Let's say your module, among other things can run tests on a given source directory.
It may look like this:

```php
#[DaggerObject]
final class MyModule
{
    #[DaggerFunction]
    public function test(Directory $source): Container
    {
        // ...
    }

    // ...
```

We could then call tests like so:

```
dagger call test --dir="path/to/dir"
```

But if multiple methods require a source directory, it may be better to supply a constructor.

```php
#[DaggerObject]
final class MyModule
{
    #[DaggerFunction]
    public function __construct(
        public Directory $source
    ) {
    }

    #[DaggerFunction]
    public function test(): Container
    {
        // ...
    }

    // ...
```

We could then call tests like so:

```
dagger call --dir="path/to/dir" test
```

### Arguments

All parameters on a `Dagger Function` are considered providable arguments,
If you want to add a doc-string for your argument use the `#[Doc]` Attribute.

If any of your arguments, or return values, are arrays. Please see the section on [Lists](#lists)

Use the following examples for reference:

```php
<?php

namespace DaggerModule;

use Dagger\Attribute\DaggerFunction;
use Dagger\Attribute\DaggerObject;
use Dagger\Attribute\Doc;

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

     #[DaggerFunction]
     #[Doc('documentation for function')]
     public function myWellDocumentedDaggerFunction(
         #[Doc('documentation for argument')]
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
use Dagger\Attribute\Doc;
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
     #[DaggerFunction]
     #[Doc('Annotations are not supported')]
     public function myStillInvalidList(
         array $value,
     ): string {
         // do something...
     }

     #[DaggerFunction]
     #[Doc('ListOfType attribute is supported')]
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
use Dagger\Attribute\Doc;
use Dagger\Attribute\ReturnsListOfType;

#[DaggerObject]
class Example
{
     #[DaggerFunction]
     #[Doc('The subtype of an array MUST be specified')]
     public function myInvalidList(): array
     {
         // do something...
     }

     /**
     * @return int[]
     */
     #[DaggerFunction]
     #[Doc('Annotations are not supported')]
     public function myStillInvalidList(): array
     {
         // do something...
     }

     #[DaggerFunction]
     #[Doc(('ReturnsListOfType attribute is supported'))]
     #[ReturnsListOfType('int')]
     public function myValidList(): array
     {
         // do something...
     }

     // ...
}
```

#### Directories and Files

[Directories and Files can also specify additional meta data.](https://docs.dagger.io/manuals/developer/functions/#directories-and-files)


##### Default Paths

Default paths allow your module access files outside the *source directory* specified in your `dagger.json` file.
Note that default paths are only applicable for paths within your project:
- If it is a Git Repository, it is restricted to files in the same Git Repository.
- If it is a non-repository, then it is restricted to files and sub-directories of the directory containing your `dagger.json` file.

Note that you cannot easily instantiate a `Directory` or `File` object from scratch.
So the standard way to specify defaults in PHP would be hard to apply.

Instead, specify a `DefaultPath` attribute with a `string` path.

If an absolute path is specified:
- in a Git repository (defined by the presence of a .git sub-directory), the default context is the root of the Git repository.
- in a non-repository location (defined by the absence of a .git sub-directory), the default context is the directory containing a `dagger.json` file.

If a relative path is specified the default context is always the directory containing a `dagger.json` file.

A default path is specified like so:

```php
#[DaggerFunction]
public function myDaggerFunction(
    #[DefaultPath('.')]
    Directory $dir,
): Container {
    // ...
}
```
A default path of `.` returns the directory containing your `dagger.json` file.

If your `dagger.json` file is located in `~/my-project/src/`:

- `.` resolves to `~/my-project/src/`
- `..` resolves to `~/my-project/` **if it is a Git Repository** otherwise this is not allowed. 
- `/`, if `~/my-project/` is a Git repository, resolves to `~/my-project/` because that is the root of your Git repository.
- `/`, if `~/my-project/` is not a Git repository, resolves to `~/my-project/src/` because that is the directory containing your `dagger.json` file.

- `./README.md` resolves to `~/my-project/src/README.md`
- `../README.md` resolves to `~/my-project/README.md` **if it is a Git Repository** otherwise this is not allowed.
- `/README.md`, if `~/my-project/` is a Git repository, resolves to `~/my-project/README.md` because that is the root of your Git repository.
- `/`, if `~/my-project/` is not a Git repository, resolves to `~/my-project/src/README` because that is the directory containing your `dagger.json` file.

For further detail, refer to [Directories and Files](https://docs.dagger.io/manuals/developer/functions/#directories-and-files).

##### Ignore

For Directories only: an `Ignore` Attribute can specify any number of strings for files|directories to ignore.

The following example would ignore your `vendor/` and `tests/` directories:

```php
#[DaggerFunction]
public function myDaggerFunction(
    #[DefaultPath('.')]
    #[Ignore('vendor/', 'tests/')]
    Directory $dir,
): Container {
    // ...
}
```

`Ignore` follows the [`.gitignore` syntax](https://git-scm.com/docs/gitignore/en) and should be referred to for further detail.
