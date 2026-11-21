package io.dagger.client;

import static io.dagger.client.exception.DaggerExceptionConstants.CODE_KEY;
import static io.dagger.client.exception.DaggerExceptionConstants.CODE_VALIDATION_FAILED_VALUE;
import static io.dagger.client.exception.DaggerExceptionConstants.TYPE_EXEC_ERROR_VALUE;
import static io.dagger.client.exception.DaggerExceptionConstants.TYPE_KEY;

import io.dagger.client.exception.DaggerExecException;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.exception.StaleClientException;
import io.dagger.client.graphql.GraphQLClient;
import io.dagger.client.graphql.GraphQLError;
import io.dagger.client.graphql.GraphQLResponse;
import jakarta.json.JsonArray;
import jakarta.json.JsonObject;
import jakarta.json.JsonString;
import jakarta.json.JsonValue;
import jakarta.json.bind.Jsonb;
import jakarta.json.bind.JsonbBuilder;
import jakarta.json.bind.JsonbConfig;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.ParameterizedType;
import java.lang.reflect.Type;
import java.util.ArrayList;
import java.util.Deque;
import java.util.LinkedHashSet;
import java.util.LinkedList;
import java.util.List;
import java.util.Set;
import java.util.Spliterators;
import java.util.concurrent.ExecutionException;
import java.util.stream.Collectors;
import java.util.stream.StreamSupport;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

/**
 * Builds and executes one GraphQL selection chain.
 *
 * <p>Public because generated clients live in packages of their own and chain through it. Not a
 * user-facing API: user code goes through the generated types.
 */
public final class QueryBuilder {

  static final Logger LOG = LoggerFactory.getLogger(QueryBuilder.class);

  private final GraphQLClient client;
  private final ModuleLoader loader;
  private final Set<ModuleTarget> targets;
  private final Deque<QueryPart> parts;
  private final List<QueryPart> leaves;
  private final String inlineFragmentType;

  public QueryBuilder(GraphQLClient client) {
    this(client, new ModuleLoader(), Set.of(), new LinkedList<>(), new ArrayList<>(), null);
  }

  private QueryBuilder(
      GraphQLClient client,
      ModuleLoader loader,
      Set<ModuleTarget> targets,
      Deque<QueryPart> parts,
      List<String> finalFields,
      String inlineFragmentType) {
    this.client = client;
    this.loader = loader;
    this.targets = targets;
    this.parts = parts;
    this.leaves = finalFields.stream().map(QueryPart::new).toList();
    this.inlineFragmentType = inlineFragmentType;
  }

  /**
   * The same chain, reaching into {@code target}. Its queries first serve the target into the
   * session, once per session.
   */
  public QueryBuilder withTarget(ModuleTarget target) {
    Set<ModuleTarget> targets = new LinkedHashSet<>(this.targets);
    targets.add(target);
    return new QueryBuilder(
        client,
        loader,
        targets,
        parts,
        leaves.stream().map(QueryPart::getOperation).toList(),
        inlineFragmentType);
  }

  public QueryBuilder chain(String operation) {
    return chain(operation, Arguments.noArgs());
  }

  public QueryBuilder chain(String operation, Arguments arguments) {
    if (leaves != null && !leaves.isEmpty()) {
      throw new IllegalStateException("A new field cannot be chained");
    }
    Deque<QueryPart> list = new LinkedList<>();
    list.addAll(this.parts);
    list.push(new QueryPart(operation, arguments));
    return new QueryBuilder(client, loader, targets, list, new ArrayList<>(), inlineFragmentType);
  }

  public QueryBuilder chain(String operation, List<String> leaves) {
    if (!this.leaves.isEmpty()) {
      throw new IllegalStateException("A new field cannot be chained");
    }
    Deque<QueryPart> list = new LinkedList<>();
    list.addAll(this.parts);
    list.push(new QueryPart(operation));
    return new QueryBuilder(client, loader, targets, list, leaves, inlineFragmentType);
  }

  public QueryBuilder chain(List<String> leaves) {
    if (!this.leaves.isEmpty()) {
      throw new IllegalStateException("A new field cannot be chained");
    }
    Deque<QueryPart> list = new LinkedList<>();
    list.addAll(this.parts);
    return new QueryBuilder(client, loader, targets, list, leaves, inlineFragmentType);
  }

