'use strict';

const fs = require('node:fs');
const path = require('node:path');
const realpathNative = fs.realpathSync.native || fs.realpathSync;

const FILE_EXPORT_QUERY = `
query ExportWorkspaceFile($id: WorkspaceID!, $path: String!, $out: String!) {
  loadWorkspaceFromID(id: $id) {
    file(path: $path) {
      export(path: $out, allowParentDirPath: true)
    }
  }
}
`;

const DIRECTORY_EXPORT_QUERY = `
query ExportWorkspaceDirectory($id: WorkspaceID!, $path: String!, $out: String!) {
  loadWorkspaceFromID(id: $id) {
    directory(path: $path) {
      export(path: $out)
    }
  }
}
`;

function mustEnv(name) {
  const value = process.env[name];
  if (value == null || value == '') {
    throw new Error(name + ' is required');
  }
  return value;
}

function within(parent, child) {
  return child == parent || child.startsWith(parent + path.sep);
}

function normalizePath(raw) {
  const resolved = path.resolve(raw);
  try {
    return realpathNative(resolved);
  } catch (_err) {
    return path.normalize(resolved);
  }
}

function toWorkspacePath(absPath, workspaceRoot) {
  const rel = path.relative(workspaceRoot, absPath);
  if (rel == '' || rel == '.') {
    return '.';
  }
  if (rel.startsWith('..') || path.isAbsolute(rel)) {
    throw new Error('requested path is outside workspace: ' + absPath);
  }
  return rel.split(path.sep).join('/');
}

async function graphql(query, variables) {
  const token = mustEnv('DAGGER_SESSION_TOKEN');
  const port = mustEnv('DAGGER_SESSION_PORT');
  const endpoint = 'http://127.0.0.1:' + port + '/query';
  const auth = 'Basic ' + Buffer.from(token + ':').toString('base64');

  const response = await fetch(endpoint, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      authorization: auth,
    },
    body: JSON.stringify({ query, variables }),
  });

  const body = await response.text();
  let payload = null;
  try {
    payload = JSON.parse(body);
  } catch (_err) {
    throw new Error('invalid GraphQL response: ' + body);
  }

  if (response.ok == false) {
    throw new Error('GraphQL HTTP error: ' + response.status + ' ' + body);
  }
  if (Array.isArray(payload.errors) && payload.errors.length > 0) {
    throw new Error(payload.errors.map((err) => err.message).join('; '));
  }
  return payload.data;
}

async function exportFile(workspaceID, workspacePath, absTargetPath) {
  fs.mkdirSync(path.dirname(absTargetPath), { recursive: true });
  await graphql(FILE_EXPORT_QUERY, {
    id: workspaceID,
    path: workspacePath,
    out: absTargetPath,
  });
}

async function exportDirectory(workspaceID, workspacePath, absTargetPath) {
  fs.mkdirSync(path.dirname(absTargetPath), { recursive: true });
  await graphql(DIRECTORY_EXPORT_QUERY, {
    id: workspaceID,
    path: workspacePath,
    out: absTargetPath,
  });
}

async function main() {
  const targetArg = process.argv[2];
  const hint = process.argv[3] || 'any';
  if (targetArg == null || targetArg == '') {
    throw new Error('target path argument is required');
  }

  const workspaceRoot = normalizePath(mustEnv('DAGGER_JIT_WORKSPACE_ROOT'));
  const siteRoot = normalizePath(mustEnv('DAGGER_JIT_WORKSPACE_SITE_ROOT'));
  const workspaceID = mustEnv('DAGGER_JIT_WORKSPACE_ID');
  const absTargetPath = normalizePath(targetArg);

  if (within(workspaceRoot, absTargetPath) == false) {
    process.exit(1);
  }
  if (within(siteRoot, absTargetPath) == true) {
    process.exit(1);
  }
  if (within(absTargetPath, siteRoot) == true) {
    process.exit(1);
  }
  if (fs.existsSync(absTargetPath) == true) {
    process.exit(0);
  }

  const workspacePath = toWorkspacePath(absTargetPath, workspaceRoot);
  const plan = ifHint(hint);
  let lastErr = null;

  for (const mode of plan) {
    try {
      if (mode == 'file') {
        await exportFile(workspaceID, workspacePath, absTargetPath);
      } else {
        await exportDirectory(workspaceID, workspacePath, absTargetPath);
      }

      if (fs.existsSync(absTargetPath) == true) {
        process.exit(0);
      }
    } catch (err) {
      lastErr = err;
    }
  }

  if (lastErr != null) {
    process.stderr.write(String(lastErr.message || lastErr) + '\n');
  }
  process.exit(1);
}

function ifHint(hint) {
  if (hint === 'dir') {
    return ['directory', 'file'];
  }
  if (hint === 'file') {
    return ['file', 'directory'];
  }
  return ['file', 'directory'];
}

main().catch((err) => {
  process.stderr.write(String(err.message || err) + '\n');
  process.exit(1);
});
