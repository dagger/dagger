// Featured core types, shown in the Types Reference sidebar and first in
// generated type lists, so the reference leads with the types readers reach
// for most.
//
// This is only a prominence hint, not the full list: every other core type in
// the schema (object types implementing Node) is appended automatically and
// alphabetically by resolveTypeList in schema.js, so the reference can't omit
// a type. Reorder or extend this list to promote a type into the sidebar; do
// nothing to include a newly added one on the All types page and as a published
// reference page.
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
