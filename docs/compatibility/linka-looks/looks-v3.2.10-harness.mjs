#!/usr/bin/env node
// N5 execution harness for Linka Looks v3.2.10 (commit b8e65af5825a5a3389e416253393c39d4d5353bd).
// It reproduces the relevant runtime path from:
//   src/electron/services/card-storage-service.ts:getConfigFile/saveSet/cleanFile
//   src/common/interfaces/ConfigFile.ts:normalizeConfigFile/normalizeLegacyPages/normalizePage
// The UUID generator is deterministic here so reports are diffable; it does not affect format compatibility.

import { execFileSync } from "node:child_process";
import { basename } from "node:path";

const archive = process.argv[2];
if (!archive) {
  console.error("usage: node looks-v3.2.10-harness.mjs <fixture.linka>");
  process.exit(2);
}

const CardType = { AudioCard: 0, SpaceCard: 1, EmptyCard: 2, NewCard: 3 };
const CURRENT_SET_VERSION = "3.0";
const DEFAULT_COLUMNS = 3;
const DEFAULT_ROWS = 3;
let nextId = 0;
const uuid = () => `harness-${++nextId}`;

function clampPageDimension(value, fallback = DEFAULT_COLUMNS) {
  if (!value || Number.isNaN(value)) return fallback;
  return Math.max(1, Math.floor(value));
}
function clampCardSpan(value, fallback = 1) {
  if (!value || Number.isNaN(value)) return fallback;
  return Math.max(1, Math.floor(value));
}
function isPageMode(value) {
  return value === "standard" || value === "quiz" || value === "match";
}
function getMatchTopColumns(page, fallback) {
  const columns = clampPageDimension(page.columns, clampPageDimension(fallback?.columns, DEFAULT_COLUMNS));
  return clampPageDimension(page.topColumns, clampPageDimension(fallback?.topColumns, columns));
}
function getMatchBottomColumns(page, fallback) {
  const columns = clampPageDimension(page.columns, clampPageDimension(fallback?.columns, DEFAULT_COLUMNS));
  return clampPageDimension(page.bottomColumns, clampPageDimension(fallback?.bottomColumns, columns));
}
function getMatchLane(index, columns) { return index < columns ? "top" : "bottom"; }
function createPlaceholderCard(cardType = CardType.NewCard) { return { id: uuid(), cardType }; }
function cloneCard(card) { return JSON.parse(JSON.stringify(card)); }
function normalizeCard(card, mode, columns, rows, topColumns, index) {
  const normalized = { ...card, id: card?.id ?? uuid(), cardType: card?.cardType ?? CardType.NewCard };
  if (mode === "standard" || mode === "quiz") {
    const width = Math.min(clampCardSpan(card?.width), columns);
    const height = Math.min(clampCardSpan(card?.height), rows);
    if (width > 1) normalized.width = width; else delete normalized.width;
    if (height > 1) normalized.height = height; else delete normalized.height;
  } else {
    delete normalized.width;
    delete normalized.height;
  }
  if (mode === "match") normalized.matchLane = getMatchLane(index, topColumns);
  else { delete normalized.matchLane; delete normalized.matchId; }
  if (mode !== "quiz") delete normalized.answer;
  return normalized;
}
function normalizePage(page, fallback) {
  const mode = isPageMode(page.mode) ? page.mode : (isPageMode(fallback?.mode) ? fallback.mode : "standard");
  const rowsFallback = mode === "match" ? 2 : DEFAULT_ROWS;
  const columns = clampPageDimension(page.columns, clampPageDimension(fallback?.columns, DEFAULT_COLUMNS));
  const rows = mode === "match" ? 2 : clampPageDimension(page.rows, clampPageDimension(fallback?.rows, rowsFallback));
  const topColumns = mode === "match" ? getMatchTopColumns(page, fallback) : undefined;
  const bottomColumns = mode === "match" ? getMatchBottomColumns(page, fallback) : undefined;
  const size = mode === "match" ? (topColumns ?? DEFAULT_COLUMNS) + (bottomColumns ?? DEFAULT_COLUMNS) : Math.max(1, rows * columns);
  const sourceCards = (page.cards ?? []).filter(Boolean);
  const cards = (mode === "match" ? sourceCards : sourceCards.slice(0, size))
    .map((card, index) => normalizeCard(card, mode, columns, rows, topColumns ?? columns, index));
  while (cards.length < size) cards.push(normalizeCard(createPlaceholderCard(), mode, columns, rows, topColumns ?? columns, cards.length));
  return { id: page.id ?? fallback?.id ?? uuid(), mode, columns, rows, cards,
    ...(topColumns !== undefined ? { topColumns } : {}),
    ...(bottomColumns !== undefined ? { bottomColumns } : {}),
    ...(mode === "quiz" ? { question: page.question ?? fallback?.question ?? "" } : {}) };
}
function normalizeLegacyPages(config, columns, rows) {
  const cards = (config.cards ?? []).filter(Boolean).map(cloneCard);
  const questions = config.questions ?? [];
  const pageSize = Math.max(1, columns * rows);
  const pageCount = Math.max(1, Math.ceil(cards.length / pageSize), config.quiz ? questions.length : 0);
  const mode = config.quiz ? "quiz" : "standard";
  const pages = [];
  for (let pageIndex = 0; pageIndex < pageCount; pageIndex++) {
    pages.push(normalizePage({
      id: uuid(), mode, columns, rows,
      cards: cards.slice(pageIndex * pageSize, (pageIndex + 1) * pageSize),
      ...(mode === "quiz" ? { question: questions[pageIndex] ?? "" } : {})
    }));
  }
  return pages;
}
function normalizeConfigFile(config) {
  if (!config) return null;
  const baseColumns = clampPageDimension(config.columns, DEFAULT_COLUMNS);
  const baseRows = clampPageDimension(config.rows, DEFAULT_ROWS);
  const pages = config.pages?.length
    ? config.pages.map((page) => normalizePage(page, { columns: baseColumns, rows: baseRows }))
    : normalizeLegacyPages(config, baseColumns, baseRows);
  return {
    version: CURRENT_SET_VERSION,
    withoutSpace: !!config.withoutSpace,
    directSet: !!config.directSet,
    quizAutoNext: config.quizAutoNext ?? true,
    quizReadQuestion: config.quizReadQuestion ?? false,
    ...(config.description !== undefined ? { description: config.description } : {}),
    pages
  };
}

