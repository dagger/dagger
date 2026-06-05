// The core types we publish an API reference for. Start with the set that
// already has hand-written conceptual pages under extending/types, so the
// reference lines up with the docs we have; growing this list is a one-line
// change (the renderer and cross-linking pick it up automatically).
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
