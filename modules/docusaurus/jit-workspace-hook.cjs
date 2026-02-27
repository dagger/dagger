'use strict';

// Node preload hook used by Sandbox.withJustInTimeWorkspace():
// - intercept fs/module path resolution
// - hydrate missing workspace paths on demand
// - persist "seen" and "hydrated" path logs for later collection

const fs = require('node:fs');
const fsPromises = require('node:fs/promises');
const path = require('node:path');
const { fileURLToPath } = require('node:url');
const { spawnSync } = require('node:child_process');
const Module = require('node:module');

const realpathNative = fs.realpathSync.native || fs.realpathSync;
const SEEN_LOG_SUFFIX = '.json';
const HYDRATED_LOG_SUFFIX = '.hydrated.json';

function canonicalRoot(rawPath) {
  const resolved = path.resolve(rawPath);
  try {
    return realpathNative(resolved);
  } catch (_err) {
    return path.normalize(resolved);
  }
}

const outputDir = process.env.DAGGER_JIT_WORKSPACE_LOG_DIR || '/tmp/docusaurus-jit-workspace';
const outputDirPath = canonicalRoot(outputDir);
const workspaceRoot = canonicalRoot(process.env.DAGGER_JIT_WORKSPACE_ROOT || '/workspace');
const siteRoot = canonicalRoot(process.env.DAGGER_JIT_WORKSPACE_SITE_ROOT || process.cwd());
const hydrateHelperPath = process.env.DAGGER_JIT_WORKSPACE_HYDRATE_HELPER || '/jit-workspace-hydrate.cjs';
const workspaceExcludes = parseExcludePatterns(process.env.DAGGER_JIT_WORKSPACE_EXCLUDES_JSON);
const disableHydrate = process.env.DAGGER_JIT_WORKSPACE_DISABLE_HYDRATE === '1';
const originalExistsSync = fs.existsSync.bind(fs);
const seen = new Set();
const hydrated = new Set();
const hydrateFailures = new Set();
const globRegexCache = new Map();
let hasFlushed = false;
const debugEnabled = process.env.DAGGER_JIT_WORKSPACE_DEBUG === '1';
const debugStartedAt = Date.now();
let hydrateAttemptCount = 0;
let hydrateSuccessCount = 0;
let hydrateFailureCount = 0;
let hydrateKnownSuccessSkips = 0;
let hydrateKnownFailureSkips = 0;
let hydrateSpawnTotalMs = 0;
let hydrateSpawnMaxMs = 0;
const slowHydrates = [];

function debugLine(label, payload) {
  if (debugEnabled == false) {
    return;
  }
  process.stderr.write('[jit-workspace-hook] ' + label + ' ' + JSON.stringify(payload) + '\n');
}

function rememberSlowHydrate(filePath, hint, elapsedMs, status) {
  if (debugEnabled == false) {
    return;
  }
  slowHydrates.push({
    elapsedMs,
    hint,
    status,
    path: filePath,
  });
  slowHydrates.sort((left, right) => right.elapsedMs - left.elapsedMs);
  if (slowHydrates.length > 8) {
    slowHydrates.length = 8;
  }
}

function toPath(value) {
  if (typeof value === 'string') {
    return value;
  }
  if (Buffer.isBuffer(value) == true) {
    return value.toString('utf8');
  }
  if (value != null && typeof value == 'object' && value.protocol == 'file:') {
    try {
      return fileURLToPath(value);
    } catch (_err) {
      return null;
    }
  }
  return null;
}

function canonicalize(value) {
  const input = toPath(value);
  if (input == null) {
    return null;
  }

  const absolute = path.isAbsolute(input) ? input : path.resolve(process.cwd(), input);
  try {
    return realpathNative(absolute);
  } catch (_err) {
    return path.normalize(absolute);
  }
}

function within(parent, child) {
  return child == parent || child.startsWith(parent + path.sep);
}

function hasPathSegment(filePath, segment) {
  return filePath.split(path.sep).includes(segment);
}

function parseExcludePatterns(raw) {
  if (raw == null || raw == '') {
    return [];
  }
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed) == false) {
      return [];
    }
    return parsed
      .filter((item) => typeof item == 'string')
      .filter((item) => item.trim() != '');
  } catch (_err) {
    return [];
  }
}

function normalizePattern(rawPattern) {
  let pattern = rawPattern.trim().replaceAll('\\', '/');
  pattern = pattern.replace(/^\.\/+/, '');
  pattern = pattern.replace(/^\/+/, '');
  // "dir/" means "everything under dir".
  if (pattern.endsWith('/')) {
    pattern = pattern + '**';
  }
  return pattern;
}

function escapeRegExpChar(value) {
  return /[\\^$*+?.()|[\]{}]/.test(value) ? '\\' + value : value;
}

