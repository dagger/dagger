// Featured core types, shown first and in this order — the set that already
// has hand-written conceptual pages under extending/types, so the reference
// leads with the types readers reach for most.
//
// This is only a prominence hint, not the full list: every other core type in
// the schema (object types implementing Node) is appended automatically and
// alphabetically by resolveTypeList in schema.js, so the reference can't omit
// a type. Reorder or extend this list to promote a type; do nothing to include
// a newly added one.
module.exports = [
  "Container",
  "Directory",
  "File",
  "Service",
  "Secret",
  "CacheVolume",
  "GitRepository",
  "Env",
  "LLM",
  "CurrentModule",
];
