'use strict';

// Collect JIT workspace logs emitted by jit-workspace-hook.cjs.
// Modes:
// - hydrated: only paths that were actually materialized on demand
// - seen: all traced accesses (excluding hydrated files)
// - all: both sets

const fs = require('node:fs');
const path = require('node:path');

const realpathNative = fs.realpathSync.native || fs.realpathSync;

function mustEnv(name) {
  const value = process.env[name];
  if (value == null || value == '') {
    throw new Error(name + ' is required');
  }
  return value;
}

function canonicalize(rawPath) {
  const resolved = path.resolve(rawPath);
  try {
    return realpathNative(resolved);
  } catch (_err) {
    return path.normalize(resolved);
  }
}

function within(parent, child) {
  return child == parent || child.startsWith(parent + path.sep);
}

const outputDir = canonicalize(mustEnv('DAGGER_JIT_WORKSPACE_LOG_DIR'));
const workspaceDir = canonicalize(mustEnv('DAGGER_JIT_WORKSPACE_ROOT'));
const siteDir = canonicalize(mustEnv('DAGGER_JIT_WORKSPACE_SITE_ROOT'));
const collectMode = (process.env.DAGGER_JIT_WORKSPACE_COLLECT_MODE || 'hydrated').toLowerCase();
const debugEnabled = process.env.DAGGER_JIT_WORKSPACE_DEBUG === '1';
const startedAt = Date.now();
const collected = new Set();
let filesScanned = 0;
let rawPathsScanned = 0;
let pathsCollected = 0;

function debugLine(label, payload) {
  if (debugEnabled == false) {
    return;
  }
  process.stderr.write('[jit-workspace-log] ' + label + ' ' + JSON.stringify(payload) + '\n');
}

function shouldReadEntry(entry) {
  if (entry.endsWith('.json') == false) {
    return false;
  }

  if (collectMode == 'seen') {
    return entry.endsWith('.hydrated.json') == false;
  }
  if (collectMode == 'all') {
    return true;
  }
  return entry.endsWith('.hydrated.json');
}

function loadPathList(filePath) {
  try {
    const raw = fs.readFileSync(filePath, 'utf8');
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed) == false) {
      return [];
    }
    return parsed.filter((item) => typeof item == 'string');
  } catch (_err) {
    return [];
  }
}

function shouldCollectPath(absPath) {
  if (fs.existsSync(absPath) == false) {
    return false;
  }
  if (within(workspaceDir, absPath) == false) {
    return false;
  }
  if (within(siteDir, absPath) == true) {
    return false;
  }
  if (within(absPath, siteDir) == true) {
    return false;
  }
  return true;
}

if (fs.existsSync(outputDir)) {
  for (const entry of fs.readdirSync(outputDir)) {
    filesScanned += 1;
    if (shouldReadEntry(entry) == false) {
      continue;
    }

    const filePath = path.join(outputDir, entry);
    const paths = loadPathList(filePath);
    for (const item of paths) {
      rawPathsScanned += 1;
      const absPath = canonicalize(item);
      if (shouldCollectPath(absPath) == false) {
        continue;
      }

      const relPath = path.relative(siteDir, absPath);
      if (relPath == '') {
        continue;
      }
      collected.add(relPath);
      pathsCollected += 1;
    }
  }
}

debugLine('summary', {
  collectMode,
  elapsedMs: Date.now() - startedAt,
  filesScanned,
  rawPathsScanned,
  pathsCollected,
  uniqueCollected: collected.size,
});

process.stdout.write(JSON.stringify(Array.from(collected).sort()));
