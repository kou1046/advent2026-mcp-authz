# 12/4 手元で OSS の認可サーバーを立ててみたら、claude.ai の DCR とちょっと相性が悪かった件

記事：（公開したら追記）

12/3 の仮の認可サーバーを、Keycloak に置き換えました。MCP サーバーのコードは変えず、メタデータの `authorization_servers` を Keycloak の realm に向けただけです。claude.ai は DCR で自分を登録して、Keycloak のログイン画面まで来ます。

## 動かす

Docker、make、jq が要ります。

```sh
make tunnel   # MCP サーバー・Keycloak・nginx・cloudflared（2本）を立ち上げて、登録する URL を表示する
make logs     # 届いたリクエストのログを見る
make down     # 試し終わったら、必ずトンネルごと止める
```

`make tunnel` が表示する `https://….trycloudflare.com/mcp` を、claude.ai のカスタムコネクターに登録します。ログイン画面に着いたら、ユーザー `alice`、パスワード `alice` でログインできます。

Keycloak の管理画面は外に出していません。手元の http://127.0.0.1:8180/admin/ から開きます（`admin` / `admin`）。claude.ai が DCR で登録したクライアントは、realm「mcp」のクライアント一覧に出ます。

トンネルを使わずに手元だけで試すなら、こうします。

```sh
make up       # MCP サーバー（127.0.0.1:8080）、Keycloak（127.0.0.1:8180）、nginx（127.0.0.1:8081、8082）を立ち上げる
make smoke    # curl で一通り叩く
```

## 構成

- cloudflared：MCP サーバー用と認可サーバー用の2本。それぞれ別の `https://….trycloudflare.com` の URL を出す
- nginx：ポートでホストを分ける（80：MCP のホスト、81：認可サーバーのホスト）。Anthropic が繋ぎに来る IP の範囲（[公開されている範囲](https://platform.claude.com/docs/en/api/ip-addresses)）だけを通す。ただし、ユーザーのブラウザが来るログイン画面まわりは誰でも通し、管理画面は出さない
- MCP サーバー：12/3 と同じ。メタデータの `authorization_servers` には、`make tunnel` が Keycloak の realm の URL を渡す
- Keycloak：開発モード。立ち上げるたびに `deploy/local/keycloak-setup.sh` が realm「mcp」とダミーユーザーを作り、認証なしの DCR を `redirect_uris` が claude.ai のときだけ受け付けるようにする

| パス | 中身 |
|---|---|
| `cmd/mcp/` | MCP サーバー本体（12/3 から変更なし） |
| `internal/notes/` | メモの保存（メモリ） |
| `deploy/local/` | Docker Compose、nginx の設定、Keycloak の設定スクリプト、smoke テスト |
