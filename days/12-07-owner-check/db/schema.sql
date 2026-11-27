-- メモのテーブル。持ち主は、書いた人のトークンの iss（どこの）と sub（誰）の組で覚える。
-- Postgres の起動時に流し（docker-entrypoint-initdb.d）、sqlc もこのファイルを読む。
CREATE TABLE notes (
  id         text PRIMARY KEY,
  owner_iss  text NOT NULL,
  owner_sub  text NOT NULL,
  title      text NOT NULL,
  body       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- 一覧は、持ち主で絞ってから作った順に並べる。
CREATE INDEX notes_owner ON notes (owner_iss, owner_sub, created_at);

-- メモの ID（n1、n2、…）を振るための連番。
CREATE SEQUENCE note_seq;
