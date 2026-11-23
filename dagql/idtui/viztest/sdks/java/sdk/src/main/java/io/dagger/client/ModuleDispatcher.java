package io.dagger.client;

import io.dagger.client.engineconn.Connection;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.client.telemetry.Telemetry;
import jakarta.json.Json;
import jakarta.json.JsonArrayBuilder;
import jakarta.json.JsonException;
import jakarta.json.JsonObject;
import jakarta.json.JsonObjectBuilder;
import jakarta.json.JsonReader;
import jakarta.json.JsonString;
import jakarta.json.JsonValue;
import jakarta.json.stream.JsonParser;
import jakarta.json.stream.JsonParsingException;
import java.io.IOException;
import java.io.InputStream;
import java.io.PrintStream;
import java.io.StringReader;
import java.lang.reflect.InvocationTargetException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Collections;
import java.util.HashMap;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.Function;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Answers one call of a module's Dang entrypoint: the request on standard input, the result in a
 * file, and a failure reported on standard error.
 *
 * <p>Values travel as the core JSON scalar, whose generated class is named after the schema the SDK
 * was generated from ({@code JSON}, or {@code Json} under engine-formatted names). This
 * hand-written class can't name it, so it takes the class as a type parameter and its {@code from}
 * factory from the generated entrypoint.
 */
public final class ModuleDispatcher {

  /**
   * Starts the last line of standard error when the function fails, for an engine that names no
   * {@link #FUNCTION_ERROR_PATH_ENV file} for its error; the entrypoint reads it.
   */
  public static final String FUNCTION_ERROR_MARKER = "::dagger-function-error::";

  /**
   * Names the file the engine gives a call's process to write its function's error to. The engine
   * fails the call with the error the file reports, values and all, and shows it.
   */
  public static final String FUNCTION_ERROR_PATH_ENV = "DAGGER_FUNCTION_ERROR_PATH";

  /** The largest function error file the engine reads. */
  static final int FUNCTION_ERROR_FILE_LIMIT = 1 << 20;

  /** What a report over the limit keeps of each long string, before it drops the values. */
  static final int SHORTENED_LENGTH = 64 * 1024;

  /**
   * The engine hands the entrypoint the last 100 KiB of a failed exec's standard error, and JSON
   * encodes a character in at most six bytes.
   */
  static final int MAX_REPORTED_ERROR_LENGTH = 16 * 1024;

  /** An origin an error's message names: the span of the call the engine reports it from. */
  static final Pattern ERROR_ORIGIN =
      Pattern.compile("\\s*\\[traceparent:[0-9a-f]{32}-[0-9a-f]{16}\\]");

  /**
   * The packages of frames a short stack trace leaves out: the JDK's, the SDK's, the generated
   * entrypoint's and clients', and those of the libraries the SDK runs on. What remains is the
   * module's own code.
   */
  static final List<String> FRAMEWORK_PACKAGES =
      List.of(
          "java.",
          "javax.",
          "jdk.",
          "sun.",
          "com.sun.",
          "io.dagger.client.",
          "io.dagger.gen.",
          "io.dagger.module.",
          "io.dagger.annotation.",
          "jakarta.",
          "org.eclipse.parsson.",
          "org.eclipse.yasson.",
          "io.opentelemetry.",
          "org.slf4j.");

  /** Where the engine keeps a collection's base in the object's state, outside its schema. */
  static final String COLLECTION_BASE_FIELD = "__daggerCollectionBase";

  private ModuleDispatcher() {}

  /** The generated entrypoint's dispatch switch, over the core JSON scalar class {@code J}. */
  @FunctionalInterface
  public interface Dispatch<J extends Scalar<String>> {
    J call(J parentJson, String parentName, String fnName, Map<String, J> inputArgs)
        throws Exception;
  }

  /**
   * Run the {@code engine-call} mode: {@code engine-call [--output PATH]}. Returns the process exit
   * code.
   *
   * @param json the JSON scalar's factory, {@code JSON::from}
   */
  public static <J extends Scalar<String>> int engineCall(
      String[] args, Function<String, J> json, Dispatch<J> dispatch) {
    String errorPath = System.getenv(FUNCTION_ERROR_PATH_ENV);
    return engineCall(
        args,
        json,
        dispatch,
        errorPath == null || errorPath.isEmpty() ? null : Path.of(errorPath));
  }

