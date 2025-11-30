using System.Collections;
using System.Reflection;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Xml.Linq;
using System.IO;
using Dagger.Telemetry;
using DaggerObject = Dagger.Object;

namespace Dagger.ModuleRuntime;

/// <summary>
/// Entry point for Dagger C# modules. Handles module discovery, registration, and invocation.
/// </summary>
public static class Entrypoint
{
    private static readonly JsonSerializerOptions SerializerOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
        Converters = { new JsonStringEnumConverter(JsonNamingPolicy.CamelCase) },
    };

    // Registry to track interface types and their registered names
    // This ensures we reference the same TypeDef when an interface is used in multiple places
    // Note: Repopulated at invocation start since static state doesn't persist across containers
    private static readonly Dictionary<Type, string> InterfaceTypeRegistry = new();
    
    // Store the current module's name for constructing namespaced interface names
    // This is set during registration and re-set during invocation
    private static string? _currentModuleName;
    
    // Cache reflection lookups to avoid repeated searches
    private static readonly Dictionary<Type, MethodInfo> LoadMethodCache = new();
    private static readonly Dictionary<Type, MethodInfo> IdAsyncMethodCache = new();
    private static readonly NullabilityInfoContext NullabilityContext = new();
    
    // Cache for camelCase conversions (small strings only)
    private static readonly Dictionary<string, string> CamelCaseCache = new(StringComparer.Ordinal);

    // Debug logging
    private static bool _debugEnabled = false;

    /// <summary>
    /// Configure interface resolution mode for the Dagger module runtime.
    /// </summary>
    /// <summary>
    /// Configure debug logging for the Dagger module runtime.
    /// When enabled, the runtime will output detailed debug information to stderr and /tmp/dagger-csharp-debug.log.
    /// </summary>
    /// <param name="enabled">Whether to enable debug logging</param>
    /// <remarks>
    /// To enable debugging:
    ///   1. Add <c>Entrypoint.ConfigureLogging(true)</c> in your Program.cs before RunAsync
    ///   2. Run dagger with: <c>dagger call --progress=plain &lt;function&gt;</c>
    ///   3. View logs in terminal stderr or at /tmp/dagger-csharp-debug.log
    /// 
    /// Debug logs include:
    ///   - Module and interface registration details
    ///   - Function invocation parameters
    ///   - Type resolution and interface proxy creation
    ///   - GraphQL type mapping
    /// </remarks>
    public static void ConfigureLogging(bool enabled)
    {
        _debugEnabled = enabled;
        if (enabled)
        {
            Console.Error.WriteLine("[DEBUG] Dagger.Module: Debug logging ENABLED");
        }
    }

    private static void DebugLog(string message)
    {
        if (_debugEnabled)
        {
            // Write to both stderr and a file since container stderr isn't always visible
            Console.Error.WriteLine($"[DEBUG] Dagger.Module: {message}");
            try
            {
                System.IO.File.AppendAllText("/tmp/dagger-csharp-debug.log", 
                    $"[{DateTime.UtcNow:O}] {message}\n");
            }
            catch
            {
                // Ignore file write errors
            }
        }
    }

    private static bool _xmlDocumentationLoaded;
    private static XDocument? _xmlDocumentation;

    /// <summary>
    /// Entry point for the Dagger module runtime.
    /// </summary>
    /// <param name="args">
    ///     Command-line arguments (not used).
    /// </param>
    /// <returns>
    ///     0 on success, non-zero on failure.
    /// </returns>
    public static async Task<int> RunAsync(string[] args)
    {
        // Initialize trace context propagation for distributed tracing
        TracePropagation.Initialize();

        Query dag;
        try
        {
            dag = Dagger.Client.Dag;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine($"Failed to initialise Dagger client: {ex.Message}");
            return 1;
        }

        FunctionCall fnCall;
        try
        {
            fnCall = dag.CurrentFunctionCall();
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine($"Failed to resolve current function call: {ex.Message}");
            return 1;
        }

        var (moduleInfos, interfaceInfos) = BuildModuleTypeInfos();
        if (moduleInfos.Count == 0)
        {
            await ReturnErrorAsync(
                dag,
                fnCall,
                "No types decorated with [Object] were discovered in the entry assembly."
            );
            return 1;
        }

        string parentName;
        try
        {
            parentName = await fnCall.ParentNameAsync();
        }
        catch (Exception ex)
        {
            await ReturnErrorAsync(dag, fnCall, ex);
            return 1;
        }

        if (string.IsNullOrEmpty(parentName))
        {
            return await HandleRegistrationAsync(dag, fnCall, moduleInfos, interfaceInfos);
        }

        return await HandleInvocationAsync(dag, fnCall, parentName, moduleInfos);
    }

    private static async Task<int> HandleRegistrationAsync(
        Query dag,
        FunctionCall fnCall,
        IReadOnlyCollection<ModuleTypeInfo> moduleInfos,
        IReadOnlyCollection<InterfaceTypeInfo> interfaceInfos
    )
    {
        DebugLog("========== STARTING REGISTRATION ==========");
        DebugLog($"Found {moduleInfos.Count} module objects, {interfaceInfos.Count} interfaces");
        
        try
        {
            var module = dag.Module();
            DebugLog("Created Module instance");

            // Register enums first
            var enums = BuildEnumTypeInfos();
            DebugLog($"Found {enums.Count} enums");
            foreach (var enumInfo in enums)
            {
                var enumDef = dag.TypeDef().WithEnum(enumInfo.Name, enumInfo.Description);

                foreach (var valueInfo in enumInfo.Values)
                {
                    enumDef = enumDef.WithEnumMember(
                        name: valueInfo.Name,
                        value: valueInfo.Value,
                        description: valueInfo.Description,
                        deprecated: valueInfo.Deprecated
                    );
                }

                module = module.WithEnum(enumDef);
            }

            // Pre-populate the interface registry before processing any interfaces or objects
            // This allows interface functions that reference their own type (e.g., CustomIface WithStr(string))
            // to find the interface name during BuildTypeDef calls
            DebugLog($"Pre-populating interface registry with {interfaceInfos.Count} interfaces");
            foreach (var interfaceInfo in interfaceInfos)
            {
                InterfaceTypeRegistry[interfaceInfo.ClrType] = interfaceInfo.Name;
                DebugLog($"  Registry[{interfaceInfo.ClrType.FullName}] = '{interfaceInfo.Name}'");
            }
            
            // Store module name - use the first module object's name as the module identifier
            // This matches Python's approach where the module has a main_cls
            if (moduleInfos.Count > 0)
            {
                _currentModuleName = moduleInfos.First().Name;
                DebugLog($"Current module name: '{_currentModuleName}'");
            }

            // Register interfaces before objects so object functions can reference them
            foreach (var interfaceInfo in interfaceInfos)
            {
                DebugLog($"Registering interface: Name='{interfaceInfo.Name}', ClrType={interfaceInfo.ClrType.Name}");

                var typeDef = dag.TypeDef().WithInterface(interfaceInfo.Name, interfaceInfo.Description);
                DebugLog($"Called WithInterface('{interfaceInfo.Name}') on TypeDef");

                // Register interface functions
                foreach (var function in interfaceInfo.Functions)
                {
                    DebugLog($"  Processing interface function: {function.Name}, ReturnType={function.ReturnType.Name}");
                    
                    var (returnTypeDef, returnIsNullable) = BuildTypeDef(dag, function.ReturnType);
                    if (function.ReturnsVoid || returnIsNullable)
                    {
                        returnTypeDef = returnTypeDef.WithOptional(true);
                    }

                    var functionDef = dag.Function(function.Name, returnTypeDef);

                    if (!string.IsNullOrWhiteSpace(function.Description))
                    {
                        functionDef = functionDef.WithDescription(function.Description);
                    }

                    if (!string.IsNullOrWhiteSpace(function.Deprecated))
                    {
                        functionDef = functionDef.WithDeprecated(function.Deprecated);
                    }

                    // Add function parameters
                    foreach (var param in function.Parameters)
                    {
                        if (param.IsCancellationToken)
                        {
                            continue;
                        }

                        var (paramTypeDef, paramIsNullable) = BuildTypeDef(
                            dag,
                            param.ParameterType,
                            parameterInfo: param.Parameter
                        );

                        if (param.IsOptional || paramIsNullable)
                        {
                            paramTypeDef = paramTypeDef.WithOptional(true);
                        }

                        Json? defaultJson = null;
                        if (param.Parameter.HasDefaultValue)
                        {
                            var normalized = NormalizeDefaultValue(param.Parameter.DefaultValue);
                            if (normalized != null)
                            {
                                defaultJson = new Json
                                {
                                    Value = JsonSerializer.Serialize(normalized, SerializerOptions),
                                };
                            }
                        }

                        functionDef = functionDef.WithArg(
                            param.Name,
                            paramTypeDef,
                            param.Description,
                            defaultJson,
                            param.DefaultPath,
                            param.Ignore?.ToArray()
                        );
                    }

                    typeDef = typeDef.WithFunction(functionDef);
                }

                module = module.WithInterface(typeDef);
                DebugLog($"Added interface '{interfaceInfo.Name}' to module");
            }

            // Register objects after interfaces
            foreach (var moduleInfo in moduleInfos)
            {
                var typeDef = dag.TypeDef().WithObject(moduleInfo.Name, moduleInfo.Description);

                // TODO: TypeDef.WithDeprecated not available in current Dagger version
                // Register deprecated status on object (when API becomes available)
                // if (!string.IsNullOrWhiteSpace(moduleInfo.Deprecated))
                // {
                //     typeDef = typeDef.WithDeprecated(moduleInfo.Deprecated);
                // }

                // Register constructor if present
                if (moduleInfo.Constructor != null)
                {
                    var ctorFunc = dag.Function("", typeDef); // Empty name for constructor

                    foreach (var param in moduleInfo.Constructor.GetParameters())
                    {
                        var (paramTypeDef, paramNullable) = BuildTypeDef(
                            dag,
                            param.ParameterType,
                            parameterInfo: param
                        );

                        DebugLog(
                            $"Constructor parameter '{param.Name}': nullable={paramNullable}, hasDefault={param.HasDefaultValue}, isOptional={param.IsOptional}"
                        );

                        if (paramNullable || param.HasDefaultValue || param.IsOptional)
                        {
                            paramTypeDef = paramTypeDef.WithOptional(true);
                        }

                        Json? defaultJson = null;
                        if (param.HasDefaultValue)
                        {
                            var normalized = NormalizeDefaultValue(param.DefaultValue);
                            if (normalized != null)
                            {
                                defaultJson = new Json
                                {
                                    Value = JsonSerializer.Serialize(normalized, SerializerOptions),
                                };
                            }
                        }

                        // Extract DefaultPath and Ignore attributes
                        var defaultPathAttr = param.GetCustomAttribute<DefaultPathAttribute>();
                        var ignoreAttr = param.GetCustomAttribute<IgnoreAttribute>();

                        ctorFunc = ctorFunc.WithArg(
                            ToCamelCase(param.Name ?? $"arg{param.Position}"),
                            paramTypeDef,
                            null, // description
                            defaultJson,
                            defaultPathAttr?.Path,
                            ignoreAttr?.Patterns?.ToArray()
                        );
                    }

                    typeDef = typeDef.WithConstructor(ctorFunc);
                }

                // Register fields
                foreach (var field in moduleInfo.Fields)
                {
                    var (fieldTypeDef, fieldIsNullable) = BuildTypeDef(
                        dag,
                        field.PropertyType,
                        propertyInfo: field.PropertyInfo
                    );
                    if (fieldIsNullable)
                    {
                        fieldTypeDef = fieldTypeDef.WithOptional(true);
                    }

                    typeDef = typeDef.WithField(
                        name: field.Name,
                        typeDef: fieldTypeDef,
                        description: field.Description,
                        deprecated: field.Deprecated
                    );
                }

                // Register functions
                foreach (var function in moduleInfo.Functions)
                {
                    var (returnTypeDef, returnIsNullable) = BuildTypeDef(dag, function.ReturnType);
                    if (function.ReturnsVoid || returnIsNullable)
                    {
                        returnTypeDef = returnTypeDef.WithOptional(true);
                    }

                    var functionDef = dag.Function(function.Name, returnTypeDef);

                    if (!string.IsNullOrWhiteSpace(function.Description))
                    {
                        functionDef = functionDef.WithDescription(function.Description);
                    }

                    // Register cache policy
                    if (!string.IsNullOrWhiteSpace(function.CachePolicy))
                    {
                        switch (function.CachePolicy.ToLowerInvariant())
                        {
                            case "never":
                                functionDef = functionDef.WithCachePolicy(
                                    FunctionCachePolicy.Never
                                );
                                break;
                            case "session":
                                functionDef = functionDef.WithCachePolicy(
                                    FunctionCachePolicy.PerSession
                                );
                                break;
                            default:
                                // Duration string like "5m", "1h"
                                functionDef = functionDef.WithCachePolicy(
                                    policy: FunctionCachePolicy.Default,
                                    timeToLive: function.CachePolicy
                                );
                                break;
                        }
                    }

                    // Register deprecation
                    if (!string.IsNullOrWhiteSpace(function.Deprecated))
                    {
                        functionDef = functionDef.WithDeprecated(function.Deprecated);
                    }

                    foreach (var parameter in function.Parameters)
                    {
                        if (parameter.IsCancellationToken)
                        {
                            continue;
                        }

                        var (argumentTypeDef, argumentNullable) = BuildTypeDef(
                            dag,
                            parameter.ParameterType,
                            parameterInfo: parameter.Parameter
                        );

                        DebugLog(
                            $"Function '{function.Name}' parameter '{parameter.Name}': nullable={argumentNullable}, isOptional={parameter.IsOptional}, hasDefault={parameter.Parameter.HasDefaultValue}"
                        );

                        if (argumentNullable || parameter.IsOptional)
                        {
                            argumentTypeDef = argumentTypeDef.WithOptional(true);
                        }

                        Json? defaultJson = null;
                        if (parameter.Parameter.HasDefaultValue)
                        {
                            var normalizedDefault = NormalizeDefaultValue(
                                parameter.Parameter.DefaultValue
                            );
                            if (normalizedDefault is not null)
                            {
                                defaultJson = new Json
                                {
                                    Value = JsonSerializer.Serialize(
                                        normalizedDefault,
                                        SerializerOptions
                                    ),
                                };
                            }
                        }

                        functionDef = functionDef.WithArg(
                            parameter.Name,
                            argumentTypeDef,
                            parameter.Description,
                            defaultJson,
                            parameter.DefaultPath,
                            parameter.Ignore?.ToArray()
                        );
                    }

                    typeDef = typeDef.WithFunction(functionDef);
                }

                module = module.WithObject(typeDef);
            }

            var moduleId = await module.IdAsync().ConfigureAwait(false);
            DebugLog($"Got module ID: {moduleId.Value}");
            DebugLog($"========== REGISTRATION SUMMARY ==========");
            DebugLog($"Registered {interfaceInfos.Count} interfaces:");
            foreach (var iface in interfaceInfos)
            {
                DebugLog($"  - Interface '{iface.Name}' ({iface.ClrType.FullName})");
            }
            DebugLog($"Registered {moduleInfos.Count} objects");
            DebugLog($"Interface registry contains {InterfaceTypeRegistry.Count} entries:");
            foreach (var kvp in InterfaceTypeRegistry)
            {
                DebugLog($"  - {kvp.Key.FullName} => '{kvp.Value}'");
            }
            DebugLog($"==========================================");
            
            var result = new Json
            {
                Value = JsonSerializer.Serialize(moduleId.Value, SerializerOptions),
            };

            await fnCall.ReturnValueAsync(result).ConfigureAwait(false);
            DebugLog("========== REGISTRATION COMPLETE ==========");
            return 0;
        }
        catch (Exception ex)
        {
            await ReturnErrorAsync(dag, fnCall, ex);
            return 1;
        }
    }

    /// <summary>
    /// Ensures the interface registry is populated from discovered types.
    /// Called at invocation start to restore interface metadata that doesn't persist across containers.
    /// </summary>
    private static void EnsureInterfaceRegistryPopulated(
        IReadOnlyCollection<ModuleTypeInfo> moduleInfos,
        IReadOnlyCollection<InterfaceTypeInfo> interfaceInfos
    )
    {
        // Only populate if registry is empty (avoid duplicate work)
        if (InterfaceTypeRegistry.Count > 0)
        {
            DebugLog($"Interface registry already populated with {InterfaceTypeRegistry.Count} entries");
            return;
        }

        DebugLog($"Populating interface registry with {interfaceInfos.Count} interfaces");
        foreach (var interfaceInfo in interfaceInfos)
        {
            InterfaceTypeRegistry[interfaceInfo.ClrType] = interfaceInfo.Name;
            DebugLog($"  Registry[{interfaceInfo.ClrType.FullName}] = '{interfaceInfo.Name}'");
        }

        // Store module name - use the first module object's name as the module identifier
        if (moduleInfos.Count > 0 && string.IsNullOrEmpty(_currentModuleName))
        {
            _currentModuleName = moduleInfos.First().Name;
            DebugLog($"Set current module name: '{_currentModuleName}'");
        }
    }

    private static async Task<int> HandleInvocationAsync(
        Query dag,
        FunctionCall fnCall,
        string parentName,
        IReadOnlyCollection<ModuleTypeInfo> moduleInfos
    )
    {
        var functionName = await fnCall.NameAsync();

        // Re-populate interface registry from discovered types
        // This is necessary because each invocation may run in a new container/process
        // where static state doesn't persist from registration time
        var (_, interfaceInfos) = BuildModuleTypeInfos();
        EnsureInterfaceRegistryPopulated(moduleInfos, interfaceInfos);

        try
        {
            var moduleInfo = moduleInfos.FirstOrDefault(info =>
                string.Equals(info.Name, parentName, StringComparison.Ordinal)
            );
            if (moduleInfo is null)
            {
                await ReturnErrorAsync(
                    dag,
                    fnCall,
                    $"Module object '{parentName}' is not registered."
                );
                return 1;
            }

            // Handle constructor invocation (empty function name)
            if (string.IsNullOrEmpty(functionName))
            {
                return await HandleConstructorInvocationAsync(dag, fnCall, moduleInfo);
            }

            var functionInfo = moduleInfo.Functions.FirstOrDefault(f =>
                string.Equals(f.Name, functionName, StringComparison.Ordinal)
            );
            if (functionInfo is null)
            {
                await ReturnErrorAsync(
                    dag,
                    fnCall,
                    $"Function '{functionName}' not found on module object '{parentName}'."
                );
                return 1;
            }

            // Create instance with constructor if needed
            object? instance;
            if (moduleInfo.Constructor != null && moduleInfo.Constructor.GetParameters().Length > 0)
            {
                var parentJson = await fnCall.ParentAsync();
                using var parentDoc = JsonDocument.Parse(parentJson.Value);
                var parentElement = parentDoc.RootElement;

                var ctorParams = moduleInfo.Constructor.GetParameters();
                var ctorArgs = new object?[ctorParams.Length];

                for (var i = 0; i < ctorParams.Length; i++)
                {
                    var param = ctorParams[i];
                    var paramName = ToCamelCase(param.Name ?? $"arg{i}");

                    if (parentElement.TryGetProperty(paramName, out var propElement))
                    {
                        ctorArgs[i] = await ConvertArgumentAsync(
                            propElement,
                            param.ParameterType,
                            dag
                        );
                    }
                    else if (param.HasDefaultValue)
                    {
                        ctorArgs[i] = param.DefaultValue;
                    }
                    else if (param.IsOptional)
                    {
                        ctorArgs[i] = null;
                    }
                    else
                    {
                        throw new InvalidOperationException(
                            $"Missing required constructor argument '{paramName}'."
                        );
                    }
                }

                instance = moduleInfo.Constructor.Invoke(ctorArgs);
            }
            else
            {
                instance = Activator.CreateInstance(moduleInfo.ClrType);
            }

            if (instance == null)
            {
                throw new InvalidOperationException(
                    $"Unable to create instance of '{moduleInfo.ClrType.FullName}'."
                );
            }

            // Populate [Field] properties from parent JSON
            // Skip fields that were already set by constructor parameters
            if (moduleInfo.Fields.Count > 0)
            {
                var parentJson = await fnCall.ParentAsync();
                using var parentDoc = JsonDocument.Parse(parentJson.Value);
                var parentElement = parentDoc.RootElement;

                // Get constructor parameter names to skip
                var ctorParamNames = new HashSet<string>(StringComparer.Ordinal);
                if (moduleInfo.Constructor != null)
                {
                    foreach (var param in moduleInfo.Constructor.GetParameters())
                    {
                        ctorParamNames.Add(ToCamelCase(param.Name ?? ""));
                    }
                }

                foreach (var fieldInfo in moduleInfo.Fields)
                {
                    // Skip if this field was already initialized by constructor
                    if (ctorParamNames.Contains(fieldInfo.Name))
                    {
                        continue;
                    }

                    if (parentElement.TryGetProperty(fieldInfo.Name, out var fieldElement))
                    {
                        var fieldValue = await ConvertArgumentAsync(
                            fieldElement,
                            fieldInfo.PropertyType,
                            dag,
                            null
                        );
                        fieldInfo.PropertyInfo.SetValue(instance, fieldValue);
                    }
                }
            }

            var argumentValues = await LoadArgumentsAsync(dag, fnCall, functionInfo);

            object? invocationResult;
            try
            {
                invocationResult = functionInfo.Method.Invoke(instance, argumentValues);
            }
            catch (TargetInvocationException tie) when (tie.InnerException is not null)
            {
                throw tie.InnerException;
            }

            if (functionInfo.ReturnsTask)
            {
                if (functionInfo.Method.ReturnType.IsGenericType)
                {
                    dynamic awaitedTask = invocationResult!;
                    invocationResult = await awaitedTask;
                }
                else
                {
                    await (Task)invocationResult!;
                    invocationResult = null;
                }
            }
            else if (functionInfo.ReturnsValueTask)
            {
                if (functionInfo.Method.ReturnType.IsGenericType)
                {
                    dynamic awaitedValueTask = invocationResult!;
                    invocationResult = await awaitedValueTask;
                }
                else
                {
                    await (ValueTask)invocationResult!;
                    invocationResult = null;
                }
            }

            var normalizedResult = await NormalizeResultAsync(invocationResult);
            var jsonResult = new Json
            {
                Value = JsonSerializer.Serialize(normalizedResult, SerializerOptions),
            };

            await fnCall.ReturnValueAsync(jsonResult);

            // Record metrics
            return 0;
        }
        catch (Exception ex)
        {
            await ReturnErrorAsync(dag, fnCall, ex);
            return 1;
        }
    }

    private static async Task<int> HandleConstructorInvocationAsync(
        Query dag,
        FunctionCall fnCall,
        ModuleTypeInfo moduleInfo
    )
    {
        try
        {
            // Create instance with constructor arguments
            object? instance;
            if (moduleInfo.Constructor != null && moduleInfo.Constructor.GetParameters().Length > 0)
            {
                var inputArgs = await fnCall.InputArgsAsync();
                var argumentMap = new Dictionary<string, JsonElement>(StringComparer.Ordinal);

                foreach (var arg in inputArgs)
                {
                    var name = await arg.NameAsync();
                    var value = await arg.ValueAsync();
                    using var document = JsonDocument.Parse(value.Value);
                    argumentMap[name] = document.RootElement.Clone();
                }

                var ctorParams = moduleInfo.Constructor.GetParameters();
                var ctorArgs = new object?[ctorParams.Length];

                for (var i = 0; i < ctorParams.Length; i++)
                {
                    var param = ctorParams[i];
                    var paramName = ToCamelCase(param.Name ?? $"arg{i}");

                    if (argumentMap.TryGetValue(paramName, out var argElement))
                    {
                        ctorArgs[i] = await ConvertArgumentAsync(
                            argElement,
                            param.ParameterType,
                            dag
                        );
                    }
                    else if (param.HasDefaultValue)
                    {
                        ctorArgs[i] = param.DefaultValue;
                    }
                    else if (param.IsOptional)
                    {
                        ctorArgs[i] = null;
                    }
                    else
                    {
                        throw new InvalidOperationException(
                            $"Missing required constructor argument '{paramName}'."
                        );
                    }
                }

                instance = moduleInfo.Constructor.Invoke(ctorArgs);
            }
            else
            {
                instance = Activator.CreateInstance(moduleInfo.ClrType);
            }

            if (instance == null)
            {
                throw new InvalidOperationException(
                    $"Unable to create instance of '{moduleInfo.ClrType.FullName}'."
                );
            }

            // Serialize instance to JSON and return
            var jsonResult = new Json
            {
                Value = JsonSerializer.Serialize(instance, SerializerOptions),
            };
            await fnCall.ReturnValueAsync(jsonResult);
            return 0;
        }
        catch (Exception ex)
        {
            await ReturnErrorAsync(dag, fnCall, ex);
            return 1;
        }
    }

    private static async Task<object?[]> LoadArgumentsAsync(
        Query dag,
        FunctionCall fnCall,
        FunctionInfo functionInfo
    )
    {
        var providedArgs = await fnCall.InputArgsAsync();
        var argumentMap = new Dictionary<string, JsonElement>(StringComparer.Ordinal);

        foreach (var arg in providedArgs)
        {
            var name = await arg.NameAsync();
            var value = await arg.ValueAsync();
            using var document = JsonDocument.Parse(value.Value);
            argumentMap[name] = document.RootElement.Clone();
        }

        var result = new object?[functionInfo.Parameters.Count];

        for (var i = 0; i < functionInfo.Parameters.Count; i++)
        {
            var parameter = functionInfo.Parameters[i];

            if (parameter.IsCancellationToken)
            {
                result[i] = CancellationToken.None;
                continue;
            }

            if (!argumentMap.TryGetValue(parameter.Name, out var element))
            {
                if (parameter.Parameter.HasDefaultValue)
                {
                    result[i] = parameter.Parameter.DefaultValue;
                }
                else if (parameter.IsOptional)
                {
                    result[i] = null;
                }
                else
                {
                    throw new InvalidOperationException(
                        $"Missing required argument '{parameter.Name}'."
                    );
                }

                continue;
            }

            // Pass the GraphQL typename for interface parameters
            result[i] = await ConvertArgumentAsync(element, parameter.ParameterType, dag, parameter.GraphQLTypeName);
        }

        return result;
    }

    private static (IReadOnlyList<ModuleTypeInfo> objects, IReadOnlyList<InterfaceTypeInfo> interfaces) BuildModuleTypeInfos()
    {
        var types = DiscoverModuleTypes();
        DebugLog($"DiscoverModuleTypes() returned {types.Count} types total");
        
        var moduleTypes = new List<ModuleTypeInfo>();
        var interfaceTypes = new List<InterfaceTypeInfo>();

        foreach (var type in types)
        {
            DebugLog($"Examining type: {type.FullName}, IsInterface={type.IsInterface}");
            
            var daggerAttr = type.GetCustomAttribute<ObjectAttribute>();
            if (daggerAttr is not null)
            {
                // Process object types (existing logic)

            var moduleInfo = new ModuleTypeInfo
            {
                Name = daggerAttr.Name ?? type.Name,
                Description = daggerAttr.Description ?? GetTypeDescription(type),
                Deprecated = daggerAttr.Deprecated,
                ClrType = type,
                Constructor = GetModuleConstructor(type),
            };

            foreach (var method in type.GetMethods(BindingFlags.Instance | BindingFlags.Public))
            {
                if (method.IsSpecialName)
                {
                    continue;
                }

                var functionAttr = method.GetCustomAttribute<FunctionAttribute>();
                if (functionAttr is null)
                {
                    continue;
                }

                var functionName = functionAttr.Name ?? method.Name;
                var returnType = UnwrapReturnType(
                    method.ReturnType,
                    out var returnsTask,
                    out var returnsValueTask,
                    out var returnsVoid
                );

                var functionInfo = new FunctionInfo
                {
                    Name = ToCamelCase(functionName),
                    Description = functionAttr.Description ?? GetMethodDescription(method),
                    Deprecated = functionAttr.Deprecated,
                    CachePolicy = functionAttr.Cache,
                    Method = method,
                    ReturnType = returnType,
                    ReturnsTask = returnsTask,
                    ReturnsValueTask = returnsValueTask,
                    ReturnsVoid = returnsVoid,
                };

                foreach (var parameter in method.GetParameters())
                {
                    var parameterName = ToCamelCase(
                        parameter.Name ?? $"arg{functionInfo.Parameters.Count}"
                    );

                    // Extract DefaultPath and Ignore attributes
                    var defaultPathAttr = parameter.GetCustomAttribute<DefaultPathAttribute>();
                    var ignoreAttr = parameter.GetCustomAttribute<IgnoreAttribute>();

                    // Check if parameter is nullable (for optional parameter handling)
                    var (_, isNullable) = UnwrapNullableType(parameter.ParameterType, parameter);

                    // Get the GraphQL typename for interface parameters
                    string? graphQLTypeName = null;
                    if (parameter.ParameterType.IsInterface)
                    {
                        if (InterfaceTypeRegistry.TryGetValue(parameter.ParameterType, out var ifaceName))
                        {
                            graphQLTypeName = ifaceName; // Local interface name
                        }
                    }

                    var parameterMetadata = new ParameterMetadata
                    {
                        Name = parameterName,
                        Description = null,
                        Parameter = parameter,
                        ParameterType = parameter.ParameterType,
                        IsOptional =
                            parameter.HasDefaultValue
                            || parameter.IsOptional
                            || isNullable,
                        IsCancellationToken = parameter.ParameterType == typeof(CancellationToken),
                        DefaultPath = defaultPathAttr?.Path,
                        Ignore = ignoreAttr?.Patterns?.ToList(),
                        GraphQLTypeName = graphQLTypeName,
                    };

                    functionInfo.Parameters.Add(parameterMetadata);
                }

                moduleInfo.Functions.Add(functionInfo);
            }

            // Discover fields (properties) marked with [Field]
            foreach (
                var property in type.GetProperties(BindingFlags.Instance | BindingFlags.Public)
            )
            {
                var fieldAttr = property.GetCustomAttribute<FieldAttribute>();
                if (fieldAttr is null)
                {
                    continue;
                }

                var fieldInfo = new DaggerFieldInfo
                {
                    Name = ToCamelCase(fieldAttr.Name ?? property.Name),
                    Description = fieldAttr.Description,
                    Deprecated = fieldAttr.Deprecated,
                    PropertyInfo = property,
                    PropertyType = property.PropertyType,
                };

                moduleInfo.Fields.Add(fieldInfo);
            }

                if (moduleInfo.Functions.Count > 0 || moduleInfo.Fields.Count > 0)
                {
                    moduleTypes.Add(moduleInfo);
                }
                continue;
            }

            // Process interface types
            var interfaceAttr = type.GetCustomAttribute<InterfaceAttribute>();
            DebugLog($"  [Interface] attribute present: {interfaceAttr is not null}, IsInterface: {type.IsInterface}");
            
            if (interfaceAttr is not null && type.IsInterface)
            {
                DebugLog($"  Processing interface: {type.Name}");
                
                // Strip 'I' prefix from interface name if not explicitly set (IProcessor -> Processor)
                // This matches the GraphQL schema naming convention for load{TypeName}FromID methods
                var interfaceName = interfaceAttr.Name ?? type.Name;
                if (interfaceAttr.Name == null && interfaceName.StartsWith("I") && interfaceName.Length > 1 && char.IsUpper(interfaceName[1]))
                {
                    interfaceName = interfaceName.Substring(1);
                }
                
                var interfaceInfo = new InterfaceTypeInfo
                {
                    Name = interfaceName,
                    Description = interfaceAttr.Description ?? GetTypeDescription(type),
                    ClrType = type,
                };

                // NOTE: Interfaces currently only support methods/functions, not properties/fields
                // This is a limitation in Dagger's GraphQL implementation (InterfaceTypeDef only has Functions, no Fields)
                // GraphQL spec supports fields on interfaces, but Dagger doesn't yet implement this
                // TODO: Add field/property support when Dagger engine is extended to support interface fields
                foreach (var method in type.GetMethods(BindingFlags.Instance | BindingFlags.Public))
                {
                    if (method.IsSpecialName)
                    {
                        continue;
                    }

                    var functionAttr = method.GetCustomAttribute<FunctionAttribute>();
                    if (functionAttr is null)
                    {
                        continue;
                    }

                    var functionName = functionAttr.Name ?? method.Name;
                    var returnType = UnwrapReturnType(
                        method.ReturnType,
                        out var returnsTask,
                        out var returnsValueTask,
                        out var returnsVoid
                    );

                    var functionInfo = new FunctionInfo
                    {
                        Name = ToCamelCase(functionName),
                        Description = functionAttr.Description ?? GetMethodDescription(method),
                        Deprecated = functionAttr.Deprecated,
                        CachePolicy = functionAttr.Cache,
                        Method = method,
                        ReturnType = returnType,
                        ReturnsTask = returnsTask,
                        ReturnsValueTask = returnsValueTask,
                        ReturnsVoid = returnsVoid,
                    };

                    foreach (var parameter in method.GetParameters())
                    {
                        var parameterName = ToCamelCase(
                            parameter.Name ?? $"arg{functionInfo.Parameters.Count}"
                        );

                        var defaultPathAttr = parameter.GetCustomAttribute<DefaultPathAttribute>();
                        var ignoreAttr = parameter.GetCustomAttribute<IgnoreAttribute>();

                        // Check if parameter is nullable (for optional parameter handling)
                        var (_, isNullable) = UnwrapNullableType(parameter.ParameterType, parameter);

                        // Get the GraphQL typename for interface parameters
                        string? graphQLTypeName = null;
                        if (parameter.ParameterType.IsInterface)
                        {
                            if (InterfaceTypeRegistry.TryGetValue(parameter.ParameterType, out var ifaceName))
                            {
                                graphQLTypeName = ifaceName; // Local interface name
                            }
                        }

                        var parameterMetadata = new ParameterMetadata
                        {
                            Name = parameterName,
                            Description = null,
                            Parameter = parameter,
                            ParameterType = parameter.ParameterType,
                            IsOptional =
                                parameter.HasDefaultValue
                                || parameter.IsOptional
                                || isNullable,
                            IsCancellationToken = parameter.ParameterType == typeof(CancellationToken),
                            DefaultPath = defaultPathAttr?.Path,
                            Ignore = ignoreAttr?.Patterns?.ToList(),
                            GraphQLTypeName = graphQLTypeName,
                        };

                        functionInfo.Parameters.Add(parameterMetadata);
                    }

                    interfaceInfo.Functions.Add(functionInfo);
                }

                DebugLog($"  Interface {type.Name} has {interfaceInfo.Functions.Count} functions");
                if (interfaceInfo.Functions.Count > 0)
                {
                    DebugLog($"  Adding interface {type.Name} to interfaceTypes list");
                    interfaceTypes.Add(interfaceInfo);
                }
                else
                {
                    DebugLog($"  SKIPPING interface {type.Name} - no functions found");
                }
            }
        }

        DebugLog($"BuildModuleTypeInfos completed: {moduleTypes.Count} objects, {interfaceTypes.Count} interfaces");
        return (moduleTypes, interfaceTypes);
    }

    /// <summary>
    /// Unwraps nullable types and determines nullability state.
    /// </summary>
    private static (Type type, bool isNullable) UnwrapNullableType(
        Type clrType,
        ParameterInfo? parameterInfo = null,
        PropertyInfo? propertyInfo = null)
    {
        // Check nullable value types (int?, bool?, etc.) FIRST
        var underlyingNullable = Nullable.GetUnderlyingType(clrType);
        if (underlyingNullable is not null)
        {
            DebugLog($"Detected nullable value type: {underlyingNullable.Name}?");
            return (underlyingNullable, true);
        }
        
        // Check nullable reference types for non-value types
        if (!clrType.IsValueType)
        {
            if (parameterInfo != null)
            {
                var nullabilityInfo = NullabilityContext.Create(parameterInfo);
                var isNullable = nullabilityInfo.WriteState == NullabilityState.Nullable
                              || nullabilityInfo.ReadState == NullabilityState.Nullable;
                DebugLog($"Parameter {parameterInfo.Name}: Type={clrType.Name}, isNullable={isNullable}");
                return (clrType, isNullable);
            }
            
            if (propertyInfo != null)
            {
                var nullabilityInfo = NullabilityContext.Create(propertyInfo);
                var isNullable = nullabilityInfo.ReadState == NullabilityState.Nullable
                              || nullabilityInfo.WriteState == NullabilityState.Nullable;
                DebugLog($"Property {propertyInfo.Name}: Type={clrType.Name}, isNullable={isNullable}");
                return (clrType, isNullable);
            }
        }
        
        return (clrType, false);
    }

    private static Type UnwrapReturnType(
        Type returnType,
        out bool returnsTask,
        out bool returnsValueTask,
        out bool returnsVoid
    )
    {
        returnsTask = false;
        returnsValueTask = false;
        returnsVoid = false;

        if (returnType == typeof(void))
        {
            returnsVoid = true;
            return typeof(void);
        }

        if (returnType == typeof(Task))
        {
            returnsTask = true;
            returnsVoid = true;
            return typeof(void);
        }

        if (returnType == typeof(ValueTask))
        {
            returnsValueTask = true;
            returnsVoid = true;
            return typeof(void);
        }

        if (returnType.IsGenericType && returnType.GetGenericTypeDefinition() == typeof(Task<>))
        {
            returnsTask = true;
            var innerType = returnType.GetGenericArguments()[0];
            returnsVoid = innerType == typeof(void);
            return innerType;
        }

        if (
            returnType.IsGenericType
            && returnType.GetGenericTypeDefinition() == typeof(ValueTask<>)
        )
        {
            returnsValueTask = true;
            var innerType = returnType.GetGenericArguments()[0];
            returnsVoid = innerType == typeof(void);
            return innerType;
        }

        return returnType;
    }

    private static (TypeDef typeDef, bool isNullable) BuildTypeDef(
        Query dag,
        Type clrType,
        ParameterInfo? parameterInfo = null,
        PropertyInfo? propertyInfo = null
    )
    {
        var (unwrappedType, isNullable) = UnwrapNullableType(clrType, parameterInfo, propertyInfo);
        clrType = unwrappedType;

        if (clrType.IsArray)
        {
            var elementType = clrType.GetElementType()!;
            var (elementTypeDef, _) = BuildTypeDef(dag, elementType);
            return (dag.TypeDef().WithListOf(elementTypeDef), isNullable);
        }

        if (clrType.IsGenericType)
        {
            var genericDefinition = clrType.GetGenericTypeDefinition();

            if (
                genericDefinition == typeof(IEnumerable<>)
                || genericDefinition == typeof(IReadOnlyList<>)
                || genericDefinition == typeof(IList<>)
                || genericDefinition == typeof(List<>)
            )
            {
                var elementType = clrType.GetGenericArguments()[0];
                var (elementTypeDef, _) = BuildTypeDef(dag, elementType);
                return (dag.TypeDef().WithListOf(elementTypeDef), isNullable);
            }
        }

        if (clrType == typeof(string))
        {
            return (dag.TypeDef().WithKind(TypeDefKind.STRING_KIND), isNullable);
        }

        if (
            clrType == typeof(int)
            || clrType == typeof(long)
            || clrType == typeof(short)
            || clrType == typeof(byte)
        )
        {
            return (dag.TypeDef().WithKind(TypeDefKind.INTEGER_KIND), isNullable);
        }

        if (clrType == typeof(float) || clrType == typeof(double) || clrType == typeof(decimal))
        {
            return (dag.TypeDef().WithKind(TypeDefKind.FLOAT_KIND), isNullable);
        }

        if (clrType == typeof(bool))
        {
            return (dag.TypeDef().WithKind(TypeDefKind.BOOLEAN_KIND), isNullable);
        }

        if (typeof(Scalar).IsAssignableFrom(clrType))
        {
            return (dag.TypeDef().WithKind(TypeDefKind.SCALAR_KIND), isNullable);
        }

        if (clrType.IsEnum)
        {
            return (dag.TypeDef().WithEnum(clrType.Name), isNullable);
        }

        if (typeof(DaggerObject).IsAssignableFrom(clrType))
        {
            return (dag.TypeDef().WithObject(clrType.Name), isNullable);
        }

        if (clrType.GetCustomAttribute<ObjectAttribute>() is not null)
        {
            return (dag.TypeDef().WithObject(clrType.Name), isNullable);
        }

        // Check for Dagger interface types
        if (clrType.IsInterface && clrType.GetCustomAttribute<InterfaceAttribute>() is not null)
        {
            // Check the registry first to reuse the registered interface name
            // This is similar to Python's get_object_type() approach
            if (InterfaceTypeRegistry.TryGetValue(clrType, out var registeredName))
            {
                DebugLog($"BuildTypeDef for interface {clrType.Name}: using registered name '{registeredName}' from registry");
                var typedef = dag.TypeDef().WithInterface(registeredName);
                DebugLog($"BuildTypeDef created TypeDef.WithInterface('{registeredName}')");
                return (typedef, isNullable);
            }

            // Fallback to attribute name if not yet registered (shouldn't happen in normal flow)
            var interfaceAttr = clrType.GetCustomAttribute<InterfaceAttribute>();
            var interfaceName = interfaceAttr?.Name ?? clrType.Name;
            DebugLog($"BuildTypeDef for interface {clrType.Name}: using fallback name '{interfaceName}' (NOT in registry - this is unexpected!)");
            return (dag.TypeDef().WithInterface(interfaceName), isNullable);
        }

        if (clrType == typeof(void))
        {
            return (dag.TypeDef().WithKind(TypeDefKind.VOID_KIND), true);
        }

        if (clrType == typeof(JsonElement) || clrType == typeof(JsonDocument))
        {
            return (dag.TypeDef().WithKind(TypeDefKind.SCALAR_KIND), isNullable);
        }

        throw new NotSupportedException($"Unsupported type '{clrType.FullName}'.");
    }

    private static object? NormalizeDefaultValue(object? defaultValue)
    {
        return defaultValue switch
        {
            null => null,
            string or bool or int or long or short or byte or double or float or decimal =>
                defaultValue,
            Enum enumValue => enumValue.ToString(),
            _ => null,
        };
    }

    private static async Task<object?> ConvertArgumentAsync(
        JsonElement element,
        Type targetType,
        Query dag,
        string? graphQLTypeName = null
    )
    {
        var underlyingNullable = Nullable.GetUnderlyingType(targetType);
        if (underlyingNullable is not null)
        {
            targetType = underlyingNullable;
            if (element.ValueKind == JsonValueKind.Null)
            {
                return null;
            }
        }

        if (element.ValueKind == JsonValueKind.Null)
        {
            return targetType.IsValueType ? Activator.CreateInstance(targetType) : null;
        }

        if (targetType == typeof(string))
        {
            return element.GetString();
        }

        if (targetType == typeof(int))
        {
            return element.GetInt32();
        }

        if (targetType == typeof(long))
        {
            return element.GetInt64();
        }

        if (targetType == typeof(short))
        {
            return (short)element.GetInt32();
        }

        if (targetType == typeof(byte))
        {
            return (byte)element.GetInt32();
        }

        if (targetType == typeof(bool))
        {
            return element.GetBoolean();
        }

        if (targetType == typeof(double))
        {
            return element.GetDouble();
        }

        if (targetType == typeof(float))
        {
            return element.GetSingle();
        }

        if (targetType == typeof(decimal))
        {
            return element.GetDecimal();
        }

        if (targetType == typeof(Guid))
        {
            return element.GetGuid();
        }

        if (targetType.IsEnum)
        {
            var stringValue = element.GetString();
            if (stringValue is null)
            {
                throw new InvalidOperationException(
                    $"Cannot convert null to enum '{targetType.Name}'."
                );
            }

            return Enum.Parse(targetType, stringValue, ignoreCase: true);
        }

        if (typeof(Scalar).IsAssignableFrom(targetType))
        {
            var scalar = (Scalar)Activator.CreateInstance(targetType)!;
            scalar.Value =
                element.ValueKind == JsonValueKind.String
                    ? element.GetString()!
                    : element.GetRawText();
            return scalar;
        }

        if (typeof(DaggerObject).IsAssignableFrom(targetType))
        {
            var id = element.ValueKind switch
            {
                JsonValueKind.String => element.GetString(),
                JsonValueKind.Object when element.TryGetProperty("id", out var idProperty) =>
                    idProperty.GetString(),
                _ => null,
            };

            if (string.IsNullOrWhiteSpace(id))
            {
                return null;
            }

            // Cache load method lookups
            if (!LoadMethodCache.TryGetValue(targetType, out var loadMethod))
            {
                loadMethod = typeof(Query).GetMethod($"Load{targetType.Name}FromId");
                if (loadMethod is null)
                {
                    throw new NotSupportedException($"Cannot load '{targetType.Name}' from id.");
                }
                LoadMethodCache[targetType] = loadMethod;
            }

            var idType =
                targetType.Assembly.GetType($"{targetType.Namespace}.{targetType.Name}Id")
                ?? throw new NotSupportedException($"Missing id type for '{targetType.Name}'.");

            var idInstance = Activator.CreateInstance(idType);
            idType.GetProperty("Value")?.SetValue(idInstance, id);

            return loadMethod.Invoke(dag, new[] { idInstance });
        }

        // Handle interface types
        if (targetType.IsInterface && targetType.GetCustomAttribute<InterfaceAttribute>() is not null)
        {
            var id = element.ValueKind switch
            {
                JsonValueKind.String => element.GetString(),
                JsonValueKind.Object when element.TryGetProperty("id", out var idProperty) =>
                    idProperty.GetString(),
                _ => null,
            };

            if (string.IsNullOrWhiteSpace(id))
            {
                return null;
            }

            // Following Python/TypeScript pattern:
            // 1. Get the interface name from the registry (local name like "Processor")
            // 2. Prepend the current module name to get the GraphQL typename ("InterfaceExampleProcessor")
            // 3. This works because interfaces are always defined in the module that uses them
            
            string? typename = null;
            if (InterfaceTypeRegistry.TryGetValue(targetType, out var localInterfaceName))
            {
                // Construct the namespaced typename: ModuleName + InterfaceName
                // e.g., "InterfaceExample" + "Processor" = "InterfaceExampleProcessor"
                if (!string.IsNullOrWhiteSpace(_currentModuleName))
                {
                    typename = _currentModuleName + localInterfaceName;
                    DebugLog($"[ConvertArgument] Constructed typename '{typename}' from module '{_currentModuleName}' + interface '{localInterfaceName}'");
                }
                else
                {
                    // Fallback to just the interface name if module name not set
                    typename = localInterfaceName;
                    DebugLog($"[ConvertArgument] Using local interface name '{typename}' (module name not set)");
                }
            }
            else
            {
                DebugLog($"[ConvertArgument] Interface type {targetType.Name} not found in registry!");
            }

            // Create a dynamic wrapper that implements the interface
            return await CreateInterfaceWrapperAsync(dag, targetType, id, typename);
        }

        if (targetType.IsArray)
        {
            var elementType = targetType.GetElementType()!;
            var items = new List<object?>();
            foreach (var item in element.EnumerateArray())
            {
                items.Add(await ConvertArgumentAsync(item, elementType, dag, null));
            }

            var array = Array.CreateInstance(elementType, items.Count);
            for (var i = 0; i < items.Count; i++)
            {
                array.SetValue(items[i], i);
            }

            return array;
        }

        if (targetType.IsGenericType)
        {
            var genericDefinition = targetType.GetGenericTypeDefinition();

            if (
                genericDefinition == typeof(IEnumerable<>)
                || genericDefinition == typeof(IReadOnlyList<>)
                || genericDefinition == typeof(IList<>)
                || genericDefinition == typeof(List<>)
            )
            {
                var elementType = targetType.GetGenericArguments()[0];
                var listType = typeof(List<>).MakeGenericType(elementType);
                var list = (IList)Activator.CreateInstance(listType)!;

                foreach (var item in element.EnumerateArray())
                {
                    list.Add(await ConvertArgumentAsync(item, elementType, dag, null));
                }

                if (genericDefinition == typeof(List<>))
                {
                    return list;
                }

                return list;
            }

            if (genericDefinition == typeof(Dictionary<,>))
            {
                return JsonSerializer.Deserialize(
                    element.GetRawText(),
                    targetType,
                    SerializerOptions
                );
            }
        }

        if (targetType == typeof(JsonElement))
        {
            return element.Clone();
        }

        if (targetType == typeof(JsonDocument))
        {
            return JsonDocument.Parse(element.GetRawText());
        }

        return JsonSerializer.Deserialize(element.GetRawText(), targetType, SerializerOptions);
    }

    private static Task<object?> CreateInterfaceWrapperAsync(Query dag, Type interfaceType, string id, string? typename = null)
    {
        if (!interfaceType.IsInterface)
        {
            throw new ArgumentException(
                $"Type '{interfaceType.Name}' is not an interface.",
                nameof(interfaceType));
        }

        // Determine the interface name to use in the GraphQL query
        // - Cross-module: Use typename from JSON (includes module prefix: "InterfaceExampleProcessor")
        // - Same-module: Use registry (local name: "Processor") 
        string interfaceName;
        if (!string.IsNullOrWhiteSpace(typename))
        {
            interfaceName = typename;
            DebugLog($"[Wrapper] Using typename '{interfaceName}' from JSON (cross-module interface) for '{interfaceType.Name}'");
        }
        else if (InterfaceTypeRegistry.TryGetValue(interfaceType, out var registeredName))
        {
            interfaceName = registeredName;
            DebugLog($"[Wrapper] Using registered name '{interfaceName}' from registry (same-module interface) for '{interfaceType.Name}'");
        }
        else
        {
            // This should never happen - all [Interface]-decorated types are registered during startup
            throw new InvalidOperationException(
                $"Interface '{interfaceType.FullName}' not found in registry. " +
                $"This indicates a bug in interface registration.");
        }

        DebugLog($"[DispatchProxy] Creating proxy for interface '{interfaceType.Name}' with id '{id}' and typename '{interfaceName}'");
        
        // Use DispatchProxy to create a runtime implementation
        // Need to use reflection to call DaggerInterfaceProxy<T>.Create with the actual interface type
        var proxyType = typeof(DaggerInterfaceProxy<>).MakeGenericType(interfaceType);
        var createMethod = proxyType.GetMethod("Create", BindingFlags.Public | BindingFlags.Static);
        
        if (createMethod == null)
        {
            throw new InvalidOperationException($"Could not find Create method on {proxyType.Name}");
        }
        
        var proxy = createMethod.Invoke(null, new object[] { dag, interfaceName, id });
        
        if (proxy == null)
        {
            throw new InvalidOperationException(
                $"Failed to create dynamic proxy for interface '{interfaceName}'.");
        }
        
        DebugLog($"[DispatchProxy] Successfully created proxy instance for interface '{interfaceType.Name}'");
        return Task.FromResult<object?>(proxy);
    }

    private static async Task<object?> NormalizeResultAsync(object? value)
    {
        if (value is null)
        {
            return null;
        }

        switch (value)
        {
            case string or bool or int or long or short or byte or double or float or decimal:
                return value;
            case Enum enumValue:
                return enumValue.ToString();
            case Scalar scalar:
                return scalar.Value;
            case JsonElement element:
                return JsonSerializer.Deserialize<object>(element.GetRawText(), SerializerOptions);
            case JsonDocument document:
                return JsonSerializer.Deserialize<object>(
                    document.RootElement.GetRawText(),
                    SerializerOptions
                );
            case IEnumerable sequence when value is not string:
            {
                var list = new List<object?>();
                foreach (var item in sequence)
                {
                    list.Add(await NormalizeResultAsync(item));
                }

                return list;
            }
        }

        if (value is DaggerObject daggerObject)
        {
            var objectType = daggerObject.GetType();
            
            // Cache IdAsync method lookup to avoid repeated reflection
            if (!IdAsyncMethodCache.TryGetValue(objectType, out var idAsyncMethod))
            {
                idAsyncMethod = objectType.GetMethod("IdAsync", new[] { typeof(CancellationToken) })
                    ?? throw new InvalidOperationException($"Type '{objectType.Name}' does not have IdAsync method.");
                IdAsyncMethodCache[objectType] = idAsyncMethod;
            }
            
            // Invoke IdAsync with default cancellation token - returns Task<TId> where TId : Scalar
            var idTask = (Task)idAsyncMethod.Invoke(daggerObject, new object[] { CancellationToken.None })!;
            await idTask.ConfigureAwait(false);
            
            // Extract result from Task<TId> using reflection to avoid dynamic
            var resultProperty = idTask.GetType().GetProperty("Result");
            var scalarId = resultProperty!.GetValue(idTask);
            
            // Get Value property from the Scalar
            var valueProperty = scalarId!.GetType().GetProperty("Value");
            return valueProperty!.GetValue(scalarId) as string;
        }

        // Handle custom module objects - recursively normalize their properties
        if (value.GetType().GetCustomAttribute<ObjectAttribute>() is not null)
        {
            var dict = new Dictionary<string, object?>();
            foreach (
                var prop in value
                    .GetType()
                    .GetProperties(BindingFlags.Public | BindingFlags.Instance)
            )
            {
                var fieldAttr = prop.GetCustomAttribute<FieldAttribute>();
                if (fieldAttr is null)
                {
                    continue;
                }

                var propValue = prop.GetValue(value);
                var fieldName = ToCamelCase(fieldAttr.Name ?? prop.Name);
                dict[fieldName] = await NormalizeResultAsync(propValue);
            }
            return dict;
        }

        return JsonSerializer.Deserialize<object>(
            JsonSerializer.Serialize(value, SerializerOptions),
            SerializerOptions
        );
    }

    private static async Task ReturnErrorAsync(Query dag, FunctionCall fnCall, Exception ex)
    {
        Console.Error.WriteLine(ex);
        await ReturnErrorAsync(dag, fnCall, ex.Message);
    }

    private static async Task ReturnErrorAsync(Query dag, FunctionCall fnCall, string message)
    {
        var error = dag.Error(message);
        await fnCall.ReturnErrorAsync(error);
    }

    /// <summary>
    /// Discovers all types in the entry assembly marked with [Object].
    /// </summary>
    private static List<Type> DiscoverModuleTypes()
    {
        var assembly = Assembly.GetEntryAssembly();
        if (assembly == null)
        {
            return new List<Type>();
        }

        return assembly
            .GetTypes()
            .Where(t =>
                t.GetCustomAttributes(false).Any(a => 
                    a.GetType().Name == "ObjectAttribute" || 
                    a.GetType().Name == "InterfaceAttribute")
            )
            .ToList();
    }

    /// <summary>
    /// Gets the description from a type's XML documentation or attributes.
    /// </summary>
    private static string? GetTypeDescription(Type type)
    {
        // First try to get description from attribute
        var attr = type.GetCustomAttributes(false)
            .FirstOrDefault(a => a.GetType().Name == "ObjectAttribute");

        if (attr != null)
        {
            var descProp = attr.GetType().GetProperty("Description");
            if (descProp != null)
            {
                var description = descProp.GetValue(attr) as string;
                if (!string.IsNullOrWhiteSpace(description))
                {
                    return description;
                }
            }
        }

        // Fall back to XML documentation
        return GetXmlDocumentation(type);
    }

    /// <summary>
    /// Gets the description from a method's XML documentation or attributes.
    /// </summary>
    private static string? GetMethodDescription(MethodInfo method)
    {
        // First try to get description from attribute
        var attr = method
            .GetCustomAttributes(false)
            .FirstOrDefault(a => a.GetType().Name == "FunctionAttribute");

        if (attr != null)
        {
            var descProp = attr.GetType().GetProperty("Description");
            if (descProp != null)
            {
                var description = descProp.GetValue(attr) as string;
                if (!string.IsNullOrWhiteSpace(description))
                {
                    return description;
                }
            }
        }

        // Fall back to XML documentation
        return GetXmlDocumentation(method);
    }

    /// <summary>
    /// Converts a PascalCase name to camelCase.
    /// </summary>
    private static string ToCamelCase(string name)
    {
        if (string.IsNullOrEmpty(name) || char.IsLower(name[0]))
            return name;

        // Check cache first
        if (CamelCaseCache.TryGetValue(name, out var cached))
            return cached;

        var result = string.Create(name.Length, name, (span, n) =>
        {
            n.AsSpan().CopyTo(span);
            span[0] = char.ToLowerInvariant(span[0]);
        });
        
        // Only cache small strings to avoid memory bloat
        if (name.Length < 100)
        {
            CamelCaseCache[name] = result;
        }
        
        return result;
    }

    /// <summary>
    /// Discovers all enum types in the entry assembly marked with [DaggerEnum].
    /// </summary>
    private static IReadOnlyList<EnumTypeInfo> BuildEnumTypeInfos()
    {
        var enumTypes = new List<EnumTypeInfo>();
        var assembly = Assembly.GetEntryAssembly();

        if (assembly == null)
        {
            return enumTypes;
        }

        foreach (var type in assembly.GetTypes().Where(t => t.IsEnum))
        {
            var enumAttr = type.GetCustomAttribute<EnumAttribute>();
            if (enumAttr is null)
            {
                continue; // Only process enums with [DaggerEnum]
            }

            var enumInfo = new EnumTypeInfo
            {
                Name = enumAttr.Name ?? type.Name,
                Description = enumAttr.Description,
                EnumType = type,
            };

            foreach (var field in type.GetFields(BindingFlags.Public | BindingFlags.Static))
            {
                var valueAttr = field.GetCustomAttribute<EnumValueAttribute>();
                var value = field.GetRawConstantValue()?.ToString() ?? field.Name;

                enumInfo.Values.Add(
                    new EnumValueInfo
                    {
                        Name = field.Name,
                        Value = value,
                        Description = valueAttr?.Description,
                        Deprecated = valueAttr?.Deprecated,
                    }
                );
            }

            enumTypes.Add(enumInfo);
        }

        return enumTypes;
    }

    /// <summary>
    /// Gets the module constructor if it exists and has parameters.
    /// </summary>
    private static ConstructorInfo? GetModuleConstructor(Type moduleType)
    {
        var constructors = moduleType.GetConstructors(BindingFlags.Public | BindingFlags.Instance);

        // Prefer default constructor for parameter-less case
        var defaultCtor = constructors.FirstOrDefault(c => c.GetParameters().Length == 0);
        if (defaultCtor != null && constructors.Length == 1)
        {
            return null; // Only default constructor, no need to register
        }

        // Return the first constructor with parameters, or null if only default exists
        return constructors.FirstOrDefault(c => c.GetParameters().Length > 0);
    }

    /// <summary>
    /// Loads the XML documentation file for the entry assembly.
    /// </summary>
    private static void LoadXmlDocumentation()
    {
        if (_xmlDocumentationLoaded)
        {
            return;
        }

        _xmlDocumentationLoaded = true;

        try
        {
            var assembly = Assembly.GetEntryAssembly();
            if (assembly == null)
            {
                return;
            }

            var xmlPath = Path.ChangeExtension(assembly.Location, ".xml");
            if (System.IO.File.Exists(xmlPath))
            {
                _xmlDocumentation = XDocument.Load(xmlPath);
            }
        }
        catch
        {
            // Silently ignore XML documentation loading errors
        }
    }

    /// <summary>
    /// Gets XML documentation for a type.
    /// </summary>
    private static string? GetXmlDocumentation(Type type)
    {
        LoadXmlDocumentation();

        if (_xmlDocumentation == null)
        {
            return null;
        }

        var memberName = $"T:{type.FullName}";
        return ExtractSummary(memberName);
    }

    /// <summary>
    /// Gets XML documentation for a method.
    /// </summary>
    private static string? GetXmlDocumentation(MethodInfo method)
    {
        LoadXmlDocumentation();

        if (_xmlDocumentation == null)
        {
            return null;
        }

        var parameters = method.GetParameters();
        var paramList =
            parameters.Length > 0
                ? $"({string.Join(",", parameters.Select(p => p.ParameterType.FullName))})"
                : string.Empty;

        var memberName = $"M:{method.DeclaringType?.FullName}.{method.Name}{paramList}";
        return ExtractSummary(memberName);
    }

    /// <summary>
    /// Extracts the summary text from XML documentation.
    /// </summary>
    private static string? ExtractSummary(string memberName)
    {
        if (_xmlDocumentation == null)
        {
            return null;
        }

        var member = _xmlDocumentation
            .Descendants("member")
            .FirstOrDefault(m => m.Attribute("name")?.Value == memberName);

        if (member == null)
        {
            return null;
        }

        var summary = member.Element("summary");
        if (summary == null)
        {
            return null;
        }

        // Clean up the summary text (remove extra whitespace, trim)
        var text = summary
            .Value.Split('\n')
            .Select(line => line.Trim())
            .Where(line => !string.IsNullOrWhiteSpace(line));

        return string.Join(" ", text);
    }
}
