using Dagger;

/// <summary>
/// A module for HelloWithGeneratorsCs functions
/// </summary>
[Object]
public class HelloWithGeneratorsCs
{
    /// <summary>
    /// Return a changeset with a new file
    /// </summary>
    [Function]
    [Generate]
    public Changeset GenerateFiles()
    {
        return Dag.Directory().WithNewFile("foo", "bar").Changes(Dag.Directory());
    }

    /// <summary>
    /// Return a changeset with a new file
    /// </summary>
    [Function]
    [Generate]
    public Changeset GenerateOtherFiles()
    {
        return Dag.Directory().WithNewFile("bar", "foo").Changes(Dag.Directory());
    }

    /// <summary>
    /// Return an empty changeset
    /// </summary>
    [Function]
    [Generate]
    public Changeset EmptyChangeset()
    {
        return Dag.Directory().Changes(Dag.Directory());
    }

    /// <summary>
    /// Return an error
    /// </summary>
    [Function]
    [Generate]
    public Changeset ChangesetFailure()
    {
        throw new InvalidOperationException("could not generate the changeset");
    }

    /// <summary>
    /// Returns the MetaGen object whose generators surface as other-generators:gen-things
    /// </summary>
    [Function]
    public MetaGen OtherGenerators()
    {
        return new MetaGen();
    }
}

/// <summary>
/// Namespaced generators, mirroring the go/python fixtures
/// </summary>
[Object]
public class MetaGen
{
    /// <summary>
    /// Return a changeset with a new file
    /// </summary>
    [Function]
    [Generate]
    public Changeset GenThings()
    {
        return Dag.Directory().WithNewFile("meta-gen", "generated").Changes(Dag.Directory());
    }
}