function globToRegex(pattern) {
  const cached = globRegexCache.get(pattern);
  if (cached != null) {
    return cached;
  }

  // Intentionally minimal glob support:
  // - *  => segment wildcard
  // - ** => cross-segment wildcard
  let source = '^';
  for (let index = 0; index < pattern.length; index += 1) {
    const ch = pattern[index];
    if (ch == '*') {
      if (index + 1 < pattern.length && pattern[index + 1] == '*') {
        source += '.*';
        index += 1;
      } else {
        source += '[^/]*';
      }
      continue;
    }
    source += escapeRegExpChar(ch);
  }
  source += '$';

  const regex = new RegExp(source);
  globRegexCache.set(pattern, regex);
  return regex;
}

function matchesPattern(relPath, rawPattern) {
  const pattern = normalizePattern(rawPattern);
  if (pattern == '') {
    return false;
  }

  return globToRegex(pattern).test(relPath);
}

function isWorkspaceExcluded(filePath) {
  if (workspaceExcludes.length == 0) {
    return false;
  }
  if (within(workspaceRoot, filePath) == false) {
    return false;
  }

  const relPath = path.relative(workspaceRoot, filePath).replaceAll(path.sep, '/');
  if (relPath == '' || relPath == '.') {
    return false;
  }

  for (const pattern of workspaceExcludes) {
    if (matchesPattern(relPath, pattern)) {
      return true;
    }
  }
  return false;
}

function hasNodeModulesFragment(rawPath) {
  return rawPath.includes('node_modules/') || rawPath.includes('node_modules\\');
}

function shouldRecordPath(filePath) {
  if (filePath == null) {
    return false;
  }

  if (within(outputDirPath, filePath) == true) {
    return false;
  }
  if (hasPathSegment(filePath, 'node_modules') == true) {
    return false;
  }
  if (within(workspaceRoot, filePath) == false) {
    return false;
  }
  if (isWorkspaceExcluded(filePath) == true) {
    return false;
  }
  if (within(siteRoot, filePath) == true) {
    return false;
  }
  if (within(filePath, siteRoot) == true) {
    return false;
  }
  return true;
}

function recordCanonical(filePath) {
  if (shouldRecordPath(filePath) == false) {
    return;
  }
  seen.add(filePath);
}

function record(value) {
  recordCanonical(canonicalize(value));
}

function shouldHydrate(filePath) {
  if (disableHydrate == true) {
    return false;
  }
  // Node's resolver probes many non-existent node_modules paths.
  // Hydrating those is expensive and does not help external source detection.
  if (hasPathSegment(filePath, 'node_modules') == true) {
    return false;
  }
  if (within(workspaceRoot, filePath) == false) {
    return false;
  }
  if (isWorkspaceExcluded(filePath) == true) {
    return false;
  }
  if (within(siteRoot, filePath) == true) {
    return false;
  }
  if (within(filePath, siteRoot) == true) {
    return false;
  }
  if (originalExistsSync(filePath) == true) {
    return false;
  }
  return true;
}

function hydrate(filePath, hint) {
  if (filePath == null) {
    return false;
  }
  if (shouldHydrate(filePath) == false) {
    return false;
  }
  if (hydrated.has(filePath) == true) {
    hydrateKnownSuccessSkips += 1;
    return true;
  }
  if (hydrateFailures.has(filePath) == true) {
    hydrateKnownFailureSkips += 1;
    return false;
  }

  hydrateAttemptCount += 1;
  const startedAt = Date.now();
  const result = spawnSync(
    process.execPath,
    [hydrateHelperPath, filePath, hint],
    {
      encoding: 'utf8',
      env: {
        ...process.env,
        NODE_OPTIONS: '',
        DAGGER_JIT_WORKSPACE_HELPER: '1',
        DAGGER_JIT_WORKSPACE_DISABLE_HYDRATE: '1',
      },
    },
  );
  const elapsedMs = Date.now() - startedAt;
  hydrateSpawnTotalMs += elapsedMs;
  if (elapsedMs > hydrateSpawnMaxMs) {
    hydrateSpawnMaxMs = elapsedMs;
  }

  if (result.status === 0) {
    hydrated.add(filePath);
    hydrateSuccessCount += 1;
    rememberSlowHydrate(filePath, hint, elapsedMs, 'success');
    return true;
  }

  hydrateFailures.add(filePath);
  hydrateFailureCount += 1;
  rememberSlowHydrate(filePath, hint, elapsedMs, 'failure');
  return false;
}

function methodHint(method) {
  switch (method) {
    case 'readdir':
    case 'readdirSync':
    case 'opendir':
    case 'opendirSync':
      return 'dir';
    case 'readFile':
    case 'readFileSync':
    case 'createReadStream':
    case 'open':
    case 'openSync':
    case 'readlink':
    case 'readlinkSync':
      return 'file';
    default:
      return 'any';
  }
}

function copyFunctionProps(targetFn, sourceFn) {
  // Preserve attached properties (e.g. fs.realpath.native, custom symbols).
  for (const key of Reflect.ownKeys(sourceFn)) {
    if (key === 'length' || key === 'name' || key === 'prototype') {
      continue;
    }
    const descriptor = Object.getOwnPropertyDescriptor(sourceFn, key);
    if (descriptor == null) {
      continue;
    }
    try {
      Object.defineProperty(targetFn, key, descriptor);
    } catch (_err) {
      // Some engine-provided properties are non-redefinable; ignore.
    }
  }
}

