namespace Dagger;

/// <summary>
/// Interface for Dagger types that have an ID.
/// </summary>
/// <typeparam name="TId">The scalar ID type.</typeparam>
public interface IId<TId>
    where TId : Scalar
{
    /// <summary>
    /// Retrieves the unique identifier for this object.
    /// </summary>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns>The unique identifier.</returns>
    Task<TId> Id(CancellationToken cancellationToken = default);
}