  /**
   * Create a QueryBuilder for node(id:) with an inline fragment for the given type.
   *
   * <p>This produces queries like: {@code node(id: "...") { ... on Container { field { ... } } }}
   *
   * <p>The node lookup starts a new query on the same session, whatever this builder's own chain
   * is: an ID addresses an object from the root. It keeps this chain's targets, because the
   * object's type may be one of theirs.
   */
  public QueryBuilder chainNode(String typeName, Object id) {
    Deque<QueryPart> list = new LinkedList<>();
    // Unwrap Scalar (e.g. ID) to its inner value — Scalar doesn't override toString()
    String idStr =
        (id instanceof Scalar<?>) ? ((Scalar<?>) id).convert().toString() : id.toString();
    list.push(new QueryPart("node", Arguments.newBuilder().add("id", idStr).build()));
    return new QueryBuilder(client, loader, targets, list, new ArrayList<>(), typeName);
  }

  private void handleErrors(GraphQLResponse response) throws DaggerQueryException {
    if (!response.hasError()) {
      return;
    }
    LOG.debug(
        String.format(
            "Query execution failed: %s",
            response.getErrors().stream()
                .map(GraphQLError::toString)
                .collect(Collectors.joining(", "))));
    if (response.getErrors().isEmpty()) {
      throw new DaggerQueryException();
    }
    if (!targets.isEmpty()) {
      for (GraphQLError error : response.getErrors()) {
        if (failedValidation(error)) {
          throw new StaleClientException(error, targets);
        }
      }
    }

    GraphQLError error = response.getErrors().get(0);
    String errorType = (String) error.getExtensions().getOrDefault(TYPE_KEY, null);

    if (TYPE_EXEC_ERROR_VALUE.equalsIgnoreCase(errorType)) {
      throw new DaggerExecException(response.getErrors().get(0));
    }

    throw new DaggerQueryException(response.getErrors().get(0));
  }

  /**
   * Validation fails before anything runs, so its error has no path. An error with a path comes
   * from something that ran, such as a nested call whose own client is stale.
   */
  private static boolean failedValidation(GraphQLError error) {
    return error.getPath().length == 0
        && CODE_VALIDATION_FAILED_VALUE.equals(error.getExtensions().get(CODE_KEY));
  }

  String buildQuery() throws ExecutionException, InterruptedException, DaggerQueryException {
    String block = leaves.stream().map(QueryPart::getOperation).collect(Collectors.joining(" "));
    // parts front-to-back is innermost-to-outermost; wrap each level around the previous
    List<QueryPart> chain = new ArrayList<>(parts);
    for (int i = 0; i < chain.size(); i++) {
      boolean outermost = i == chain.size() - 1;
      if (outermost && inlineFragmentType != null && !block.isEmpty()) {
        // node(id: "...") { ... on TypeName { nested { fields } } }
        block = "... on " + inlineFragmentType + " {" + block + "}";
      }
      String rendered = chain.get(i).toGraphQL();
      block = block.isEmpty() ? rendered : rendered + " {" + block + "}";
    }
    return "query {" + block + "}";
  }

  GraphQLResponse executeQuery(String query)
      throws ExecutionException, InterruptedException, DaggerQueryException {
    for (ModuleTarget target : targets) {
      loader.load(
          new QueryBuilder(client, loader, Set.of(), new LinkedList<>(), List.of(), null), target);
    }
    LOG.debug("Executing query: {}", query);
    GraphQLResponse response = client.executeQuery(query);
    handleErrors(response);
    LOG.debug("Received response: {}", response.getData());
    return response;
  }

  /**
   * Execute a query and discord the response.
   *
   * @throws ExecutionException
   * @throws InterruptedException
   * @throws DaggerQueryException
   */
  public void executeQuery() throws ExecutionException, InterruptedException, DaggerQueryException {
    executeQuery(buildQuery());
  }

