-- どのクエリも、持ち主（トークンの iss と sub）で絞る。
-- ID は claude.ai がツールの引数で渡してくる値なので、ID だけでは引かない。

-- name: ListNotes :many
-- read_note（id なし）：自分のメモの一覧
SELECT id, owner_iss, owner_sub, title, body, created_at
FROM notes
WHERE owner_iss = $1 AND owner_sub = $2
ORDER BY created_at;

-- name: GetNote :one
-- read_note（id あり）：その1件。他人のメモなら 0 行（ないメモと同じ）
SELECT id, owner_iss, owner_sub, title, body, created_at
FROM notes
WHERE id = $1 AND owner_iss = $2 AND owner_sub = $3;

-- name: CreateNote :one
-- write_note：持ち主を付けて保存する
INSERT INTO notes (id, owner_iss, owner_sub, title, body)
VALUES ('n' || nextval('note_seq'), $1, $2, $3, $4)
RETURNING id, owner_iss, owner_sub, title, body, created_at;
