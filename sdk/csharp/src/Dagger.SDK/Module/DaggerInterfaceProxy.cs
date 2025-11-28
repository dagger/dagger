using System;
using System.Collections.Immutable;
using System.Reflection;
using System.Threading;
using System.Threading.Tasks;
using Dagger.GraphQL;

namespace Dagger.Runtime;

/// <summary>
/// DispatchProxy-based implementation of interfaces that routes method calls to the GraphQL API.
/// </summary>
internal class DaggerInterfaceProxy<T> : DispatchProxy where T : class
{
    private Query? _dag;
    private string? _interfaceName;
    private string? _id;

    public static T Create(Query dag, string interfaceName, string id)
    {
        var proxy = Create<T, DaggerInterfaceProxy<T>>() as DaggerInterfaceProxy<T>;
        if (proxy == null)
        {
            throw new InvalidOperationException($"Failed to create proxy for interface {typeof(T).Name}");
        }
        
        proxy._dag = dag;
        proxy._interfaceName = interfaceName;
        proxy._id = id;
        
        return proxy as T ?? throw new InvalidOperationException("Proxy cast failed");
    }

    protected override object? Invoke(MethodInfo? targetMethod, object?[]? args)
    {
        if (targetMethod == null || _dag == null || _interfaceName == null || _id == null)
        {
            throw new InvalidOperationException("Proxy not properly initialized");
        }

        // Special case: Id() or id() method - return the interface ID directly
        if (targetMethod.Name.Equals("Id", StringComparison.OrdinalIgnoreCase) &&
            targetMethod.ReturnType == typeof(Task<string>))
        {
            return Task.FromResult(_id);
        }

        // Build GraphQL query using QueryBuilder, like generated wrapper classes do.
        // Example: query { loadInterfaceExampleProcessorFromID(id: "...") { methodName(args...) } }
        
        // _interfaceName is already the correct GraphQL type name (e.g., "InterfaceExampleProcessor")
        var loadMethodName = $"load{_interfaceName}FromID";
        
        // Start with load method and ID argument
        var arguments = ImmutableList<Argument>.Empty;
        arguments = arguments.Add(new Argument("id", new StringValue(_id)));
        var queryBuilder = _dag.QueryBuilder.Select(loadMethodName, arguments);
        
        // Add the interface method call
        var methodName = char.ToLowerInvariant(targetMethod.Name[0]) + targetMethod.Name.Substring(1);
        var methodArgs = ImmutableList<Argument>.Empty;
        
        var parameters = targetMethod.GetParameters();
        args ??= Array.Empty<object>();
        
        for (int i = 0; i < parameters.Length && i < args.Length; i++)
        {
            var paramName = parameters[i].Name ?? $"arg{i}";
            var argValue = args[i];
            
            if (argValue == null)
            {
                continue;
            }
            
            // Convert argument to GraphQL value
            Value graphqlValue = argValue switch
            {
                string s => new StringValue(s),
                int n => new IntValue(n),
                bool b => new BooleanValue(b),
                IId<Scalar> id => new IdValue<Scalar>(id),
                _ => throw new NotSupportedException($"Argument type {argValue.GetType().Name} not yet supported in interface proxy")
            };
            
            methodArgs = methodArgs.Add(new Argument(paramName, graphqlValue));
        }
        
        queryBuilder = queryBuilder.Select(methodName, methodArgs);
        
        // Execute the query and return result
        var returnType = targetMethod.ReturnType;
        
        // Handle async methods
        if (returnType.IsGenericType && returnType.GetGenericTypeDefinition() == typeof(Task<>))
        {
            var resultType = returnType.GetGenericArguments()[0];
            var executeMethod = typeof(QueryExecutor).GetMethod(nameof(QueryExecutor.ExecuteAsync))!
                .MakeGenericMethod(resultType);
            
            var task = executeMethod.Invoke(null, new object[] { _dag.GraphQLClient, queryBuilder, default(CancellationToken) });
            return task;
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
            throw new NotSupportedException($"Return type {returnType.Name} not yet supported in interface proxy");
        }
    }
}
