### Added

* engine: add direct `File()` method to API for easier file creation
  * Simplifies file creation with a more intuitive API
  * Reduces code verbosity compared to using Directory().WithNewFile()
  * Maintains backward compatibility with existing Directory API
  * Example: `dag.File("file.txt", "content")` vs `dag.Directory().WithNewFile("file.txt", "content").File("file.txt")`

### Pull Request

* https://github.com/dagger/dagger/pull/9116

### Author

* @devin
