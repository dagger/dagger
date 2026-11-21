package io.dagger.client;

import io.dagger.client.exception.DaggerQueryException;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentMap;
import java.util.concurrent.ExecutionException;

/** Serves each target into one session once, however many selections reach for it. */
final class ModuleLoader {

  private final ConcurrentMap<ModuleTarget, Load> loads = new ConcurrentHashMap<>();

  void load(QueryBuilder root, ModuleTarget target)
      throws ExecutionException, InterruptedException, DaggerQueryException {
    loads.computeIfAbsent(target, t -> new Load()).once(root, target);
  }

  /**
   * Concurrent selections of one target wait for a single serve. A refused serve is not recorded,
   * so the next selection tries again.
   */
  private static final class Load {

    private boolean done;

    synchronized void once(QueryBuilder root, ModuleTarget target)
        throws ExecutionException, InterruptedException, DaggerQueryException {
      if (done) {
        return;
      }
      Arguments.Builder args = Arguments.newBuilder().add("address", target.address());
      if (target.pin() != null) {
        args.add("refPin", target.pin());
      }
      root.chain("serveModule", args.build()).executeQuery();
      done = true;
    }
  }
}
