package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/kou1046/advent2026-mcp-authz/days/12-02-minimal-mcp/internal/notes"
)

// 認可がまだないので、誰が書いても同じ owner になる。
const anonymous = "anonymous"

// 認可なしで一般公開しているので、誰が何を書いてくるかわからない。
// プロンプトインジェクションの置き場にしないよう、認可を入れるまでは、書かれた中身は捨てて伏せた文言で保存する。
const masked = "（認可がないので伏せています）"

const (
	maxTitle = 100
	maxBody  = 2000
)

func (s *server) toolDefs() []map[string]any {
	readSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "読むメモの ID。省略すると一覧を返す"},
		},
		"additionalProperties": false,
	}
	writeSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string", "maxLength": maxTitle},
			"body":  map[string]any{"type": "string", "maxLength": maxBody},
		},
		"required":             []string{"title", "body"},
		"additionalProperties": false,
	}
	var rs, ws any = readSchema, writeSchema
	if s.brk == "bad-schema" {
		rs, ws = nil, nil
	}

	return []map[string]any{
		{
			"name":        "read_note",
			"title":       "メモを読む",
			"description": "メモを読む。id を渡すとその1件、省略すると一覧を返す。認可がまだないので、タイトルと本文は伏せてある。",
			"inputSchema": rs,
			"annotations": map[string]any{"readOnlyHint": true},
		},
		{
			"name":        "write_note",
			"title":       "メモを書く",
			"description": "新しいメモを書いて、その ID を返す。認可がまだないので、タイトルと本文は伏せて保存する。",
			"inputSchema": ws,
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
	}
}

// callTool はツールを実行する。知らないツールや形の崩れた引数はプロトコルのエラー、
// 「メモがない」「長すぎる」のようなツールとしての失敗は isError の結果で返す。
func (s *server) callTool(ctx context.Context, p params) (map[string]any, *rpcError) {
	args := p.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	invalid := &rpcError{Code: codeInvalidParams, Message: "invalid arguments for " + p.Name}

	switch p.Name {
	case "read_note":
		var in struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(args, &in) != nil {
			return nil, invalid
		}
		if in.ID == "" {
			list, err := s.store.List(ctx)
			if err != nil {
				return nil, internal(err)
			}
			return toolResult(map[string]any{"notes": list}), nil
		}
		n, err := s.store.Get(ctx, in.ID)
		if errors.Is(err, notes.ErrNotFound) {
			return toolError("メモ " + in.ID + " は見つかりません"), nil
		}
		if err != nil {
			return nil, internal(err)
		}
		return toolResult(n), nil

	case "write_note":
		var in struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if json.Unmarshal(args, &in) != nil {
			return nil, invalid
		}
		switch {
		case in.Title == "":
			return toolError("title が空です"), nil
		case utf8.RuneCountInString(in.Title) > maxTitle:
			return toolError(fmt.Sprintf("title は %d 文字までです", maxTitle)), nil
		case utf8.RuneCountInString(in.Body) > maxBody:
			return toolError(fmt.Sprintf("body は %d 文字までです", maxBody)), nil
		}
		n, err := s.store.Create(ctx, anonymous, masked, masked)
		if err != nil {
			return nil, internal(err)
		}
		return toolResult(n), nil
	}
	return nil, &rpcError{Code: codeInvalidParams, Message: "Unknown tool: " + p.Name}
}

// toolResult は構造化した結果と、同じ中身の JSON テキストを両方返す。
func toolResult(v any) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": v,
		"isError":           false,
	}
}

func toolError(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
}

func internal(err error) *rpcError {
	return &rpcError{status: http.StatusInternalServerError, Code: -32603, Message: "internal error: " + err.Error()}
}
