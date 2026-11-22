<!--
1日目（12-01-mcp-registration）から退避した「やってみる」以降の下書き（2026-11-22）。
1日目は解説回に絞り、実測は予定をずらさずに3日目・4日目へ吸収する方針（B 案）。
- 「サインインなし」の観察 → 3日目
- reqlog -mode=as で設定の組み合わせ（認証 × OAuth クライアント）ごとに届くリクエストを見る → 4日目（Keycloak を繋ぐ前段）
画像の参照（図2など）は 1日目の images/ を指しているので、使うときに張り替える。
-->

## やってみる：リクエストを全部ログに出すサーバーを公開する

では、本当に図のとおりになっているのでしょうか。URL を持つ MCP サーバーを自作して claude.ai に登録し、届くリクエストで確かめてみましょう。

### サーバー

`cmd/reqlog` は、届いたリクエストを JSON Lines で標準出力に出すだけの小さなサーバーです。応答の仕方を2つ用意しました。

| モード | 応答 | 見たいもの |
| --- | --- | --- |
| `-mode=404` | すべて 404 | 何も知らないクライアントが最初にどこを叩くか |
| `-mode=401` | MCP のパスには `401 + WWW-Authenticate`、`/.well-known/oauth-protected-resource` には RFC 9728 のメタデータを返す。それ以外の `/.well-known/` は 404 | 認可サーバーのメタデータを探しに行くところまでフローが進むか、その順番 |

<!-- TODO: -mode=as（偽の認可サーバーとして DCR / CIMD を広告し、/register と /authorize まで進める）を表に足す -->

401 モードでは、メタデータの `authorization_servers` に、わざと自分自身を書いておきます。こうすると、クライアントは認可サーバーのメタデータも自分のところへ取りに来ます。どのパスをどの順で試すのかまで、ログで見えるはずです。

なお、ログに出すときは `Authorization` と `Cookie` を伏せています。接続元 IP は Cloudflare 経由なので、`CF-Connecting-IP` から取っています。

<!-- TODO: main.go の要所（respond 関数）を抜粋して貼る -->

### 公開する

さっそく claude.ai から繋いでみたいところですが、1つ注意があります。claude.ai の場合、MCP サーバーに接続しに来るのは自分のブラウザではなく、Anthropic のクラウドです。なので、localhost のままでは繋がりません。

ちなみに、同じ Claude でも入口によって、誰が MCP サーバーに接続しに行くのかが違います。

| 入口 | 接続方式 | 接続しに行くのは誰か | localhost に繋がるか |
| --- | --- | --- | --- |
| claude.ai | remote（Streamable HTTP） | Anthropic のクラウド | 繋がらない。公開 URL が要る |
| Claude Desktop | remote（Streamable HTTP）／ local（stdio） | remote は claude.ai と同じ。local は手元のプロセス | local（stdio）なら繋がる |
| Claude Code | remote（HTTP）／ local（stdio） | 手元のマシン | 繋がる |

<!-- TODO(執筆時に確認): 各入口の画面スクショ。Desktop の remote 接続が手元から出るかクラウドから出るかを、reqlog の client_ip で確認して表を確定させる -->

そこで今回は、Cloudflare Tunnel を使って手元の localhost をインターネットに公開します。

```sh
go run ./cmd/reqlog -mode=404
cloudflared tunnel --url http://localhost:8080
```

<!-- TODO: quick tunnel か named tunnel か決める。前半を通して URL を固定したいなら named tunnel -->

### claude.ai から登録する

あとは図2の画面で、MCP サーバーの URL に `https://<トンネルのホスト>/mcp` を入れるだけです。

<!-- TODO: スクショ（追加画面 → 失敗時の表示） -->

## 観察した結果

<!-- TODO: 設定の組み合わせごとに実測する（-mode=as）
  1. 今すぐサインイン ＋ Claude の公開アイデンティティ（CIMD、default）
  2. 今すぐサインイン ＋ 自動的に登録（DCR）
  3. 今すぐサインイン ＋ 独自の OAuth クライアント（静的登録）
  4. 必要なときにサインイン
  5. サインインなし（→ 3日目向き）
  見たい点：
  - 401 の後、WWW-Authenticate の resource_metadata をそのまま取りに来たか
  - 認可サーバーのメタデータを、どのパスの順で探したか（oauth-authorization-server / openid-configuration、path insertion の有無）
  - CIMD / DCR / 静的登録で、/register や /authorize の client_id がどう違うか
  - 認可サーバーが CIMD を広告しないとき、default（CIMD）の claude.ai はどうするか
-->

### Claude Code から同じ URL を登録した場合

<!-- TODO: `claude mcp add --transport http` で同じ URL を登録し、User-Agent・順序・接続元の違いを比べる -->

## 分かったこと

<!-- TODO: 実測を見てから書く。仮説：
  - クライアントは仕様のシーケンスどおり、401 → RFC 9728 → 認可サーバーのメタデータの順に「探しに来る」
  - claude.ai のリクエストは自分のブラウザではなく Anthropic 側から来る（だから公開が要る）
  - 入口によって接続元も振る舞いも違う ＝ クライアントは多様
-->