  /**
   * {@link #engineCall(String[], Function, Dispatch)}, reporting a failure in {@code errorFile},
   * or with the marker when it is null.
   */
  static <J extends Scalar<String>> int engineCall(
      String[] args, Function<String, J> json, Dispatch<J> dispatch, Path errorFile) {
    Connection.markModule();
    try (Telemetry telemetry = new Telemetry()) {
      Path output = outputPath(args);
      String result = dispatch(System.in, json, dispatch);
      if (output == null) {
        System.out.println(result);
      } else {
        if (output.getParent() != null) {
          Files.createDirectories(output.getParent());
        }
        Files.writeString(output, result, StandardCharsets.UTF_8);
      }
      return 0;
    } catch (Throwable e) {
      // The resources are closed by now, so the function's spans are flushed.
      reportFailure(System.err, e, errorFile);
      return 2;
    }
  }

  static <J extends Scalar<String>> String dispatch(
      InputStream in, Function<String, J> json, Dispatch<J> dispatch) throws Exception {
    JsonObject request = readRequest(in);
    String parentName = requiredString(request, "receiverType");
    String fnName = requiredString(request, "fnName");
    J parentJson = json.apply(jsonText(request, "receiverValue"));
    JsonValue args = parse(jsonText(request, "fnArgs"), "fnArgs");
    if (args.getValueType() != JsonValue.ValueType.OBJECT) {
      throw new IllegalArgumentException("call request field fnArgs is not a JSON object");
    }
    Map<String, J> inputArgs = new HashMap<>();
    for (Map.Entry<String, JsonValue> arg : args.asJsonObject().entrySet()) {
      inputArgs.put(arg.getKey(), json.apply(arg.getValue().toString()));
    }
    J value = dispatch.call(parentJson, parentName, fnName, inputArgs);
    return value == null ? "null" : value.convert();
  }

  /**
   * The JSON of what a function of a collection returned. When the function returned its receiver,
   * the receiver's base goes with it, so the engine computes the delta against the collection the
   * module first returned. Any other result, a new instance included, starts a base of its own.
   *
   * @param json the JSON scalar's factory, {@code JSON::from}
   */
  public static <J extends Scalar<String>> J collectionResult(
      J receiverJson, Object receiver, Object result, J resultJson, Function<String, J> json) {
    if (result != receiver) {
      return resultJson;
    }
    JsonValue state = read(receiverJson);
    if (state.getValueType() != JsonValue.ValueType.OBJECT) {
      return resultJson;
    }
    JsonValue base = state.asJsonObject().get(COLLECTION_BASE_FIELD);
    if (base == null) {
      return resultJson;
    }
    return json.apply(
        Json.createObjectBuilder(read(resultJson).asJsonObject())
            .add(COLLECTION_BASE_FIELD, base)
            .build()
            .toString());
  }

  private static JsonValue read(Scalar<String> json) {
    try (JsonReader reader = Json.createReader(new StringReader(json.convert()))) {
      return reader.readValue();
    }
  }

  /**
   * Report the function's error. With an {@code errorFile}, the engine reads the error there and
   * shows it, so only a short stack trace of the module's own frames is printed, and nothing for an
   * API error whose origin the engine shows. Without one, or when the file can't be written, the
   * error is reported last on standard error, after its full stack trace, on one marker line
   * holding its message as a JSON string: the function's own output shares standard error, so the
   * marker comes after everything else.
   */
  static void reportFailure(PrintStream err, Throwable e, Path errorFile) {
    Throwable cause = e instanceof InvocationTargetException ite ? ite.getTargetException() : e;
    String message = messageOf(cause);
    if (errorFile != null && writeFunctionError(errorFile, cause, message)) {
      if (!hasOrigin(cause)) {
        printShortStackTrace(err, cause);
      }
      err.flush();
      return;
    }
    cause.printStackTrace(err);
    err.println(FUNCTION_ERROR_MARKER + Json.createValue(truncate(message)));
    err.flush();
  }

