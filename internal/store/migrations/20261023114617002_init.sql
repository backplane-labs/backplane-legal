-- sqld:up
CREATE SCHEMA IF NOT EXISTS "legal";
CREATE TABLE "legal"."chain_head" (
  "id" int4 NOT NULL,
  "seq" int8 NOT NULL DEFAULT 0,
  "hash" text NOT NULL DEFAULT ''::text,
  PRIMARY KEY ("id")
);
CREATE TABLE "legal"."document" (
  "slug" text NOT NULL,
  "audience" text NOT NULL,
  "payload" jsonb NOT NULL,
  PRIMARY KEY ("slug")
);
CREATE TABLE "legal"."journal" (
  "seq" int8 NOT NULL,
  "id" uuid NOT NULL,
  "subject" text NOT NULL,
  "document" text NOT NULL,
  "version" int8 NOT NULL,
  "accepted_at" timestamptz NOT NULL,
  "kind" text NOT NULL,
  "payload" jsonb NOT NULL,
  PRIMARY KEY ("seq")
);
CREATE TABLE "legal"."outbox" (
  "id" int8 NOT NULL GENERATED ALWAYS AS IDENTITY (SEQUENCE NAME "legal"."outbox_id_seq" INCREMENT BY 1 MINVALUE 1 MAXVALUE 9223372036854775807 START WITH 1 CACHE 1 NO CYCLE),
  "name" text NOT NULL,
  "payload" jsonb NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
CREATE TABLE "legal"."version" (
  "document" text NOT NULL,
  "number" int8 NOT NULL,
  "status" text NOT NULL,
  "effective_at" timestamptz NOT NULL,
  "payload" jsonb NOT NULL,
  PRIMARY KEY ("document", "number")
);
ALTER TABLE "legal"."chain_head" ADD CONSTRAINT "chain_seq" CHECK (seq >= 0);
ALTER TABLE "legal"."chain_head" ADD CONSTRAINT "chain_singleton" CHECK (id = 1);
ALTER TABLE "legal"."document" ADD CONSTRAINT "document_audience" CHECK (audience = ANY (ARRAY['all'::text, 'registration'::text]));
ALTER TABLE "legal"."document" ADD CONSTRAINT "document_slug" CHECK (slug ~ '^[a-z][a-z0-9-]*$'::text);
ALTER TABLE "legal"."journal" ADD CONSTRAINT "journal_id_key" UNIQUE ("id");
ALTER TABLE "legal"."journal" ADD CONSTRAINT "journal_kind" CHECK (kind = ANY (ARRAY['accepted'::text, 'withdrawn'::text]));
ALTER TABLE "legal"."journal" ADD CONSTRAINT "journal_seq" CHECK (seq > 0);
ALTER TABLE "legal"."version" ADD CONSTRAINT "version_number" CHECK (number > 0);
ALTER TABLE "legal"."version" ADD CONSTRAINT "version_status" CHECK (status = ANY (ARRAY['draft'::text, 'published'::text]));
CREATE INDEX "journal_document_time" ON "legal"."journal" ("document", "accepted_at");
CREATE INDEX "journal_subject" ON "legal"."journal" ("subject", "document", "version", "seq" DESC);
CREATE INDEX "journal_time" ON "legal"."journal" ("accepted_at");
CREATE INDEX "version_current" ON "legal"."version" ("document", "number" DESC) WHERE (status = 'published'::text);
ALTER TABLE "legal"."journal" ADD CONSTRAINT "journal_document_version_fkey" FOREIGN KEY ("document", "version") REFERENCES "legal"."version" ("document", "number");
ALTER TABLE "legal"."version" ADD CONSTRAINT "version_document_fkey" FOREIGN KEY ("document") REFERENCES "legal"."document" ("slug");
CREATE FUNCTION "legal"."guard_published_version"() RETURNS trigger LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE AS $$
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
CREATE FUNCTION "legal"."reject_journal_mutation"() RETURNS trigger LANGUAGE plpgsql VOLATILE PARALLEL UNSAFE AS $$
BEGIN
 RAISE EXCEPTION 'legal journal is append-only' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER "journal_immutable" BEFORE UPDATE OR DELETE ON "legal"."journal" FOR EACH ROW EXECUTE FUNCTION "legal"."reject_journal_mutation"();
CREATE TRIGGER "journal_no_truncate" BEFORE TRUNCATE ON "legal"."journal" FOR EACH STATEMENT EXECUTE FUNCTION "legal"."reject_journal_mutation"();
CREATE TRIGGER "version_immutable" BEFORE UPDATE OR DELETE ON "legal"."version" FOR EACH ROW EXECUTE FUNCTION "legal"."guard_published_version"();
CREATE TRIGGER "version_no_truncate" BEFORE TRUNCATE ON "legal"."version" FOR EACH STATEMENT EXECUTE FUNCTION "legal"."guard_published_version"();

-- sqld:down
DROP TRIGGER "version_no_truncate" ON "legal"."version";
DROP TRIGGER "version_immutable" ON "legal"."version";
DROP TRIGGER "journal_no_truncate" ON "legal"."journal";
DROP TRIGGER "journal_immutable" ON "legal"."journal";
DROP FUNCTION "legal"."reject_journal_mutation"();
DROP FUNCTION "legal"."guard_published_version"();
ALTER TABLE "legal"."version" DROP CONSTRAINT "version_document_fkey";
ALTER TABLE "legal"."journal" DROP CONSTRAINT "journal_document_version_fkey";
DROP INDEX "legal"."version_current";
DROP INDEX "legal"."journal_time";
DROP INDEX "legal"."journal_subject";
DROP INDEX "legal"."journal_document_time";
ALTER TABLE "legal"."version" DROP CONSTRAINT "version_status";
ALTER TABLE "legal"."version" DROP CONSTRAINT "version_number";
ALTER TABLE "legal"."journal" DROP CONSTRAINT "journal_seq";
ALTER TABLE "legal"."journal" DROP CONSTRAINT "journal_kind";
ALTER TABLE "legal"."journal" DROP CONSTRAINT "journal_id_key";
ALTER TABLE "legal"."document" DROP CONSTRAINT "document_slug";
ALTER TABLE "legal"."document" DROP CONSTRAINT "document_audience";
ALTER TABLE "legal"."chain_head" DROP CONSTRAINT "chain_singleton";
ALTER TABLE "legal"."chain_head" DROP CONSTRAINT "chain_seq";
DROP TABLE "legal"."version";
DROP TABLE "legal"."outbox";
DROP TABLE "legal"."journal";
DROP TABLE "legal"."document";
DROP TABLE "legal"."chain_head";
DROP SCHEMA "legal";
