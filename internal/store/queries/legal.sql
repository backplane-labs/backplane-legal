-- name: EnsureHead :exec
INSERT INTO legal.chain_head(id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- name: LockHead :one
SELECT seq, hash FROM legal.chain_head WHERE id = 1 FOR UPDATE;

-- name: Head :one
SELECT seq, hash FROM legal.chain_head WHERE id = 1;

-- name: UpdateHead :exec
UPDATE legal.chain_head SET seq = @seq, hash = @hash WHERE id = 1;

-- name: CreateDocument :exec
INSERT INTO legal.document(slug,audience,payload) VALUES (@slug,@audience,@payload);

-- name: Document :one
SELECT payload FROM legal.document WHERE slug = @slug;

-- name: Documents :many
SELECT payload FROM legal.document WHERE @audience::text = '' OR audience = @audience ORDER BY slug;

-- name: Versions :many
SELECT payload FROM legal.version WHERE document = @document ORDER BY number DESC;

-- name: SaveVersion :exec
INSERT INTO legal.version(document,number,status,effective_at,payload)
VALUES (@document,@number,@status,@effective_at,@payload)
ON CONFLICT (document,number) DO UPDATE SET status=EXCLUDED.status,effective_at=EXCLUDED.effective_at,payload=EXCLUDED.payload;

-- name: Records :many
SELECT seq,id,subject,document,version,accepted_at,kind,payload FROM legal.journal
WHERE (@subject::text = '' OR subject = @subject)
AND (@document::text = '' OR document = @document)
AND (@version::bigint = 0 OR version = @version)
AND (@from_time::timestamptz IS NULL OR accepted_at >= @from_time)
AND (@to_time::timestamptz IS NULL OR accepted_at <= @to_time)
ORDER BY seq;

-- name: AppendRecord :exec
INSERT INTO legal.journal(seq,id,subject,document,version,accepted_at,kind,payload)
VALUES (@seq,@id,@subject,@document,@version,@accepted_at,@kind,@payload);

-- name: Enqueue :exec
INSERT INTO legal.outbox(name,payload) VALUES (@name,@payload);

-- name: PendingEvents :many
SELECT id,name,payload FROM legal.outbox ORDER BY id LIMIT @max_events::integer;

-- name: AckEvent :exec
DELETE FROM legal.outbox WHERE id = @id;

-- name: LockDocument :one
SELECT slug FROM legal.document WHERE slug = @slug FOR UPDATE;