  /**
   * The error's message, or its type when it has none: the engine reads only a JSON report with a
   * non-empty message (or an Error ID), and rejects any other as invalid.
   */
  private static String messageOf(Throwable cause) {
    String message = cause.getMessage();
    return message == null || message.isBlank() ? cause.getClass().getName() : message;
  }

  /**
   * Whether the error is an API error that names its origin, the call that failed, which the
   * engine shows with its own logs. An error of the module's own, even one that wraps such an API
   * error, is the module's to show.
   */
  static boolean hasOrigin(Throwable cause) {
    return cause instanceof DaggerQueryException
        && cause.getMessage() != null
        && ERROR_ORIGIN.matcher(cause.getMessage()).find();
  }

  /**
   * Write {@code {"message": ..., "values": {...}}} to {@code path}: the error's message in full,
   * with any origin it names, so the engine finds the call that failed, and the extensions of the
   * GraphQL error it is or wraps, as they are, such as a failed exec's exit code and output. Only
   * a report over what the engine reads is shortened. Reports whether it wrote the file.
   */
  static boolean writeFunctionError(Path path, Throwable cause, String message) {
    JsonObject values = errorValues(cause);
    String report = functionErrorReport(message, values);
    if (utf8Length(report) > FUNCTION_ERROR_FILE_LIMIT) {
      // Long strings keep their end, where output usually says what failed, and the message its
      // start and the origins it names; then the values go.
      String shortened = message;
      if (shortened.length() > SHORTENED_LENGTH) {
        StringBuilder origins = new StringBuilder();
        Matcher m = ERROR_ORIGIN.matcher(shortened.substring(cut(shortened, SHORTENED_LENGTH)));
        while (m.find()) {
          origins.append(m.group());
        }
        shortened = shortened.substring(0, cut(shortened, SHORTENED_LENGTH)) + "..." + origins;
      }
      report = functionErrorReport(shortened, (JsonObject) shortenedValue(values));
      if (utf8Length(report) > FUNCTION_ERROR_FILE_LIMIT) {
        report = functionErrorReport(shortened, JsonValue.EMPTY_JSON_OBJECT);
      }
    }
    try {
      Files.writeString(path, report, StandardCharsets.UTF_8);
      return true;
    } catch (IOException | RuntimeException e) {
      return false;
    }
  }

  /** The extensions of the GraphQL error the cause is or wraps, or none. */
  private static JsonObject errorValues(Throwable cause) {
    Set<Throwable> seen = Collections.newSetFromMap(new IdentityHashMap<>());
    for (Throwable t = cause; t != null && seen.add(t); t = t.getCause()) {
      if (t instanceof DaggerQueryException q && q.getError() != null) {
        JsonObjectBuilder values = Json.createObjectBuilder();
        q.getError().getExtensions().forEach((name, value) -> values.add(name, jsonValue(value)));
        return values.build();
      }
    }
    return JsonValue.EMPTY_JSON_OBJECT;
  }

  /** An extension value as GraphQLError keeps it, back to JSON. */
  private static JsonValue jsonValue(Object value) {
    if (value == null) {
      return JsonValue.NULL;
    } else if (value instanceof JsonValue json) {
      return json;
    } else if (value instanceof Boolean b) {
      return b ? JsonValue.TRUE : JsonValue.FALSE;
    } else {
      return Json.createValue(value.toString());
    }
  }

  private static String functionErrorReport(String message, JsonObject values) {
    return Json.createObjectBuilder()
        .add("message", message)
        .add("values", values)
        .build()
        .toString();
  }

  /** The value with each string longer than SHORTENED_LENGTH, at any depth, cut to its end. */
  private static JsonValue shortenedValue(JsonValue value) {
    switch (value.getValueType()) {
      case STRING -> {
        String s = ((JsonString) value).getString();
        if (s.length() <= SHORTENED_LENGTH) {
          return value;
        }
        int start = s.length() - SHORTENED_LENGTH;
        if (Character.isLowSurrogate(s.charAt(start))) {
          start++;
        }
        return Json.createValue("..." + s.substring(start));
      }
      case ARRAY -> {
        JsonArrayBuilder shortened = Json.createArrayBuilder();
        value.asJsonArray().forEach(item -> shortened.add(shortenedValue(item)));
        return shortened.build();
      }
      case OBJECT -> {
        JsonObjectBuilder shortened = Json.createObjectBuilder();
        value.asJsonObject().forEach((name, item) -> shortened.add(name, shortenedValue(item)));
        return shortened.build();
      }
      default -> {
        return value;
      }
    }
  }

