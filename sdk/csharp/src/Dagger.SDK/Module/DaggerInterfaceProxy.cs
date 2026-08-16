using System.Collections.Immutable;
using System.Reflection;
using Dagger.GraphQL;

namespace Dagger.ModuleRuntime;

/// <summary>
/// DispatchProxy-based implementation of interfaces that routes method calls to the GraphQL API.
/// </summary>
/// <summary>
/// Marker for dynamic interface proxies, exposing the wrapped value's id so
/// results and state can be normalized to id strings.
/// </summary>
internal interface IDaggerInterfaceProxy
{
    string ProxyId { get; }
}

internal class DaggerInterfaceProxy<T> : DispatchProxy, IDaggerInterfaceProxy
    where T : class
{
    private Query? _dag;
    private string? _interfaceName;
    private string? _id;

    // Query-chained mode: the proxy is positioned at an interface value in an
    // unevaluated query (e.g. the return of a chainable interface method) and
    // has no id yet; the id is resolved lazily if needed.
    private QueryBuilder? _query;

    string IDaggerInterfaceProxy.ProxyId
    {
        get
        {
            if (_id != null)
            {
                return _id;
            }

            if (_query == null || _dag == null)
            {
                throw new InvalidOperationException("Proxy not properly initialized");
            }

            // Console module runtime: no synchronization context, safe to block.
            var id = QueryExecutor
                .ExecuteAsync<Id>(_dag.GraphQLClient, _query.Select("id"), default)
                .GetAwaiter()
                .GetResult();
            _id = id.Value;
            return _id;
        }
    }

    public static T Create(Query dag, string interfaceName, string id)
    {
        var proxy = NewProxy();
        proxy._dag = dag;
        proxy._interfaceName = interfaceName;
        proxy._id = id;
        return proxy as T ?? throw new InvalidOperationException("Proxy cast failed");
    }

    /// <summary>
    /// Creates a proxy positioned at the given query (no id yet), used for
    /// chainable interface-returning methods.
    /// </summary>
    public static T CreateFromQuery(Query dag, string interfaceName, QueryBuilder query)
    {
        var proxy = NewProxy();
        proxy._dag = dag;
        proxy._interfaceName = interfaceName;
        proxy._query = query;
        return proxy as T ?? throw new InvalidOperationException("Proxy cast failed");
    }

    private static DaggerInterfaceProxy<T> NewProxy()
    {
        var proxy = Create<T, DaggerInterfaceProxy<T>>() as DaggerInterfaceProxy<T>;
        if (proxy == null)
        {
            throw new InvalidOperationException(
                $"Failed to create proxy for interface {typeof(T).Name}"
            );
        }

        return proxy;
    }

    protected override object? Invoke(MethodInfo? targetMethod, object?[]? args)
    {
        if (
            targetMethod == null
            || _dag == null
            || _interfaceName == null
            || (_id == null && _query == null)
        )
        {
            throw new InvalidOperationException("Proxy not properly initialized");
        }

        // Special case: Id() - return the known interface id directly.
        if (
            targetMethod.Name.Equals("Id", StringComparison.OrdinalIgnoreCase)
            && targetMethod.ReturnType == typeof(Task<string>)
            && _id != null
        )
        {
            return Task.FromResult(_id);
        }

        // Position at the interface value: either the pending query (chained
        // proxies) or the Relay-style node lookup by id:
        // query { node(id: "...") { ... on InterfaceExampleProcessor { methodName(args...) } } }
        QueryBuilder queryBuilder;
        if (_query != null)
        {
            queryBuilder = _query;
        }
        else
        {
            var arguments = ImmutableList<Argument>.Empty;
            arguments = arguments.Add(new Argument("id", new StringValue(_id!)));
            queryBuilder = _dag
                .QueryBuilder.Select("node", arguments)
                .Select("... on " + _interfaceName);
        }

        // Add the interface method call
        var methodName =
            char.ToLowerInvariant(targetMethod.Name[0]) + targetMethod.Name.Substring(1);
        var methodArgs = ImmutableList<Argument>.Empty;

        var parameters = targetMethod.GetParameters();
        args ??= Array.Empty<object>();

        for (int i = 0; i < parameters.Length && i < args.Length; i++)
        {
            var paramName = parameters[i].Name ?? $"arg{i}";
            var argValue = args[i];

            // Generated async signatures carry a trailing CancellationToken;
            // it is client-side plumbing, never a GraphQL argument.
            if (parameters[i].ParameterType == typeof(CancellationToken))
            {
                continue;
            }

            if (argValue == null)
            {
                continue;
            }

            // Convert argument to GraphQL value
            Value graphqlValue = ConvertToGraphQLValue(argValue);

            methodArgs = methodArgs.Add(new Argument(paramName, graphqlValue));
        }

        queryBuilder = queryBuilder.Select(methodName, methodArgs);

        // Execute the query and return result
        var returnType = targetMethod.ReturnType;

        // Plain Task (void-returning async interface method): execute for
        // effect; Task<Void> is assignable to Task.
        if (returnType == typeof(Task))
        {
            return QueryExecutor.ExecuteAsync<Void?>(_dag.GraphQLClient, queryBuilder, default);
        }

        // Handle async methods
        if (returnType.IsGenericType && returnType.GetGenericTypeDefinition() == typeof(Task<>))
        {
            var resultType = returnType.GetGenericArguments()[0];

            // Async interface-valued results cannot be deserialized directly:
            // resolve ids and wrap them in proxies instead.
            if (IsModuleInterface(resultType))
            {
                return InvokeGeneric(nameof(ExecInterfaceAsync), resultType, _dag, queryBuilder);
            }

            if (resultType.IsArray && IsModuleInterface(resultType.GetElementType()!))
            {
                return InvokeGeneric(
                    nameof(ExecInterfaceArrayAsync),
                    resultType.GetElementType()!,
                    _dag,
                    queryBuilder
                );
            }

            if (
                resultType.IsGenericType
                && resultType.GetGenericTypeDefinition() == typeof(List<>)
                && IsModuleInterface(resultType.GetGenericArguments()[0])
            )
            {
                return InvokeGeneric(
                    nameof(ExecInterfaceListAsync),
                    resultType.GetGenericArguments()[0],
                    _dag,
                    queryBuilder
                );
            }

            var executeMethod = typeof(QueryExecutor)
                .GetMethod(nameof(QueryExecutor.ExecuteAsync))!
                .MakeGenericMethod(resultType);

            var task = executeMethod.Invoke(
                null,
                new object[] { _dag.GraphQLClient, queryBuilder, default(CancellationToken) }
            );
            return task;
        }
        // Chainable interface returns (self- or cross-interface) become
        // query-chained proxies; interfaces cannot be instantiated.
        else if (
            returnType.IsInterface
            && returnType.GetCustomAttribute<InterfaceAttribute>() is not null
        )
        {
            var name = Entrypoint.ResolveInterfaceGraphQLName(returnType);
            var create = typeof(DaggerInterfaceProxy<>)
                .MakeGenericType(returnType)
                .GetMethod(nameof(CreateFromQuery), BindingFlags.Public | BindingFlags.Static)!;
            return create.Invoke(null, new object[] { _dag, name, queryBuilder });
        }
        // Handle object types (chainable)
        else if (!returnType.IsPrimitive && returnType != typeof(string))
        {
            // Create new instance of the return type with updated query builder
            var instance = Activator.CreateInstance(returnType, queryBuilder, _dag.GraphQLClient);
            return instance;
        }
        else
        {
            throw new NotSupportedException(
                $"Return type {returnType.Name} not yet supported in interface proxy"
            );
        }
    }

    private static bool IsModuleInterface(Type type) =>
        type.IsInterface && type.GetCustomAttribute<InterfaceAttribute>() is not null;

    private static object? InvokeGeneric(string method, Type arg, params object[] args)
    {
        return typeof(DaggerInterfaceProxy<T>)
            .GetMethod(method, BindingFlags.NonPublic | BindingFlags.Static)!
            .MakeGenericMethod(arg)
            .Invoke(null, args);
    }

    private static async Task<TIface> ExecInterfaceAsync<TIface>(
        Query dag,
        QueryBuilder queryBuilder
    )
        where TIface : class
    {
        var id = await QueryExecutor.ExecuteAsync<Id>(
            dag.GraphQLClient,
            queryBuilder.Select("id"),
            default
        );
        var name = Entrypoint.ResolveInterfaceGraphQLName(typeof(TIface));
        return DaggerInterfaceProxy<TIface>.Create(dag, name, id.Value);
    }

    private static async Task<TIface[]> ExecInterfaceArrayAsync<TIface>(
        Query dag,
        QueryBuilder queryBuilder
    )
        where TIface : class
    {
        var ids = await QueryExecutor.ExecuteListAsync<Id>(
            dag.GraphQLClient,
            queryBuilder.Select("id"),
            default
        );
        var name = Entrypoint.ResolveInterfaceGraphQLName(typeof(TIface));
        return ids.Select(id => DaggerInterfaceProxy<TIface>.Create(dag, name, id.Value)).ToArray();
    }

    private static async Task<List<TIface>> ExecInterfaceListAsync<TIface>(
        Query dag,
        QueryBuilder queryBuilder
    )
        where TIface : class
    {
        var arr = await ExecInterfaceArrayAsync<TIface>(dag, queryBuilder);
        return arr.ToList();
    }

    private static Value ConvertToGraphQLValue(object argValue)
    {
        return argValue switch
        {
            string s => new StringValue(s),
            int n => new IntValue(n),
            long l => new IntValue((int)l),
            short s => new IntValue(s),
            byte b => new IntValue(b),
            float f => new FloatValue(f),
            double d => new FloatValue((float)d),
            decimal dec => new FloatValue((float)dec),
            bool b => new BooleanValue(b),
            // Interface proxies pass their wrapped id directly.
            IDaggerInterfaceProxy proxy => new StringValue(proxy.ProxyId),
            // Modern ids are the generic Id scalar (IId<T> is invariant, so
            // IId<Id> values do not match an IId<Scalar> pattern).
            IId<Id> id => new IdValue<Id>(id),
            IId<Scalar> id => new IdValue<Scalar>(id),
            // Handle List<T> and arrays
            System.Collections.IEnumerable enumerable => new ListValue(
                enumerable.Cast<object>().Select(ConvertToGraphQLValue).ToList()
            ),
            _ => throw new NotSupportedException(
                $"Argument type {argValue.GetType().Name} not yet supported in interface proxy"
            ),
        };
    }
}
