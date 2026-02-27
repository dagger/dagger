'use strict';

const fs = require('node:fs');
const fsPromises = require('node:fs/promises');
const path = require('node:path');
const { fileURLToPath } = require('node:url');
const { spawnSync } = require('node:child_process');
const Module = require('node:module');

const realpathNative = fs.realpathSync.native || fs.realpathSync;

function canonicalRoot(rawPath) {
  const resolved = path.resolve(rawPath);
  try {
    return realpathNative(resolved);
  } catch (_err) {
    return path.normalize(resolved);
  }
}

const outputDir = process.env.TRACE_OUTPUT_DIR || '/tmp/docusaurus-runtime-deps';
const outputDirPath = canonicalRoot(outputDir);
const workspaceRoot = canonicalRoot(process.env.TRACE_WORKSPACE_ROOT || '/workspace');
const siteRoot = canonicalRoot(process.env.TRACE_SITE_ROOT || process.cwd());
const hydrateHelperPath = process.env.TRACE_HYDRATE_HELPER || '/hydrate-runtime-path.cjs';
const disableHydrate = process.env.DAGGER_RUNTIME_DEPS_DISABLE_HYDRATE === '1';
const originalExistsSync = fs.existsSync.bind(fs);
const seen = new Set();
const hydrated = new Set();
const hydrateFailures = new Set();

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

function record(value) {
  const filePath = canonicalize(value);
  if (filePath == null) {
    return;
  }

  if (within(outputDirPath, filePath) == true) {
    return;
  }

  seen.add(filePath);
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
    return true;
  }
  if (hydrateFailures.has(filePath) == true) {
    return false;
  }

  const result = spawnSync(
    process.execPath,
    [hydrateHelperPath, filePath, hint],
    {
      encoding: 'utf8',
      env: {
        ...process.env,
        NODE_OPTIONS: '',
        DAGGER_RUNTIME_DEPS_HELPER: '1',
      },
    },
  );

  if (result.status === 0) {
    hydrated.add(filePath);
    return true;
  }

  hydrateFailures.add(filePath);
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

function wrapPathMethod(target, method, pathArgIndexes) {
  const original = target[method];
  if (typeof original != 'function') {
    return;
  }

  target[method] = function wrappedPathMethod(...args) {
    const hint = methodHint(method);
    for (const index of pathArgIndexes) {
      if (index < args.length) {
        const filePath = canonicalize(args[index]);
        try {
          hydrate(filePath, hint);
          record(args[index]);
        } catch (_err) {
          // Keep tracing non-intrusive.
        }
      }
    }
    return original.apply(this, args);
  };
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
        record(resolved);
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
            record(retried);
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
    hydrate(canonicalize(filename), 'file');
    record(filename);
    return originalDlopen.call(this, module, filename, ...rest);
  };
}

function flush() {
  fs.mkdirSync(outputDirPath, { recursive: true });
  const outputFile = path.join(outputDirPath, process.pid + '.json');
  const sorted = Array.from(seen).sort();
  fs.writeFileSync(outputFile, JSON.stringify(sorted), 'utf8');
}

process.on('exit', flush);
process.on('SIGINT', () => {
  flush();
  process.exit(130);
});
process.on('SIGTERM', () => {
  flush();
  process.exit(143);
});
