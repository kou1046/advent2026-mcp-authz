package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kou1046/advent2026-mcp-authz/days/12-08-tenant-separation/internal/notes"
)

// caller は、検証を通ったトークンとホストから決めた、リクエストの主。
type caller struct {
	owner  notes.Owner // 持ち主（iss と sub）
	route  *route      // 来たホストの設定
	orgs   []string    // トークンの organization クレームに入っていた組織（やり方2 で使う）
	tenant *string     // 決めたテナントを、ログに出すために書き戻す先
}

// callerKey は、caller を context に入れておくためのキー。
type callerKey struct{}

func withCaller(ctx context.Context, c caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// callerFrom は、リクエストの主を返す。持ち主は、ツールの引数からではなく、トークンから取ったものだけを使う。
func callerFrom(ctx context.Context) (caller, bool) {
	c, ok := ctx.Value(callerKey{}).(caller)
	return c, ok && c.owner.Iss != "" && c.owner.Sub != "" && c.route != nil
}

// resolveTenant は、どのテナントとして読み書きするかを決める。決められないときは、ユーザーに見せる理由を返す。
// arg は、ツールの引数 tenant。claude.ai が渡してくる値なので、そのままは信じない。
func (c caller) resolveTenant(arg string) (tenant, reason string) {
	// やり方1：テナントはホスト名で決まる。そのホストには、そのテナントの認可サーバーのトークンしか通していない。
	if c.route.tenant != "" {
		if arg != "" && arg != c.route.tenant {
			return "", "この URL は、テナント " + c.route.tenant + " のものです"
		}
		return c.route.tenant, ""
	}
	// やり方2：テナントは、トークンの organization クレームで決まる。
	switch {
	case len(c.orgs) == 0:
		return "", "テナントがわかりません（トークンに organization クレームがありません）"
	case arg == "" && len(c.orgs) == 1:
		return c.orgs[0], ""
	case arg == "":
		return "", "複数のテナントに属しています。引数 tenant で、どれかを指定してください：" + strings.Join(c.orgs, "、")
	case !slices.Contains(c.orgs, arg):
		// 引数で指定されたテナントは、トークンの中の所属と突き合わせる。
		return "", "テナント " + arg + " には属していません"
	}
	return arg, ""
}

const (
	maxTitle = 100
	maxBody  = 2000
)

// toolDefs は、ツールの一覧を返す。やり方2 のホストでは、引数 tenant を足す。
func (s *server) toolDefs(rt *route) []map[string]any {
	readProps := map[string]any{
		"id": map[string]any{"type": "string", "description": "読むメモの ID。省略すると一覧を返す"},
	}
	writeProps := map[string]any{
		"title": map[string]any{"type": "string", "maxLength": maxTitle},
		"body":  map[string]any{"type": "string", "maxLength": maxBody},
	}
	if rt.tenant == "" {
		tenant := map[string]any{"type": "string", "description": "どのテナントのメモか。複数のテナントに属しているときに指定する"}
		readProps["tenant"] = tenant
		writeProps["tenant"] = tenant
	}
	readSchema := map[string]any{
		"type":                 "object",
		"properties":           readProps,
		"additionalProperties": false,
	}
	writeSchema := map[string]any{
		"type":                 "object",
		"properties":           writeProps,
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
			"description": "自分のメモを読む。id を渡すとその1件、省略すると一覧を返す。",
			"inputSchema": rs,
			"annotations": map[string]any{"readOnlyHint": true},
		},
		{
			"name":        "write_note",
			"title":       "メモを書く",
			"description": "自分のメモを新しく書いて、その ID を返す。",
			"inputSchema": ws,
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
		},
	}
}

// callTool はツールを実行する。知らないツールや形の崩れた引数はプロトコルのエラー、
// 「メモがない」「長すぎる」「テナントが決まらない」のようなツールとしての失敗は isError の結果で返す。
func (s *server) callTool(ctx context.Context, p params) (map[string]any, *rpcError) {
	args := p.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	invalid := &rpcError{Code: codeInvalidParams, Message: "invalid arguments for " + p.Name}
	c, ok := callerFrom(ctx)
	if !ok {
		return nil, internal(errors.New("no caller"))
	}

	switch p.Name {
	case "read_note":
		var in struct {
			ID     string `json:"id"`
			Tenant string `json:"tenant"`
		}
		if json.Unmarshal(args, &in) != nil {
			return nil, invalid
		}
		tenant, reason := c.resolveTenant(in.Tenant)
		if reason != "" {
			return toolError(reason), nil
		}
		*c.tenant = tenant
		if in.ID == "" {
			list, err := s.store.List(ctx, tenant, c.owner)
			if err != nil {
				return nil, internal(err)
			}
			// どのテナントとして読んだかを、結果に入れて返す。
			return toolResult(map[string]any{"tenant": tenant, "notes": list}), nil
		}
		// 他人のメモも、別のテナントのメモも、ないメモと同じく「見つからない」になる（絞ったクエリが 0 行を返す）。
		n, err := s.store.Get(ctx, tenant, c.owner, in.ID)
		if errors.Is(err, notes.ErrNotFound) {
			return toolError("メモ " + in.ID + " は、テナント " + tenant + " に見つかりません"), nil
		}
		if err != nil {
			return nil, internal(err)
		}
		return toolResult(n), nil

	case "write_note":
		var in struct {
			Title  string `json:"title"`
			Body   string `json:"body"`
			Tenant string `json:"tenant"`
		}
		if json.Unmarshal(args, &in) != nil {
			return nil, invalid
		}
		tenant, reason := c.resolveTenant(in.Tenant)
		if reason != "" {
			return toolError(reason), nil
		}
		*c.tenant = tenant
		switch {
		case in.Title == "":
			return toolError("title が空です"), nil
		case utf8.RuneCountInString(in.Title) > maxTitle:
			return toolError(fmt.Sprintf("title は %d 文字までです", maxTitle)), nil
		case utf8.RuneCountInString(in.Body) > maxBody:
			return toolError(fmt.Sprintf("body は %d 文字までです", maxBody)), nil
		}
		n, err := s.store.Create(ctx, tenant, c.owner, in.Title, in.Body)
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

// routeFrom は、リクエストが来たホストの設定を返す。
func routeFrom(ctx context.Context) *route {
	c, _ := callerFrom(ctx)
	return c.route
}
