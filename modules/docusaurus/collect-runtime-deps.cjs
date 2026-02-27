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

function envWithFallback(primary, secondary) {
  const value = process.env[primary];
  if (value != null && value != '') {
    return value;
  }
  return mustEnv(secondary);
}

const outputDir = canonicalize(mustEnv('TRACE_OUTPUT_DIR'));
const workspaceDir = canonicalize(envWithFallback('TRACE_WORKSPACE_ROOT', 'TRACE_WORKSPACE_DIR'));
const siteDir = canonicalize(envWithFallback('TRACE_SITE_ROOT', 'TRACE_SITE_DIR'));
const collectMode = (process.env.TRACE_COLLECT_MODE || 'hydrated').toLowerCase();
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