// Actual Linka Looks getConfigFile() does AdmZip.readAsText("config.json") + JSON.parse + normalizeConfigFile.
const rawText = execFileSync("unzip", ["-p", archive, "config.json"], { encoding: "utf8" });
const raw = JSON.parse(rawText);
const entries = execFileSync("unzip", ["-Z1", archive], { encoding: "utf8" }).trim().split(/\r?\n/).filter(Boolean);
const opened = normalizeConfigFile(raw);

// saveSet() normalizes again, cleanFile() keeps only AudioCard imagePath/audioPath entries,
// then NewCard placeholders are written as EmptyCard before config.json is replaced.
const normalizedForSave = normalizeConfigFile(opened);
const mediaReferences = [];
for (const page of normalizedForSave.pages ?? []) {
  for (const card of (page.cards ?? []).filter(Boolean)) {
    if (card.cardType === CardType.AudioCard) {
      if (card.audioPath) mediaReferences.push(card.audioPath);
      if (card.imagePath) mediaReferences.push(card.imagePath);
    }
  }
}
const saved = JSON.parse(JSON.stringify(normalizedForSave));
for (const page of saved.pages ?? []) {
  page.cards = (page.cards ?? []).map((card) => card.cardType === CardType.NewCard
    ? { id: uuid(), cardType: CardType.EmptyCard, ...(card.matchLane ? { matchLane: card.matchLane } : {}) }
    : card);
}
// Идентификаторы берём из пришедшей схемы: Linka Config 2.0 описывает
// blocks[].elements[], конвертированный looks-3 — pages[].cards[].
const sourceElementOrder = raw.blocks
  ? raw.blocks.flatMap((b) => (b.elements ?? []).map((e) => e.id))
  : (raw.pages ?? []).flatMap((p) => (p.cards ?? []).map((c) => c.id));
const savedText = JSON.stringify(saved, null, 2);
const cyrillicSamples = ["Ёжик", "Кошка", "Собака"];
const report = {
  client: {
    appVersion: "3.2.10",
    setFormatVersion: CURRENT_SET_VERSION,
    sourceCommit: "b8e65af5825a5a3389e416253393c39d4d5353bd",
    configFileBlob: "be3443a89839a04829dd036f2cb5bf493a35e6af"
  },
  fixture: basename(archive),
  opened: opened !== null,
  raw: {
    backendVersion: raw.metadata?.version ?? null,
    blockTypes: (raw.blocks ?? []).map((b) => b.type),
    elementOrder: sourceElementOrder,
    archiveEntries: entries
  },
  afterOpen: {
    version: opened?.version ?? null,
    pageModes: (opened?.pages ?? []).map((p) => p.mode),
    cardTypes: (opened?.pages ?? []).flatMap((p) => p.cards.map((c) => c.cardType)),
    sourceElementIdsPresent: sourceElementOrder.filter((id) => JSON.stringify(opened).includes(id)),
    mediaPathsPresent: entries.filter((e) => e !== "config.json" && JSON.stringify(opened).includes(e)),
    cyrillicSamplesPresent: cyrillicSamples.filter((s) => JSON.stringify(opened).includes(s))
  },
  roundTrip: {
    retainedMediaEntries: entries.filter((e) => e !== "config.json" && mediaReferences.includes(e)),
    droppedMediaEntries: entries.filter((e) => e !== "config.json" && !mediaReferences.includes(e)),
    sourceElementIdsPresent: sourceElementOrder.filter((id) => savedText.includes(id)),
    cyrillicSamplesPresent: cyrillicSamples.filter((s) => savedText.includes(s)),
    pageModes: (saved.pages ?? []).map((p) => p.mode),
    cardTypes: (saved.pages ?? []).flatMap((p) => p.cards.map((c) => c.cardType))
  }
};
console.log(JSON.stringify(report, null, 2));