  /** Where to cut s to keep at most n chars, without splitting a surrogate pair. */
  private static int cut(String s, int n) {
    return Character.isHighSurrogate(s.charAt(n - 1)) ? n - 1 : n;
  }

  private static int utf8Length(String s) {
    return s.getBytes(StandardCharsets.UTF_8).length;
  }

  /**
   * Print the error's stack trace through the module's own frames only, causes included, as Java
   * prints an uncaught exception, without the origins the messages name.
   */
  static void printShortStackTrace(PrintStream err, Throwable cause) {
    Set<Throwable> seen = Collections.newSetFromMap(new IdentityHashMap<>());
    String prefix = "";
    for (Throwable t = cause; t != null && seen.add(t); t = t.getCause()) {
      err.println(prefix + ERROR_ORIGIN.matcher(t.toString()).replaceAll(""));
      for (StackTraceElement frame : t.getStackTrace()) {
        if (isModuleFrame(frame)) {
          err.println("\tat " + frame);
        }
      }
      prefix = "Caused by: ";
    }
  }

  static boolean isModuleFrame(StackTraceElement frame) {
    String className = frame.getClassName();
    return FRAMEWORK_PACKAGES.stream().noneMatch(className::startsWith);
  }

  static String truncate(String message) {
    if (message.length() <= MAX_REPORTED_ERROR_LENGTH) {
      return message;
    }
    int end = MAX_REPORTED_ERROR_LENGTH;
    if (Character.isHighSurrogate(message.charAt(end - 1))) {
      end--;
    }
    return message.substring(0, end) + "...";
  }

  private static Path outputPath(String[] args) {
    Path output = null;
    for (int i = 1; i < args.length; i++) {
      if (args[i].equals("--output") && i + 1 < args.length) {
        output = Path.of(args[++i]);
      } else {
        throw new IllegalArgumentException(
            "usage: engine-call [--output PATH], got argument " + args[i]);
      }
    }
    return output;
  }

  private static JsonObject readRequest(InputStream in) {
    try (JsonParser parser = Json.createParser(in)) {
      if (!parser.hasNext() || parser.next() != JsonParser.Event.START_OBJECT) {
        throw new IllegalArgumentException("call request is not a JSON object");
      }
      JsonObject request = parser.getObject();
      boolean trailing;
      try {
        trailing = parser.hasNext();
      } catch (JsonParsingException e) {
        // Parsson reports input after the request object by throwing rather than returning true.
        trailing = true;
      }
      if (trailing) {
        throw new IllegalArgumentException("call request has trailing data");
      }
      return request;
    }
  }

  private static String requiredString(JsonObject request, String name) {
    JsonValue value = request.get(name);
    if (value == null || value.getValueType() != JsonValue.ValueType.STRING) {
      throw new IllegalArgumentException("call request is missing " + name);
    }
    return ((JsonString) value).getString();
  }

  /**
   * The JSON text of a request field. The entrypoint encodes a JSON scalar as a string holding JSON
   * text, which is decoded once; a JSON value is taken as it is. A missing field reads as null.
   */
  private static String jsonText(JsonObject request, String name) {
    JsonValue value = request.get(name);
    if (value == null) {
      return "null";
    }
    if (value.getValueType() != JsonValue.ValueType.STRING) {
      return value.toString();
    }
    String text = ((JsonString) value).getString();
    parse(text, name);
    return text;
  }

  private static JsonValue parse(String json, String name) {
    try (JsonParser parser = Json.createParser(new StringReader(json))) {
      if (!parser.hasNext()) {
        throw new IllegalArgumentException("call request field " + name + " is empty");
      }
      parser.next();
      JsonValue value = parser.getValue();
      if (parser.hasNext()) {
        throw new IllegalArgumentException("call request field " + name + " has trailing data");
      }
      return value;
    } catch (JsonException e) {
      throw new IllegalArgumentException("call request field " + name + " is not valid JSON", e);
    }
  }
}
