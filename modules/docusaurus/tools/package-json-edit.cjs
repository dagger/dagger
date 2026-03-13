'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { applyEdits, modify } = require('jsonc-parser');

function usage() {
  process.stderr.write(
    'usage: node package-json-edit.cjs <package-json-path> <include-json-array>\n',
  );
  process.exit(2);
}

function parseIncludes(raw) {
  let parsed = null;
  try {
    parsed = JSON.parse(raw);
  } catch (_err) {
    throw new Error('include-json-array must be valid JSON');
  }

  if (Array.isArray(parsed) == false) {
    throw new Error('include-json-array must be a JSON array');
  }

  const includes = [];
  for (const item of parsed) {
    if (typeof item != 'string') {
      throw new Error('include-json-array must contain only strings');
    }
    includes.push(item);
  }
  return includes;
}

function gcd(a, b) {
  let x = Math.abs(a);
  let y = Math.abs(b);
  while (y != 0) {
    const t = y;
    y = x % y;
    x = t;
  }
  return x || 1;
}

function detectIndent(text) {
  const lines = text.split(/\r?\n/);
  let sawTabs = false;
  const spaceIndents = [];

  for (const line of lines) {
    if (line.trim() == '') {
      continue;
    }
    const match = line.match(/^([ \t]+)\S/);
    if (match == null) {
      continue;
    }
    const indent = match[1];
    if (indent.includes('\t')) {
      sawTabs = true;
      break;
    }
    spaceIndents.push(indent.length);
  }

  if (sawTabs == true) {
    return { insertSpaces: false, tabSize: 2 };
  }

  if (spaceIndents.length == 0) {
    return { insertSpaces: true, tabSize: 2 };
  }

  let tabSize = spaceIndents[0];
  for (const width of spaceIndents.slice(1)) {
    tabSize = gcd(tabSize, width);
  }
  if (tabSize < 1 || tabSize > 8) {
    tabSize = 2;
  }

  return { insertSpaces: true, tabSize: tabSize };
}

function detectEOL(text) {
  return text.includes('\r\n') ? '\r\n' : '\n';
}

function main() {
  const filePathArg = process.argv[2];
  const includesArg = process.argv[3];
  if (filePathArg == null || includesArg == null) {
    usage();
  }

  const filePath = path.resolve(filePathArg);
  const includes = parseIncludes(includesArg);
  const original = fs.readFileSync(filePath, 'utf8');
  const formatting = detectIndent(original);
  const edits = modify(
    original,
    ['docusaurus', 'include'],
    includes,
    {
      formattingOptions: {
        insertSpaces: formatting.insertSpaces,
        tabSize: formatting.tabSize,
        eol: detectEOL(original),
      },
    },
  );

  if (edits.length == 0) {
    return;
  }

  const updated = applyEdits(original, edits);
  if (updated == original) {
    return;
  }
  fs.writeFileSync(filePath, updated, 'utf8');
}

try {
  main();
} catch (err) {
  process.stderr.write(String(err.message || err) + '\n');
  process.exit(1);
}
