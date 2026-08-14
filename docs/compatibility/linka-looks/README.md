# Linka Looks compatibility spike (N5)

See [ADR-001-linka-looks-3.2.10.md](ADR-001-linka-looks-3.2.10.md).

## 1. Reproduce the backend GET export

The regression test routes a real request through `http.ServeMux` using the production GET pattern, then executes `ContentHandler.Export` + `ContentService.Export`. It checks headers, ZIP entries, media bytes, block/element order, Cyrillic text and exact `media_url` values, and compares the result with the checked-in golden fixture:

```bash
go test ./internal/pack -run '^TestLinkaLooksCompatibilityFixture$' -count=1
```

To intentionally regenerate the golden from that HTTP handler path:

```bash
UPDATE_LINKA_LOOKS_FIXTURE=1 go test ./internal/pack \
  -run '^TestLinkaLooksCompatibilityFixture$' -count=1
```

Captured spike evidence for the endpoint-level run is in `testdata/backend-http-export-run.json`.
Because this container cannot download the full Go 1.25.7 module/toolchain set, the captured run used an isolated module with byte-identical current export core files and the exact checked-in regression test; run the normal command above in project CI as the merge gate.

## 2. Execute the exact official Linka Looks 3.2.10 parser/model source

Checkout the official client at tag `v3.2.10` (commit
`b8e65af5825a5a3389e416253393c39d4d5353bd`). Then run:

```bash
node docs/compatibility/linka-looks/verify-official-parser.mjs \
  /path/to/linka.looks-electron/src/common/interfaces/ConfigFile.ts \
  docs/compatibility/linka-looks/testdata/backend-v2-export.linka
```

The verifier refuses any `ConfigFile.ts` whose Git blob SHA is not exactly
`be3443a89839a04829dd036f2cb5bf493a35e6af`, compiles that exact TypeScript file,
and executes its exported `normalizeConfigFile` function. It requires Node, `tsc`, and `unzip`.

Captured result:
`testdata/looks-v3.2.10-official-parser-run.json`.

## 3. Reproduce save/round-trip loss observation

```bash
node docs/compatibility/linka-looks/looks-v3.2.10-harness.mjs \
  docs/compatibility/linka-looks/testdata/backend-v2-export.linka
```

Captured result: `testdata/looks-v3.2.10-run.json`.

For a full local check (backend test + exact official parser + round-trip observation):

```bash
docs/compatibility/linka-looks/run-checks.sh \
  /path/to/linka.looks-electron/src/common/interfaces/ConfigFile.ts
```

## Follow-up implementation issue and N5 closure

The complete issue body is in [FOLLOW-UP-ISSUE.md](FOLLOW-UP-ISSUE.md). The connected ChatGPT GitHub
integration returned HTTP 403 `Resource not accessible by integration` for both issue creation and
comments, so it cannot perform the repository write itself.

From any developer environment authenticated with GitHub Issues write access, the remaining external
closure action is one command:

```bash
docs/compatibility/linka-looks/publish-follow-up-and-close-n5.sh
```

The script is idempotent with respect to the follow-up title: it reuses an existing matching issue,
otherwise creates it from `FOLLOW-UP-ISSUE.md`, best-effort applies `archive`/`pack` labels and the
assignee, comments the resulting issue number on #110, and closes #110 as completed.
