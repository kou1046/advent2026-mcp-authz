---
title: 仮の認可サーバーを Keycloak に置き換えて、claude.ai をログイン画面まで連れて来る
tags: MCP OAuth Keycloak Claude
---
<!-- TODO: タイトルは仮 -->

## 今日の問い

[前回](https://qiita.com/kou1046/items/dcdedf12db7a88f255a7)は、MCP サーバーに 401 を返させて、claude.ai が認可サーバーを探しに行く様子を追いかけました。claude.ai はメタデータをたどって、ブラウザを `/authorize` まで連れて行ってくれました。ただ、待っていたのは「ここまで来ました」とだけ返すハリボテの認可サーバーです。

今回は、このハリボテを本物の認可サーバー（Keycloak）に置き換えます。

claude.ai が探しに来た認可サーバーは、何を返せばいいのでしょうか。そして、Keycloak はそれをそのまま返してくれるのでしょうか。

ゴールは、claude.ai からブラウザが Keycloak のログイン画面にたどり着くところまでです。ログインした先（トークンが MCP サーバーに届くところ）は、次回に回します。

## 認可サーバーは何を返すのか

仕様：[RFC 8414](https://www.rfc-editor.org/rfc/rfc8414)、[MCP 2026-07-28 Authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization)

前回の仮の認可サーバーが返していたメタデータを、もう一度見てみましょう。claude.ai が使っていたのは、このうち次のあたりです。

| 項目 | 何に使われたか |
|---|---|
| `issuer` | この認可サーバーの名前 |
| `authorization_endpoint` | ブラウザを連れて行く先（ログイン画面の入口） |
| `token_endpoint` | code をトークンに換える場所（前回はここまで来なかった） |
| `code_challenge_methods_supported` | PKCE の方式。`S256` が要る |
| `registration_endpoint`、`client_id_metadata_document_supported` | DCR、CIMD に対応しているか |

<!-- TODO: MCP の仕様で、メタデータに必須のもの（code_challenge_methods_supported がないとクライアントは断る、など）を確かめて1〜2行 -->

Keycloak も、もちろんこのメタデータを返します。ただ、前回の仮の認可サーバーとは、置き場所が違います。

### issuer にパスが入ると、メタデータはどこに置かれるのか

前回の仮の認可サーバーの issuer は `https://bbb.trycloudflare.com` で、パスがありませんでした。なので、メタデータは素直に `/.well-known/oauth-authorization-server` に置けばよかったのです。

Keycloak の issuer は、`https://bbb.trycloudflare.com/realms/<realm 名>` のように、パスが入ります。こうなると、メタデータの置き場所の候補が増えます。MCP の仕様は、issuer にパスがあるとき、クライアントに次の順で探すよう求めています。

| 順 | 置き場所 | 決まりの出どころ |
|---|---|---|
| 1 | `/.well-known/oauth-authorization-server/realms/<realm 名>` | RFC 8414（パスを差し込む） |
| 2 | `/.well-known/openid-configuration/realms/<realm 名>` | OpenID Connect（パスを差し込む） |
| 3 | `/realms/<realm 名>/.well-known/openid-configuration` | OpenID Connect（パスの後ろに足す） |

<!-- TODO: この順番は仕様で要確認 -->

Keycloak がメタデータを置いているのは、3 です。なので、claude.ai が仕様どおりに探すなら、1 と 2 で 404 を受けてから、3 にたどり着くはずです。本当にそうなるのかは、ログで確かめます。

## 作ってみる

### 作るもの

前回の構成から、仮の認可サーバー（authserver）を Keycloak に置き換えます。MCP サーバーは、メタデータの `authorization_servers` を Keycloak の issuer に向けるだけで、コードは変えません。

<!-- 図：作るもの（前回の図2の認可サーバーを Keycloak に） -->

#### Keycloak（新規）

<!-- TODO: realm 名、ダミーユーザー（まず1人）、有効にする機能（CIMD は実験的機能 --features=cimd） -->

<!-- TODO: ログイン画面まで来させるには、claude.ai を何らかの形でクライアントとして知っている必要がある。CIMD を有効にすれば事前登録なしでログイン画面まで行けるか？ DCR を有効にするか？ 事前登録は次回なので、今回はどれで通すか決める -->

<!-- TODO: claude.ai が CIMD を選ぶには、token_endpoint_auth_methods_supported に none が要る（前回の発見と公式ドキュメント）。Keycloak のメタデータに none が入っているか要確認。入っていなければ DCR に落ちるはず -->

### 公開する

前回と同じく、MCP サーバーと認可サーバーを別々のトンネルで公開します。変わるのは、認可サーバー側の中身です。

- Keycloak は、自分が外からどの URL で見えているか（hostname）を知っている必要があります。トンネルの URL は立ててみるまでわからないので、前回の MCP サーバーと同じく、トンネルを先に立ててから Keycloak に渡します。
- ユーザーのブラウザが来るのは、`/authorize` だけではなくなります。ログイン画面の CSS や JavaScript、ログインの送信も、ブラウザから来ます。<!-- TODO: nginx で誰でも通すパスを確かめる -->
- 管理画面（`/admin`）は、外に出しません。

<!-- 図：構成（前回の図3の認可サーバーを Keycloak に） -->

### 実際に作ったもの

<!-- TODO -->

## claude.ai から繋いでみる

<!-- TODO: 実測。追加のダイアログの3段階、設定の画面の「検出済み」がどこに付くか -->

### 届いたリクエストを見てみる

<!-- TODO: 実測。well-known の 404 が並ぶか（1 → 2 → 3 の順か）、ほかに何を叩くか -->

### ログイン画面

<!-- TODO: 実測。Keycloak のログイン画面のスクショ（client_id がどう表示されるか）。CIMD なら Keycloak は client_id の URL を取りに行くはず（Keycloak 側のログ） -->

## 感想

<!-- TODO -->

## まとめ

<!-- TODO -->

## 次回

今回は、ログイン画面まで来ました。次回は、claude.ai をクライアントとして登録し、ログインしてトークンが MCP サーバーに届くところまで進めます。
