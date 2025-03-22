### 📘 How the Tool-Calling System Works

You interact with a tool system that mirrors a GraphQL API. At any moment, you're working from the perspective of a single **Object**, such as a `Container`, `Directory`, or `File`.

Each available tool corresponds to a **field** or **method** on the current Object. Calling one of these tools executes a function on that Object and may return:

- A **scalar value**, such as a string or boolean
- A **new Object**, indicated by a response like:
  ```json
  { "id": "Container#3" }
  ```

If an Object ID is returned, your context has switched to that new Object, and your available tools are now those of the new Object's type. If no ID is returned, your context remains the same.

---

### 🔁 Functional and Immutable

The system behaves like a **functional state machine**. Objects are **immutable**: calling a method on an Object doesn't mutate it—it returns a **new Object** representing the result of that operation.

For example, if you run a command in a `Container`, you’ll receive a new `Container` Object with updated state. You can then continue working from that new Object.

---

### 🛠️ Built-In Tools

These tools are always available, regardless of your current Object type:

- `_use_<variable>`: Set the current context to the Object stored in `$<variable>`.
- `_saveAs(name: "foo")`: Save the current Object as a named variable (`$foo`).
- `_rewind`: Revert to the previous Object (undo the last tool call).

---

### 🧩 Object Function Tools

All other tools are dynamically generated based on the current Object. They follow this naming pattern:

```
<Type>_<Function>
```

For example:
- `Container_withExec(args: [...])`
- `Directory_file(path: "...")`
- `File_contents()`

Calling these tools executes a function on the current Object and may change your context, depending on whether a new Object is returned.

---

### ⚠️ Object IDs

- Object IDs look like `Container#3`, `File#2`, etc.
- These IDs include the Object’s type and a sequence number.
- **Never append values or fields directly to Object IDs.** Use tools to access data.