function wrapPathMethod(target, method, pathArgIndexes) {
  const original = target[method];
  if (typeof original != 'function') {
    return;
  }

  function wrappedPathMethod(...args) {
    const hint = methodHint(method);
    for (const index of pathArgIndexes) {
      if (index < args.length) {
        const rawPath = toPath(args[index]);
        if (rawPath == null) {
          continue;
        }
        if (hasNodeModulesFragment(rawPath)) {
          continue;
        }
        const filePath = canonicalize(rawPath);
        try {
          hydrate(filePath, hint);
          recordCanonical(filePath);
        } catch (_err) {
          // Keep tracing non-intrusive.
        }
      }
    }
    return original.apply(this, args);
  }

  copyFunctionProps(wrappedPathMethod, original);
  target[method] = wrappedPathMethod;
}

const onePathMethods = [
  'access',
  'accessSync',
  'createReadStream',
  'exists',
  'existsSync',
  'lstat',
  'lstatSync',
  'open',
  'openSync',
  'opendir',
  'opendirSync',
  'readFile',
  'readFileSync',
  'readdir',
  'readdirSync',
  'readlink',
  'readlinkSync',
  'realpath',
  'realpathSync',
  'stat',
  'statSync',
  'watch',
  'watchFile',
];

for (const method of onePathMethods) {
  wrapPathMethod(fs, method, [0]);
}

const promiseOnePathMethods = [
  'access',
  'lstat',
  'open',
  'opendir',
  'readFile',
  'readdir',
  'readlink',
  'realpath',
  'stat',
  'watch',
];

for (const method of promiseOnePathMethods) {
  wrapPathMethod(fsPromises, method, [0]);
  wrapPathMethod(fs.promises, method, [0]);
}

const resolveFilename = Module._resolveFilename;
if (typeof resolveFilename == 'function') {
  Module._resolveFilename = function wrappedResolveFilename(...args) {
    try {
      const resolved = resolveFilename.apply(this, args);
      if (typeof resolved == 'string' && path.isAbsolute(resolved)) {
        recordCanonical(canonicalize(resolved));
      }
      return resolved;
    } catch (originalErr) {
      const request = args[0];
      const parent = args[1];
      if (typeof request == 'string') {
        let baseDir = process.cwd();
        if (parent != null && typeof parent.filename == 'string') {
          baseDir = path.dirname(parent.filename);
        }

        let candidate = null;
        if (path.isAbsolute(request)) {
          candidate = request;
        } else if (request.startsWith('./') || request.startsWith('../')) {
          candidate = path.resolve(baseDir, request);
        }

        if (candidate != null) {
          const candidates = [
            candidate,
            candidate + '.js',
            candidate + '.cjs',
            candidate + '.mjs',
            candidate + '.json',
            path.join(candidate, 'index.js'),
            path.join(candidate, 'index.cjs'),
            path.join(candidate, 'index.mjs'),
          ];
          for (const item of candidates) {
            hydrate(canonicalize(item), 'any');
          }
          const retried = resolveFilename.apply(this, args);
          if (typeof retried == 'string' && path.isAbsolute(retried)) {
            recordCanonical(canonicalize(retried));
          }
          return retried;
        }
      }
      throw originalErr;
    }
  };
}

const originalDlopen = process.dlopen;
if (typeof originalDlopen == 'function') {
  process.dlopen = function wrappedDlopen(module, filename, ...rest) {
    const filePath = canonicalize(filename);
    hydrate(filePath, 'file');
    recordCanonical(filePath);
    return originalDlopen.call(this, module, filename, ...rest);
  };
}

function flush() {
  if (hasFlushed == true) {
    return;
  }
  hasFlushed = true;

  fs.mkdirSync(outputDirPath, { recursive: true });
  const seenFile = path.join(outputDirPath, process.pid + SEEN_LOG_SUFFIX);
  const hydratedFile = path.join(outputDirPath, process.pid + HYDRATED_LOG_SUFFIX);
  fs.writeFileSync(seenFile, JSON.stringify(Array.from(seen).sort()), 'utf8');
  fs.writeFileSync(hydratedFile, JSON.stringify(Array.from(hydrated).sort()), 'utf8');

  debugLine('summary', {
    pid: process.pid,
    elapsedMs: Date.now() - debugStartedAt,
    seenCount: seen.size,
    hydratedCount: hydrated.size,
    hydrateAttemptCount,
    hydrateSuccessCount,
    hydrateFailureCount,
    hydrateKnownSuccessSkips,
    hydrateKnownFailureSkips,
    hydrateSpawnTotalMs,
    hydrateSpawnMaxMs,
  });
  if (slowHydrates.length > 0) {
    debugLine('slowHydrates', slowHydrates);
  }
}

process.once('exit', flush);
process.once('SIGINT', () => {
  flush();
  process.exit(130);
});
process.once('SIGTERM', () => {
  flush();
  process.exit(143);
});
