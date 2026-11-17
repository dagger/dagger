using Dagger;

/// <summary>
/// A module for HelloWithServicesCs functions
/// </summary>
[Object]
public class HelloWithServicesCs
{
    /// <summary>
    /// Returns a web server service
    /// </summary>
    [Function]
    [Up]
    public Service Web()
    {
        return Dag.Container().From("nginx:alpine").WithExposedPort(80).AsService();
    }

    /// <summary>
    /// Returns a redis service
    /// </summary>
    [Function]
    [Up]
    public Service Redis()
    {
        return Dag.Container().From("redis:alpine").WithExposedPort(6379).AsService();
    }

    /// <summary>
    /// Returns the Infra object whose services surface as infra:database
    /// </summary>
    [Function]
    public Infra Infra()
    {
        return new Infra();
    }
}

/// <summary>
/// Namespaced services, mirroring the go/python fixtures
/// </summary>
[Object]
public class Infra
{
    /// <summary>
    /// Returns a postgres database service
    /// </summary>
    [Function]
    [Up]
    public Service Database()
    {
        return Dag.Container()
            .From("postgres:alpine")
            .WithEnvVariable("POSTGRES_PASSWORD", "test")
            .WithExposedPort(5432)
            .AsService();
    }
}
