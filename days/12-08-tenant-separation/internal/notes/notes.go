// Package notes はメモの保存先をひとつにまとめる。
// 12/7 から、メモは Postgres に置き、どの読み書きも持ち主（トークンの iss と sub）で絞る。
// 12/8 から、テナントでも絞る。
// SQL は db/query.sql に書き、sqlc が作った internal/notes/db のコードで叩く。
package notes

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kou1046/advent2026-mcp-authz/days/12-08-tenant-separation/internal/notes/db"
)

// ErrNotFound は、メモがないこと。他人のメモも、別のテナントのメモも、ないメモと同じくこれになる。
var ErrNotFound = errors.New("note not found")

// Owner はメモの持ち主。ユーザーを確実に見分けられるのは、iss（どこの）と sub（誰）の組だけ。
type Owner struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
}

type Note struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant"`
	Owner     Owner     `json:"owner"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Store interface {
	List(ctx context.Context, tenant string, owner Owner) ([]Note, error)
	Get(ctx context.Context, tenant string, owner Owner, id string) (Note, error)
	Create(ctx context.Context, tenant string, owner Owner, title, body string) (Note, error)
}

// Postgres は Postgres にメモを置く Store。
type Postgres struct {
	q *db.Queries
}

func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Postgres{q: db.New(pool)}, nil
}

func (p *Postgres) List(ctx context.Context, tenant string, owner Owner) ([]Note, error) {
	rows, err := p.q.ListNotes(ctx, db.ListNotesParams{Tenant: tenant, OwnerIss: owner.Iss, OwnerSub: owner.Sub})
	if err != nil {
		return nil, err
	}
	list := make([]Note, 0, len(rows))
	for _, r := range rows {
		list = append(list, fromRow(r))
	}
	return list, nil
}

func (p *Postgres) Get(ctx context.Context, tenant string, owner Owner, id string) (Note, error) {
	r, err := p.q.GetNote(ctx, db.GetNoteParams{ID: id, Tenant: tenant, OwnerIss: owner.Iss, OwnerSub: owner.Sub})
	if errors.Is(err, pgx.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, err
	}
	return fromRow(r), nil
}

func (p *Postgres) Create(ctx context.Context, tenant string, owner Owner, title, body string) (Note, error) {
	r, err := p.q.CreateNote(ctx, db.CreateNoteParams{Tenant: tenant, OwnerIss: owner.Iss, OwnerSub: owner.Sub, Title: title, Body: body})
	if err != nil {
		return Note{}, err
	}
	return fromRow(r), nil
}

func fromRow(r db.Note) Note {
	return Note{
		ID:        r.ID,
		Tenant:    r.Tenant,
		Owner:     Owner{Iss: r.OwnerIss, Sub: r.OwnerSub},
		Title:     r.Title,
		Body:      r.Body,
		CreatedAt: r.CreatedAt.Time.UTC(),
	}
}
