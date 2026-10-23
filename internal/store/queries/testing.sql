-- Integration tests only: these paths require table-owner privileges and are
-- always executed in a rolled-back transaction. Production exposes no tamper API.

-- name: TestDisableJournalGuard :exec
ALTER TABLE legal.journal DISABLE TRIGGER journal_immutable;

-- name: TestTamperRecord :exec
UPDATE legal.journal SET payload = jsonb_set(payload, '{subject}', '"tampered"'::jsonb) WHERE seq = @seq;

-- name: TestDeleteRecord :exec
DELETE FROM legal.journal WHERE seq = @seq;

-- name: TestUpdateRecord :exec
UPDATE legal.journal SET subject = 'tampered' WHERE seq = @seq;

-- name: TestTruncateJournal :exec
TRUNCATE TABLE legal.journal;

-- name: TestUpdatePublished :exec
UPDATE legal.version SET effective_at = now() WHERE document = @document AND number = @number;

-- name: TestDeletePublished :exec
DELETE FROM legal.version WHERE document = @document AND number = @number;

-- name: TestTruncateVersions :exec
TRUNCATE TABLE legal.version CASCADE;

-- name: TestSetLockTimeout :exec
SET LOCAL lock_timeout = '100ms';
