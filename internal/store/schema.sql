CREATE SCHEMA legal;

CREATE TABLE legal.document (
 slug text PRIMARY KEY,
 audience text NOT NULL,
 payload jsonb NOT NULL,
 CONSTRAINT document_slug CHECK (slug ~ '^[a-z][a-z0-9-]*$'),
 CONSTRAINT document_audience CHECK (audience IN ('all', 'registration'))
);

CREATE TABLE legal.version (
 document text NOT NULL REFERENCES legal.document(slug),
 number bigint NOT NULL,
 status text NOT NULL,
 effective_at timestamptz NOT NULL,
 payload jsonb NOT NULL,
 PRIMARY KEY (document, number),
 CONSTRAINT version_number CHECK (number > 0),
 CONSTRAINT version_status CHECK (status IN ('draft', 'published'))
);
CREATE INDEX version_current ON legal.version(document, number DESC) WHERE status = 'published';

-- A durable, transactional lock serializes journal mutations and verification.
-- Document edits/publication use their document row, never this chain lock.
-- The head also detects deletion of the last journal record.
CREATE TABLE legal.chain_head (
 id integer PRIMARY KEY,
 seq bigint NOT NULL DEFAULT 0,
 hash text NOT NULL DEFAULT '',
 CONSTRAINT chain_singleton CHECK (id = 1),
 CONSTRAINT chain_seq CHECK (seq >= 0)
);

CREATE TABLE legal.journal (
 seq bigint PRIMARY KEY,
 id uuid NOT NULL UNIQUE,
 subject text NOT NULL,
 document text NOT NULL,
 version bigint NOT NULL,
 accepted_at timestamptz NOT NULL,
 kind text NOT NULL,
 payload jsonb NOT NULL,
 FOREIGN KEY (document,version) REFERENCES legal.version(document,number),
 CONSTRAINT journal_seq CHECK (seq > 0),
 CONSTRAINT journal_kind CHECK (kind IN ('accepted', 'withdrawn'))
);
CREATE INDEX journal_subject ON legal.journal(subject, document, version, seq DESC);
CREATE INDEX journal_document_time ON legal.journal(document, accepted_at);
CREATE INDEX journal_time ON legal.journal(accepted_at);

CREATE TABLE legal.outbox (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 name text NOT NULL,
 payload jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION legal.reject_journal_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'legal journal is append-only' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER journal_immutable BEFORE UPDATE OR DELETE ON legal.journal
 FOR EACH ROW EXECUTE FUNCTION legal.reject_journal_mutation();
CREATE TRIGGER journal_no_truncate BEFORE TRUNCATE ON legal.journal
 FOR EACH STATEMENT EXECUTE FUNCTION legal.reject_journal_mutation();

CREATE FUNCTION legal.guard_published_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP = 'TRUNCATE' THEN
  RAISE EXCEPTION 'legal versions cannot be truncated' USING ERRCODE = '23514';
 END IF;
 IF OLD.status = 'published' THEN
  RAISE EXCEPTION 'published legal versions are immutable' USING ERRCODE = '23514';
 END IF;
 IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER version_immutable BEFORE UPDATE OR DELETE ON legal.version
 FOR EACH ROW EXECUTE FUNCTION legal.guard_published_version();
CREATE TRIGGER version_no_truncate BEFORE TRUNCATE ON legal.version
 FOR EACH STATEMENT EXECUTE FUNCTION legal.guard_published_version();
