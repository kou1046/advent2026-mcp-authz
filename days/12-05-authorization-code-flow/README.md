# 12/5 MCP サーバーのメタデータの resource を偽ると、claude.ai で何が起きるか

記事：https://qiita.com/kou1046/items/43a1cb1599580cbd5439

12/4 の構成で、Keycloak にログインして、トークン付きのリクエストが MCP サーバーに届くところまで進めます。MCP サーバーは、Bearer が付いていれば中身を確かめずに通し、届いたトークンの中身をログに出します。さらに、メタデータの `resource` で別の URL を名乗らせて、claude.ai がどうするかを試します。

## 動かす

Docker、make、jq が要ります。

```sh
make tunnel   # MCP サーバー・Keycloak・nginx・cloudflared（2本）を立ち上げて、登録する URL を表示する
make logs     # 届いたリクエストのログを見る（MCP サーバーはトークンの中身、nginx は /token の本文）
make down     # 試し終わったら、必ずトンネルごと止める
```

`make tunnel` が表示する `https://….trycloudflare.com/mcp` を、claude.ai のカスタムコネクターに登録します。ログイン画面では、ユーザー `alice`、パスワード `alice` でログインできます。

宛先を偽る実験は、トンネルを立てたまま MCP サーバーだけ作り直します。

```sh
make resource RESOURCE=https://example.com/mcp   # メタデータで、よその URL を名乗る
make resource                                     # 自分の URL に戻す
```

偽る先は、存在しない `example.com` か、自分のトンネルにしてください。他人の本物のサービスを名乗らないでください。

nginx のログには `/token` の本文（`code`、`code_verifier`、シークレットなど）が出ます。試し終わったら `make down` でログごと消してください。

Keycloak の管理画面は外に出していません。手元の http://127.0.0.1:8180/admin/ から開きます（`admin` / `admin`）。

トンネルを使わずに手元だけで試すなら、こうします。

```sh
make up       # MCP サーバー（127.0.0.1:8080）、Keycloak（127.0.0.1:8180）、nginx（127.0.0.1:8081、8082）を立ち上げる
make smoke    # curl で一通り叩く
```

## 構成

- cloudflared：MCP サーバー用と認可サーバー用の2本
- nginx：12/4 と同じ。加えて、`/token` に届いたリクエストの本文をログに出す（Authorization ヘッダーは方式だけ）
- MCP サーバー：Bearer が付いていれば中身を確かめずに通す（検証は 12/6）。トークンの中身（`iss`、`aud`、`sub`、`scope` など）をログに出す。`-resource` で、メタデータの `resource` を好きな値にできる
- Keycloak：12/4 と同じ

| パス | 中身 |
|---|---|
| `cmd/mcp/` | MCP サーバー本体（`auth.go` が 12/4 からの変更） |
| `internal/notes/` | メモの保存（メモリ） |
| `deploy/local/` | Docker Compose、nginx の設定、Keycloak の設定スクリプト、smoke テスト |
