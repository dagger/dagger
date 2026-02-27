'use strict';

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
const collected = new Set();

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

if (fs.existsSync(outputDir)) {
  for (const entry of fs.readdirSync(outputDir)) {
    if (shouldReadEntry(entry) == false) {
      continue;
    }

    const filePath = path.join(outputDir, entry);
    const raw = fs.readFileSync(filePath, 'utf8');
    let paths = null;

    try {
      paths = JSON.parse(raw);
    } catch (_err) {
      continue;
    }

    if (Array.isArray(paths) == false) {
      continue;
    }

    for (const item of paths) {
      if (typeof item != 'string') {
        continue;
      }

      const absPath = canonicalize(item);
      if (fs.existsSync(absPath) == false) {
        continue;
      }
      if (within(workspaceDir, absPath) == false) {
        continue;
      }
      if (within(siteDir, absPath) == true) {
        continue;
      }
      if (within(absPath, siteDir) == true) {
        continue;
      }

      const relPath = path.relative(siteDir, absPath);
      if (relPath == '') {
        continue;
      }
      collected.add(relPath);
    }
  }
}

process.stdout.write(JSON.stringify(Array.from(collected).sort()));
