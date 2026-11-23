// Package notes はメモの保存先をひとつにまとめる。
// 今はメモリに置くだけだが、あとで下流の API に差し替えられるよう Store に閉じ込めておく。
package notes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var ErrNotFound = errors.New("note not found")

type Note struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

type Store interface {
	Get(ctx context.Context, id string) (Note, error)
	List(ctx context.Context) ([]Note, error)
	Create(ctx context.Context, owner, title, body string) (Note, error)
}

// Memory はプロセスのメモリにメモを置く Store。再起動で消える。
type Memory struct {
	mu    sync.Mutex
	seq   int
	notes map[string]Note
}

func NewMemory() *Memory {
	return &Memory{notes: map[string]Note{}}
}

func (m *Memory) Get(_ context.Context, id string) (Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.notes[id]
	if !ok {
		return Note{}, ErrNotFound
	}
	return n, nil
}

func (m *Memory) List(_ context.Context) ([]Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]Note, 0, len(m.notes))
	for _, n := range m.notes {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	return list, nil
}

func (m *Memory) Create(_ context.Context, owner, title, body string) (Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	n := Note{
		ID:        fmt.Sprintf("n%d", m.seq),
		Owner:     owner,
		Title:     title,
		Body:      body,
		CreatedAt: time.Now().UTC(),
	}
	m.notes[n.ID] = n
	return n, nil
}
