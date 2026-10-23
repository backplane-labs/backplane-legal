# backplane-legal

Legal documents and acceptance evidence for a [Backplane](https://github.com/backplane-labs/backplane) installation. Operators maintain translated drafts and publish immutable versions; callers record which subject accepted which exact text. The service doesn't manage identities or decide where an application must stop until a user accepts.

PostgreSQL holds the documents, journal and transactional event outbox in the `legal` schema. The public HTTP listener serves published texts without authentication. Activities and the console API stay on Backplane's authenticated internal transport; there is no public acceptance endpoint.

## Documents and versions

A document has a stable slug matching `^[a-z][a-z0-9-]*$`, an audience (`all` or `registration`), a `required` flag and a default BCP 47 language. Its policy stays fixed after creation. Titles belong to individual language texts, not to the document itself.

Save a draft with `number: 0` to allocate the next version. Each version holds an effective time, change note and texts containing `lang`, `title`, Markdown `body` and an optional absolute HTTP(S) `url`. Supply a body or URL; a URL supplements the stored text, but the service neither downloads nor archives its destination. Publication freezes the version and every translation, including its content hash. Correct a publication by publishing another version.

The current version is the highest-numbered publication whose `effective_at` has arrived. Publishing a future version doesn't immediately replace the current one. Language selection tries the requested tag, then the document's default, then the first tag in lexical order; matching ignores case but doesn't shorten `pt-BR` to `pt`.

`Pending` includes both required documents and optional consent choices. An accepted current version disappears; a withdrawal makes an optional consent pending again. `previous_accepted` tells the caller that the subject accepted an older version. `Pending` with audience `registration` includes both `all` and `registration`, so sign-up can present every applicable choice. Audience `all` includes only installation-wide documents. `ListDocuments` and the public list use exact audience filtering; omitting their filter selects both.

## Activities

Inputs use JSON field names. `subject` is an opaque identifier chosen by the caller, not an identity lookup. Invalid requests and domain conflicts are permanent activity failures, not a reason to retry unchanged input.

| Activity | Input and behavior |
| --- | --- |
| `legal.Pending` | `{subject, audience?, lang?}`. Current documents the subject hasn't accepted, including selected text, policy, languages, change note and `previous_accepted`. Check at sign-in or another application boundary, not on every request. |
| `legal.Accept` | `{subject, items: [{document, version, lang}], context?}`. Atomically accepts every supplied item; requires the exact current effective publication and an available language. An already-accepted `(subject, document, version)` returns its existing record without appending or emitting another event. |
| `legal.Withdraw` | `{subject, document, context?}`. Appends a withdrawal for an optional consent; rejects required documents and a subject with no acceptance. Repeating a withdrawal returns the existing withdrawal. A later acceptance appends new evidence. |
| `legal.GetDocument` | `{slug, version?, lang?}`. Selects a published, effective text; without a version, selects the current one. Historical effective publications remain readable. |
| `legal.ListDocuments` | `{audience?, lang?}`. Current effective publications; documents without one don't appear. |
| `legal.Acceptances` | `{subject}`. The subject's journal, including withdrawals. This isn't a filtered view of only active consent. |
| `legal.Verify` | `{from?, to?}`. Verifies sequence continuity, record hashes and the durable head, returning `{ok, broken_seq, checked}`. |

`Accept` validates the items it receives, not their completeness against required policy. An empty list fails, but omitting one required document from a nonempty list doesn't fail. Keep registration policy separate from recording evidence.

Context holds caller-supplied string pairs such as `channel`, `ip`, `device` and `user_agent`. Backplane call metadata can carry these across a hook execution; only trusted internal callers should set identity and request provenance. Don't put passwords, tokens or unnecessary personal data in journal context: records cannot later redact individual values.

## Events and TypeScript

| Event | Payload |
| --- | --- |
| `legal.Published` | `document`, `version`, RFC 3339 `effective_at`, `required`, `audience`. Emitted at publication, even if the effective time lies in the future; consumers must respect that time. |
| `legal.Accepted` | `subject`, `document`, `version`, `lang`, `seq`. One newly appended acceptance. |
| `legal.Withdrawn` | `subject`, `document`, `version`, `lang`, `seq`. One newly appended withdrawal. |

Contracts live in `proto/legal/events/v1/events.proto`. The Go package is `github.com/backplane-labs/backplane-legal/proto/legal/events/v1`; protobuf-es v2 consumers install `@backplane-labs/legal-events` from GitHub Packages and import schemas from:

```ts
import { AcceptedSchema, PublishedSchema, WithdrawnSchema } from
  '@backplane-labs/legal-events/legal/events/v1/events_pb.js';
```

`make gen` generates `events/src/legal/events/v1/events_pb.ts`; `make events` compiles the package. Release tags publish its matching version. Events use protojson with proto field names; protobuf 64-bit integers use JSON strings, so decode with the generated schemas rather than assuming JavaScript numbers. Outbox delivery is at least once. Deduplicate acceptance and withdrawal notifications by journal `seq` and event identity, and don't treat `Published` as a timer firing at `effective_at`.

## Public HTTP

| Route | Response |
| --- | --- |
| `GET /legal/v1/documents?audience=&lang=` | `{documents: [...]}` with current effective texts, document policy, version, language, `content_hash`, available `languages` and `text_url`. |
| `GET /legal/v1/documents/{slug}?version=&lang=` | One publication's text and metadata. `version` must be a positive integer when supplied. |
| `GET /legal/{slug}?version=&lang=` | Standalone HTML document with server-rendered, sanitized Markdown, an escaped title and optional external link. Supports light/dark color schemes and sets the selected language on `<html lang>`. |

All routes also support `HEAD`. Successful representations carry a SHA-256 ETag and `Cache-Control: public, max-age=0, must-revalidate`; matching `If-None-Match` returns `304`. Revalidation avoids retaining an old current version after a scheduled publication takes effect. Bad parameters return `400`; unavailable texts return `404`; a store failure returns `503`. The service never exposes drafts through these routes.

The HTML response disallows scripts, forms and remote resources through Content Security Policy. `http.frame_ancestors` controls embedding and defaults to `["*"]`; restrict it to the installation's origins when arbitrary framing isn't appropriate. Supported entries are `*`, `'self'`, `'none'` or absolute HTTP(S) origins. `'none'` must stand alone.

## Console and internal API

The console module lets operators create document policy, edit drafts by language, preview Markdown with the server's renderer, publish with confirmation and inspect publication hashes and version history. It also provides journal filters, CSV/JSON downloads and chain verification. The internal gRPC contract is `legal.console.v1.AdminService` in `proto/legal/console/v1/admin.proto`.

| RPCs | Purpose |
| --- | --- |
| `ListDocuments`, `CreateDocument` | Document policy, including documents without a publication. |
| `GetVersions`, `SaveDraft`, `Publish`, `Preview` | Draft lifecycle, publication history and sanitized preview. |
| `Journal`, `Export` | Filter by subject, document, version and inclusive server-time bounds `from`/`to` in RFC 3339. Export returns bytes, content type and filename through the authenticated console relay. |
| `Verify` | `{from?, to?}` sequence bounds; returns `{ok, broken_seq, checked}`. Zero bounds select the beginning/end. |

Operator changes produce application audit records: `legal.document.created`, `legal.draft.saved`, `legal.version.published`. Acceptance evidence stays in the separate append-only journal.

## Evidence model

Every record contains an ID, contiguous sequence, subject, document, version, language, exact content hash, `kind` (`accepted` or `withdrawn`), server timestamp, context and its chain hashes. The database locks a singleton chain-head row with `SELECT ... FOR UPDATE` before appending; the record, head update and outbox entry commit together. Database triggers reject journal updates, deletions and truncation, and reject changes to published versions.

This proves consistency of the retained journal and ties a recorded assertion to a specific stored text. It doesn't prove that a person read the text, that an IP belongs to that person, or that a caller-supplied subject was authenticated correctly. A database superuser can disable triggers and rewrite the entire chain together with its head. Keep protected backups or externally retained checkpoints if the threat model includes a privileged database operator; the chain alone isn't a signature, trusted timestamp or external notarization.

### Canonical encodings

`internal/domain/rules.go` defines the exact hash bytes. Don't hash the pretty-printed export or PostgreSQL's JSONB representation.

Content hashing uses compact UTF-8 JSON with these lexically ordered keys:

```json
{"body":"...","lang":"en","slug":"privacy","title":"Privacy","url":"","version":1}
```

The hash is lowercase hexadecimal SHA-256 of those bytes. Use Go `encoding/json` string escaping, including HTML-sensitive characters and U+2028/U+2029; version numbers are JSON integers, empty strings remain present, and there is no whitespace or trailing newline. No Unicode normalization changes the text before hashing.

Record hashing uses the same compact `encoding/json` map encoding, with keys `accepted_at`, `content_hash`, `context`, `document`, `id`, `kind`, `lang`, `prev_hash`, `seq`, `subject`, `version`. Context keys sort lexically; absent context becomes `{}`. `accepted_at` uses UTC `time.RFC3339Nano`, with server timestamps truncated to microseconds before persistence. Sequences and versions remain JSON integers.

```text
record.hash = hex(SHA256(UTF8(record.prev_hash) || canonical_record_json))
```

The prefix contains the previous hash's lowercase hexadecimal characters, not decoded digest bytes. `prev_hash` also appears inside the canonical JSON. The first record uses an empty previous hash; the record's own `hash` never enters its digest.

### Verify and export

Run `Verify` against the live journal before exporting evidence. Verification checks record hashes, links and contiguous sequences; a full-chain check also compares the durable head to detect a missing final record. A range check still validates the preceding chain to establish its starting link. The first mismatch reports `broken_seq`.

CSV and JSON exports include every evidence field and both hashes. A filtered export naturally contains gaps from unrelated subjects or documents and cannot independently prove whole-chain completeness. Retain the relevant published text and, for independent chain checking, the full journal and trusted head/checkpoint. CSV preserves raw strings rather than adding spreadsheet prefixes; import it as data with formula evaluation disabled, not by opening untrusted cells as formulas.

## Registration wiring: limits and examples

`deployments/wiring/legal-kratos.yaml` contains commented examples, not a ready-made registration policy. Review the Kratos adapter's [hook contract](https://github.com/backplane-labs/backplane-kratos#example-wiring) before publishing bindings individually through Wiring.

`kratos.BeforeRegistration` runs while Kratos initializes a flow, before the user submits acceptance choices. Its `FlowIn` can carry flow metadata, but no persisted identity or completed submission. The only honest before-hook example without another policy capability is fail-closed: deny every registration until the installation supplies proper validation. The example file includes that disabled binding; don't enable it expecting consent checkboxes to unblock it.

`kratos.AfterRegistration` exposes `req.identity.id` and `req.transient_payload`, but Kratos hasn't stored the identity yet. A partial binding can record supplied choices:

```yaml
hook: kratos.AfterRegistration
steps:
  accepted:
    activity: legal.Accept
    input:
      subject: req.identity.id
      items: req.transient_payload.legal_acceptances
      context:
        channel: "'registration'"
result: {}
```

The caller must send `transient_payload.legal_acceptances` as `[{document, version, lang}]`, with integer versions. Don't enable this in isolation as a required-consent guard: it doesn't prove that the list covers every required document in both audiences. It can also leave evidence for an identity whose registration later fails. Hook errors stop the flow by default; adding registration to the adapter's `fail_open` setting would weaken that protection.

A complete integration needs validation at a submitted-data hook, including required-current-version coverage, and an explicit decision about when identity persistence and evidence recording occur. The current legal activities don't expose a validation-only operation. `kratos.Registered` describes completed registration, but its event contract doesn't carry the transient acceptance list, so replacing the hook with that event doesn't solve the evidence handoff. No unsupported hook or synthetic activity appears in the example.

## Configuration

The service-specific configuration contains the database connection, public listener, embedding policy and document language default. A local example is `deployments/dev.yaml`:

```yaml
postgres:
  dsn: postgres://legal:legal@localhost:5457/legal?sslmode=disable
http:
  listen: ':8085'
  frame_ancestors: ['*']
default_lang: en
```

| Field | Environment | Purpose |
| --- | --- | --- |
| `postgres.dsn` | `LEGAL_POSTGRES_DSN` | PostgreSQL DSN. Use a dedicated database; startup applies migrations in the `legal` schema. The section uses the SDK's `postgres.Config`, including `query_log` and `pool` settings. |
| `http.listen` | `LEGAL_HTTP_LISTEN` | Listen address for read-only document JSON and HTML; default `:8085`. Keep the authenticated internal API on its separate listener. |
| `http.frame_ancestors` | `LEGAL_HTTP_FRAME_ANCESTORS` | Live allowed embedding origins; default `["*"]`. Restrict this for deployments that shouldn't allow arbitrary framing. |
| `default_lang` | `LEGAL_DEFAULT_LANG` | Default BCP 47 language for newly created documents that omit their own language; default `en`. Existing documents keep their stored default. |

Set `BACKPLANE_CONFIG_FILE` to load a YAML file. Backplane's own settings use `BACKPLANE_*`, including `BACKPLANE_INTERNAL_SECRET`, discovery and event transport addresses; supply those for the target installation. Don't expose the development database password or internal secret on an untrusted network.

## Transaction cancellation limitation

The SDK's current `pgtx` / `go-transaction-manager` dependency can leave a PostgreSQL pool connection acquired when a transaction's context is cancelled. A cancelled operation can therefore exhaust pool capacity over time or block graceful pool shutdown. Transactional rollback and ordinary SQL-error paths are tested, but they do not establish cancellation-safe resource cleanup. This needs an upstream dependency fix; this service does not replace the SDK's transaction manager or add a custom rollback loop. The lock-contention integration test uses PostgreSQL `lock_timeout`, not context cancellation.

## Development

Go tool versions match the reference services: golangci-lint `v2.11.3`, sqld `v1.1.10`, easyp `v0.16.6`, protoc-gen-go `v1.36.11` and protoc-gen-go-grpc `v1.6.2`. Tools install into `bin/`; use Node 22 and Yarn Classic `1.22.22`. Console packages require GitHub Packages read access.

```sh
make configure configure-web configure-events
make gen db
make web events build
make lint test
```

All npm/yarn commands in the Makefile and workflows unset `HTTPS_PROXY`, `HTTP_PROXY`, `https_proxy` and `http_proxy` for that invocation only. Direct package commands need the same prefix:

```sh
env -u HTTPS_PROXY -u HTTP_PROXY -u https_proxy -u http_proxy yarn --cwd web typecheck
```

The repository owns its development PostgreSQL, isolated under Compose project `backplane-legal`. It uses PostgreSQL 17, loopback port `5457`, credentials `legal:legal`, database `legal` and its own named volume; these credentials are development defaults, not deployment secrets.

```sh
docker compose up -d postgres
make test-live
# Equivalent test configuration:
LEGAL_TEST_PG='postgres://legal:legal@localhost:5457/legal?sslmode=disable' make test
```

Without `LEGAL_TEST_PG`, PostgreSQL integration tests skip. `make test` runs vet and race-enabled tests; CI supplies its own PostgreSQL service and sets the variable so those tests run.

`make db` regenerates typed queries and validates migrations. Before the first release, `make db-init` recreates only this Compose project's `legal_scratch` database and rebuilds the init migration. It is destructive to that scratch database; never override `DB_DEV_URL` with a real database. Ordinary runtime and integration data use `legal`, not `legal_scratch`. No target manages another repository's containers.

`make release` retains the reference services' interactive tagging procedure: it requires a clean synchronized branch and confirmation before pushing a new or recreated tag. It doesn't run local builds or tests. The tag workflow builds Linux amd64/arm64 archives and the multi-platform image, publishes the event package, then creates the GitHub release.
