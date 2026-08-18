#!/usr/bin/env node
// Executes the exact Linka Looks v3.2.10 ConfigFile.ts parser/model source.
// Usage:
//   node verify-official-parser.mjs /path/to/linka.looks-electron/src/common/interfaces/ConfigFile.ts [fixture.linka]

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync, copyFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, resolve } from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const EXPECTED_BLOB = "be3443a89839a04829dd036f2cb5bf493a35e6af";
const EXPECTED_COMMIT = "b8e65af5825a5a3389e416253393c39d4d5353bd";
const parserSource = process.argv[2];
const scriptDir = dirname(fileURLToPath(import.meta.url));
const fixture = resolve(process.argv[3] ?? join(scriptDir, "testdata/backend-v2-export.linka"));

if (!parserSource) {
  console.error("usage: node verify-official-parser.mjs <Linka-Looks-v3.2.10-ConfigFile.ts> [fixture.linka]");
  process.exit(2);
}

const source = readFileSync(parserSource);
const header = Buffer.from(`blob ${source.length}\0`);
const blob = createHash("sha1").update(header).update(source).digest("hex");
if (blob !== EXPECTED_BLOB) {
  throw new Error(`unexpected ConfigFile.ts blob ${blob}; expected ${EXPECTED_BLOB} from ${EXPECTED_COMMIT}`);
}

const work = mkdtempSync(join(tmpdir(), "linka-looks-parser-"));
try {
  const localSource = join(work, "ConfigFile.ts");
  copyFileSync(parserSource, localSource);
  mkdirSync(join(work, "node_modules/uuid"), { recursive: true });
  writeFileSync(join(work, "node_modules/uuid/index.js"), "let n=0; exports.v4=()=>`official-parser-${++n}`;\n");
  writeFileSync(join(work, "node_modules/uuid/index.d.ts"), "export function v4(): string;\n");
  writeFileSync(join(work, "node_modules/uuid/package.json"), '{"name":"uuid","main":"index.js","types":"index.d.ts"}\n');

  execFileSync("tsc", [
    localSource,
    "--target", "ES2022",
    "--module", "commonjs",
    "--moduleResolution", "node",
    "--outDir", join(work, "dist"),
    "--skipLibCheck",
    "--esModuleInterop"
  ], { cwd: work, stdio: "pipe" });

  const require = createRequire(import.meta.url);
  const { normalizeConfigFile } = require(join(work, "dist/ConfigFile.js"));
  const raw = JSON.parse(execFileSync("unzip", ["-p", fixture, "config.json"], { encoding: "utf8" }));
  const entries = execFileSync("unzip", ["-Z1", fixture], { encoding: "utf8" }).trim().split(/\r?\n/).filter(Boolean);
  const opened = normalizeConfigFile(raw);
  const openedText = JSON.stringify(opened);
  const sourceIds = (raw.blocks ?? []).flatMap((block) => (block.elements ?? []).map((element) => element.id));

  console.log(JSON.stringify({
    client: {
      appVersion: "3.2.10",
      setFormatVersion: "3.0",
      sourceCommit: EXPECTED_COMMIT,
      configFileBlob: EXPECTED_BLOB
    },
    execution: "exact official src/common/interfaces/ConfigFile.ts compiled and executed locally",
    fixture: basename(fixture),
    opened: opened !== null,
    raw: {
      backendVersion: raw.metadata?.version ?? null,
      blockTypes: (raw.blocks ?? []).map((block) => block.type),
      elementOrder: sourceIds,
      archiveEntries: entries
    },
    afterOpen: {
      version: opened?.version ?? null,
      pageModes: (opened?.pages ?? []).map((page) => page.mode),
      cardTypes: (opened?.pages ?? []).flatMap((page) => page.cards.map((card) => card.cardType)),
      sourceElementIdsPresent: sourceIds.filter((id) => openedText.includes(id)),
      mediaPathsPresent: entries.filter((entry) => entry !== "config.json" && openedText.includes(entry)),
      cyrillicSamplesPresent: ["Ёжик", "Кошка", "Собака"].filter((sample) => openedText.includes(sample))
    }
  }, null, 2));
} finally {
  rmSync(work, { recursive: true, force: true });
}
