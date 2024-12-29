using System;
using System.Collections.Generic;

namespace Dagger.SDK.SourceGenerator.Extensions;

public static class EnumerableExtensions
{
    #if NETSTANDARD2_0
    
    public static IEnumerable<TSource> ExceptBy<TSource, TKey>(
        this IEnumerable<TSource> source,
        IEnumerable<TKey> second,
        Func<TSource, TKey> keySelector
    )
    {
        HashSet<TKey> keys = [.. second];

        foreach (TSource element in source)
        {
            if (keys.Contains(keySelector(element)))
            {
                continue;
            }
            yield return element;
        }
    }

    #endif
}