  public <T> T executeQuery(Class<T> klass)
      throws ExecutionException, InterruptedException, DaggerQueryException {
    List<String> pathElts =
        StreamSupport.stream(
                Spliterators.spliteratorUnknownSize(parts.descendingIterator(), 0), false)
            .map(QueryPart::getOperation)
            .toList();
    GraphQLResponse response = executeQuery(buildQuery());
    JsonValue value = response.getData();
    for (String elt : pathElts) {
      value = (value instanceof JsonObject obj) ? obj.get(elt) : null;
    }
    if (value == null || value.getValueType() == JsonValue.ValueType.NULL) {
      return null;
    }
    if (Scalar.class.isAssignableFrom(klass)) {
      // FIXME scalar could be other types than String in the future
      String str = (value instanceof JsonString js) ? js.getString() : value.toString();
      try {
        return klass.getDeclaredConstructor(String.class).newInstance(str);
      } catch (NoSuchMethodException
          | InstantiationException
          | IllegalAccessException
          | InvocationTargetException nsme) {
        // FIXME - This may not happen
        throw new RuntimeException(nsme);
      }
    }
    if (klass == String.class && value instanceof JsonString js) {
      return klass.cast(js.getString());
    }
    if (klass.isEnum()
        && GraphQLEnum.class.isAssignableFrom(klass)
        && value instanceof JsonString js) {
      // Constants named apart from their schema values are looked up by value.
      return GraphQLEnums.valueOf(klass, js.getString());
    }
    JsonbConfig config =
        new JsonbConfig().withPropertyVisibilityStrategy(new PrivateVisibilityStrategy());
    Jsonb jsonb = JsonbBuilder.newBuilder().withConfig(config).build();
    return jsonb.fromJson(value.toString(), klass);
  }

  /**
   * Resolve a nullable object field, returning a QueryBuilder that loads it via node(id:), or null
   * when the field resolved to null.
   *
   * <p>The field's id has to be fetched to tell the two apart, so unlike a non-null object field
   * this cannot stay lazy. What comes back is lazy again: the caller wraps it in a normal client
   * object.
   */
  public QueryBuilder executeNullableObjectQuery(String graphqlTypeName)
      throws ExecutionException, InterruptedException, DaggerQueryException {
    // chain(String), not chain(List): only parts are walked when reading the response back.
    String id = chain("id").executeQuery(String.class);
    if (id == null) {
      return null;
    }
    return chainNode(graphqlTypeName, id);
  }

  public <T> List<T> executeListQuery(Class<T> klass)
      throws ExecutionException, InterruptedException, DaggerQueryException {
    List<String> pathElts =
        StreamSupport.stream(
                Spliterators.spliteratorUnknownSize(parts.descendingIterator(), 0), false)
            .map(QueryPart::getOperation)
            .toList();
    GraphQLResponse response = executeQuery(buildQuery());
    JsonObject obj = response.getData();
    for (int i = 0; i < pathElts.size() - 1; i++) {
      obj = obj.getJsonObject(pathElts.get(i));
    }
    JsonArray array = obj.getJsonArray(pathElts.get(pathElts.size() - 1));
    JsonbConfig config =
        new JsonbConfig().withPropertyVisibilityStrategy(new PrivateVisibilityStrategy());
    Jsonb jsonb = JsonbBuilder.newBuilder().withConfig(config).build();
    List<T> rv = jsonb.fromJson(array.toString(), listOf(klass));
    return rv;
  }

  private static Type listOf(Class<?> element) {
    return new ParameterizedType() {
      @Override
      public Type[] getActualTypeArguments() {
        return new Type[] {element};
      }

      @Override
      public Type getRawType() {
        return List.class;
      }

      @Override
      public Type getOwnerType() {
        return null;
      }
    };
  }

  /**
   * Execute a list query for objects, returning QueryBuilders that load each element via node(id:).
   *
   * @param graphqlTypeName the GraphQL type name for inline fragment resolution
   */
  public List<QueryBuilder> executeObjectListQuery(String graphqlTypeName)
      throws ExecutionException, InterruptedException, DaggerQueryException {
    List<String> pathElts =
        StreamSupport.stream(
                Spliterators.spliteratorUnknownSize(parts.descendingIterator(), 0), false)
            .map(QueryPart::getOperation)
            .toList();
    GraphQLResponse response = executeQuery(buildQuery());
    JsonObject obj = response.getData();
    for (int i = 0; i < pathElts.size() - 1; i++) {
      obj = obj.getJsonObject(pathElts.get(i));
    }
    JsonArray array = obj.getJsonArray(pathElts.get(pathElts.size() - 1));
    List<QueryBuilder> rv = new ArrayList<>();
    for (int i = 0; i < array.size(); i++) {
      String id = array.getJsonObject(i).getString("id");
      QueryBuilder qb = chainNode(graphqlTypeName, id);
      rv.add(qb);
    }
    return rv;
  }
}
