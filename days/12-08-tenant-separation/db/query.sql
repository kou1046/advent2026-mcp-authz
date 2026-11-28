-- どのクエリも、テナントと持ち主（トークンの iss と sub）で絞る。
-- ID は claude.ai がツールの引数で渡してくる値なので、ID だけでは引かない。

-- name: ListNotes :many
-- read_note（id なし）：そのテナントの、自分のメモの一覧
SELECT id, tenant, owner_iss, owner_sub, title, body, created_at
FROM notes
WHERE tenant = $1 AND owner_iss = $2 AND owner_sub = $3
ORDER BY created_at;

-- name: GetNote :one
-- read_note（id あり）：その1件。他人のメモや、別のテナントのメモなら 0 行（ないメモと同じ）
SELECT id, tenant, owner_iss, owner_sub, title, body, created_at
FROM notes
WHERE id = $1 AND tenant = $2 AND owner_iss = $3 AND owner_sub = $4;

-- name: CreateNote :one
-- write_note：テナントと持ち主を付けて保存する
INSERT INTO notes (id, tenant, owner_iss, owner_sub, title, body)
VALUES ('n' || nextval('note_seq'), $1, $2, $3, $4, $5)
RETURNING id, tenant, owner_iss, owner_sub, title, body, created_at;
