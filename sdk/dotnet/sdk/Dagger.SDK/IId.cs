namespace Dagger.SDK;

public interface IId<TId>
    where TId : Scalar
{
    Task<TId> IdAsync(CancellationToken cancellationToken = default);
}
