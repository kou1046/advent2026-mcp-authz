---
title: SDK なしで、claude.ai に登録できる最小の MCP サーバーを作る
tags: MCP JSON-RPC Go Claude
---

## 今日の問い

前回は、claude.ai にカスタムコネクターを登録するとき、裏で何が起きているのかを見ました。今回は認可をいったん脇に置いて、もっと手前のところを確かめてみます。

MCP サーバーとして claude.ai に認識してもらうには、最低限なにが要るのでしょうか。

SDK を使えば数行で書けてしまいますが、それだと中で何が行き来しているのかが見えませんよね。なので今回は、SDK を使わずに JSON-RPC を直接書いて、Go で MCP サーバーを作ってみます。ツールはメモを読む `read_note` と、書く `write_note` の2つだけです。

今回作るのは、「誰でも叩ける場所に置かれた、認可のない MCP サーバー」です。これがどういうことかは最後に考えるとして、まずは動くところまで持っていきます。

## claude.ai に登録できる (remote) MCP サーバーに最低限要るもの

要るものは、大きく2つに分かれます。MCP として正しく話せること（プロトコルの要件）と、claude.ai から届く場所にいること（ネットワークの要件）です。

### プロトコルの要件

仕様：[MCP 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28)

#### JSON-RPC で話す

MCP のメッセージは [JSON-RPC 2.0](https://www.jsonrpc.org/specification) です。やりとりするメッセージは3種類あります。

- リクエスト：MCP サーバーに何かしてほしいときに送る。`id` と `method` を持ち、必ず返事が来る
- レスポンス：リクエストへの答え。同じ `id` に、`result`（成功）か `error`（失敗）を載せて返す
- 通知：MCP サーバーに伝えるだけで、返事のいらないときに送る。`id` がない。MCP の仕様では名前が `notifications/` で始まる

<p align="center"><img src="images/jsonrpc.drawio.svg" alt="JSON-RPC のリクエスト・レスポンスと通知" width="800"></p>
<p align="center"><sub>図1：リクエストには同じ id でレスポンスが返り、通知には返事がない</sub></p>

上から順に、リクエスト、レスポンス、通知の例です。

```json
{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}
{"jsonrpc": "2.0", "id": 1, "result": {"tools": []}}
{"jsonrpc": "2.0", "method": "notifications/initialized"}
```

remote の MCP サーバーでは、これを HTTP に載せます。1つの URL で POST を受けて、本文のメッセージに応えます。リクエストならレスポンスを返し（`application/json` か、SSE の `text/event-stream`）、通知なら本文なしの 202 を返すだけです。ここは最新の版でも変わりません。

#### 以前の流れ：最初に initialize する

変わったのは、会話の始め方です。2025-11-25 までの MCP は、最初に `initialize` でバージョンや機能を交換してから、ツールを呼ぶ流れでした。

<p align="center"><img src="images/initialize.drawio.svg" alt="以前の流れ：initialize で挨拶してから、セッション ID を付けてツールを呼ぶ" width="800"></p>
<p align="center"><sub>図2：以前の流れ。initialize で決めたことを、サーバーがセッションとして覚えておく</sub></p>

この `initialize` は、JSON-RPC ではなく MCP が決めていたルールです。エディタと言語サーバーをつなぐ [LSP](https://microsoft.github.io/language-server-protocol/) にならった形で、最初に挨拶して「どの版で話すか」「お互い何ができるか」を決めてから、本題に入っていました。続く `notifications/initialized` は、さっきの通知です。「準備できました」と伝えるだけで、返事は待ちません。

ただ、決めた内容をサーバーが会話の間ずっと覚えておく必要があり、そのためにセッション ID も要っていました。これだとスケールさせにくそうです。サーバーを何台かに増やすと、同じセッションのリクエストを、覚えているサーバーに届け続けるか、セッションをサーバー同士で共有する仕組みが要ります。

#### 最新の流れ：毎回のリクエストで名乗る

ところが、最新の 2026-07-28 では、この `initialize` がなくなっています。代わりに、毎回のリクエストの `_meta` に、プロトコルのバージョンとクライアントの機能を載せます。

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

1回1回のリクエストで話が完結するので、サーバーは前のリクエストを覚えておく必要がありません。セッション ID（`Mcp-Session-Id`）も廃止されました。サーバーを何台に増やしても、どれが受けてもいいわけですね。

<p align="center"><img src="images/meta.drawio.svg" alt="最新の流れ：毎回のリクエストの _meta に版と機能を載せる" width="800"></p>
<p align="center"><sub>図3：最新の流れ。毎回 _meta で名乗るので、サーバーは何も覚えなくていい（キーの接頭辞 io.modelcontextprotocol/ は省略）</sub></p>

最新の版で、サーバーが応えるべきメソッドはこの3つです。

| メソッド | 役割 |
|---|---|
| `server/discover` | サーバーの名前、対応している版、機能（ツールを持っている、など）を返す。`initialize` の代わり |
| `tools/list` | ツールの一覧と、それぞれの引数の形（JSON Schema）を返す |
| `tools/call` | ツールを実行して、結果を返す |

HTTP のヘッダーにも決まりが増えています。`MCP-Protocol-Version`、`Mcp-Method`、`tools/call` なら `Mcp-Name` を付けて、本文の中身と同じ値にします。本文を読まずに振り分けたいプロキシのためのもので、食い違っていたらサーバーは 400 で断ります。

### ネットワークの要件

出典：[claude.ai のコネクターのトラブルシューティング](https://claude.com/docs/connectors/building/troubleshooting)

claude.ai は、手元の PC ではなく Anthropic のサーバーから繋いできます。なので、プロトコルを正しく話せても、届かなければ意味がありません。

- インターネットから届く `https://` の URL があること。`localhost` やプライベートなアドレス（`192.168.x.x` など）に解決される URL は、claude.ai の側で断られます
- IPv4 で届くこと。コネクターは IPv4 にしか繋がないので、IPv6 だけのホストには届きません
- 前に WAF やファイアウォールを置くなら、Anthropic が繋ぎに来る IP の範囲（[公開されています](https://platform.claude.com/docs/en/api/ip-addresses)）を通すこと

## 作ってみる

<!-- TODO: コードの置き場所（リポジトリを公開したらリンク） -->

### 新旧どちらの版にも応える

作り始める前に、気になったことがありました。claude.ai は、もう新しい版で話しかけてくるのでしょうか。まだ `initialize` から始める古い版かもしれませんよね。

わからないので、両方に応えるようにしました。`_meta` にバージョンが載っていれば新しい版として、載っていなければ古い版として処理します。

### ツールの失敗と、プロトコルのエラーを分ける

書いていて面白かったのが、エラーの返し方が2種類あることです。

- プロトコルのエラー：知らないツール名や、形の崩れた引数。JSON-RPC の `error` で返す
- ツールとしての失敗：「そのメモは見つからない」「タイトルが長すぎる」。ふつうの結果として返し、`isError: true` を付ける

後者は、LLM に「失敗した理由」を読ませて、言い直させるためのものです。だから `error` ではなく、読める文章として結果に入れるわけですね。

```json
{"content": [{"type": "text", "text": "メモ n9 は見つかりません"}], "isError": true, "resultType": "complete"}
```

### curl で叩いてみる

まずは手元から `server/discover` を叩いてみます。

```sh
curl http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2026-07-28' \
  -H 'Mcp-Method: server/discover' \
  -d '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'
```

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "resultType": "complete",
    "supportedVersions": ["2026-07-28"],
    "capabilities": {"tools": {}},
    "instructions": "メモを読み書きするサーバーです。read_note で一覧か1件を読み、write_note で新しいメモを書きます。",
    "ttlMs": 3600000,
    "cacheScope": "public",
    "_meta": {"io.modelcontextprotocol/serverInfo": {"name": "advent-notes", "version": "0.1.0"}}
  }
}
```

`capabilities` に `tools` があるので、「このサーバーはツールを持っている」とクライアントに伝わります。

### 公開する

<p align="center"><img src="images/architecture.drawio.svg" alt="claude.ai から MCP サーバーまでの構成" width="720"></p>
<p align="center"><sub>図4：claude.ai → Cloudflare → cloudflared → nginx → MCP サーバー</sub></p>

ネットワークの要件を満たすために、Cloudflare のトンネル（cloudflared）で一時的に外へ出しました。`https://….trycloudflare.com` の URL がもらえて、IPv4 でも届きます。

ただ、OAuth を使っていないので、URL を知っていれば誰でも書き込めてしまいます。そこで、前に nginx を置いて、claude.ai が繋ぎに来る IP の範囲だけを通しています。試し終わったら、トンネルは閉じておきましょう。

## claude.ai から繋いでみる

カスタムコネクターに、トンネルの URL を「サインインなし」で登録してみました。そのときサーバーに届いたリクエストがこちらです。

| 時刻 (UTC) | User-Agent | 版 | メソッド |
|---|---|---|---|
| 09:26:14 | python-httpx/0.28.1 | 2025-11-25 | `initialize` |
| 09:26:18 | python-httpx/0.28.1 | 2025-11-25 | `initialize` |
| 09:26:18 | Claude-User | 2026-07-28 | `server/discover` |
| 09:26:18 | Claude-User | 2026-07-28 | `tools/list` |
| 09:26:19 | Claude-User | 2026-07-28 | `server/discover` |
| 09:26:19 | Claude-User | 2026-07-28 | `tools/list` |

claude.ai は、もう新しい版で話しかけてきていました。`Claude-User` のリクエストは `server/discover` から始まっていて、`initialize` は使っていません。

ただ、その前に、別の User-Agent（`python-httpx`）から古い版の `initialize` も届いています。送ったきりで続きがないので、登録のときの疎通確認なのかな、と思っていますが、確かめたわけではありません。新旧どちらにも応えるようにしておいたので、気づけたところですね。

### ツールを使わせてみる

続いて、チャットで「メモを書いて、それから一覧を見せて」と頼んでみました。

<p align="center"><img src="images/chat-write-read.png" alt="claude.ai でメモを書いて一覧を見せてもらう" width="600" style="border: 1px solid #999"></p>
<p align="center"><sub>図5：「メモを書く」「メモを読む」を順に使って、書いたメモの一覧を返してくれた</sub></p>

画面には、ツール名の `write_note` ではなく「メモを書く」と出ていますね。`tools/list` で返した `title` が、そのまま表示に使われています。

| 時刻 (UTC) | メソッド | JSON-RPC の id |
|---|---|---|
| 09:28:22 | `server/discover` | `987823046` |
| 09:28:25 | `server/discover` | `"server-discover-probe-1"` |
| 09:28:25 | `tools/list` | `0` |
| 09:28:26 | `tools/call`（`write_note`） | `1` |
| 09:28:30 | `tools/call`（`read_note`） | `2` |

`server/discover` で確かめて、`tools/list` で一覧を取って、`tools/call` で呼ぶ。さっきの3つのメソッドが、そのまま順に使われています。

書き込まれたメモの `owner` は `anonymous` です。認可がないので、誰が書いたのかはサーバーにはわかりません。

## 壊してみる

最低限の3つは、本当に全部要るのでしょうか。わざと壊して、claude.ai がどうなるかを見てみます。

<!-- TODO: claude.ai で試した結果
- no-initialize：initialize / server/discover に「そんなメソッドはない」と返す → 登録で止まる？ツールまでは行ける？
- bad-schema：ツールの inputSchema を null にする → 一覧から外れる？全部拒否？
-->

## まとめ：claude.ai に登録できる (remote) MCP サーバーに最低限要るもの

<!-- TODO: 壊してみた結果を受けて書き直す -->

プロトコルの要件

- 1つの URL で POST を受けて、JSON-RPC で応える
- `server/discover`：自分の名前、対応している版、機能を返す
- `tools/list`：ツールの一覧と、引数の形（JSON Schema）を返す
- `tools/call`：ツールを実行する。ツールとしての失敗は `isError` で返す
- 毎回のリクエストで、`_meta` の版とヘッダーを確かめる

ネットワークの要件

- インターネットから IPv4 で届く `https://` の URL
- 前で絞るなら、Anthropic の IP の範囲を通す

## 感想

<!-- TODO -->

## 次回

今回のサーバーは、公開されていて、OAuth を使っていません。claude.ai の IP だけに絞ったとはいえ、claude.ai を使う人なら誰でも、URL さえ知っていればこのサーバーに入れます。そして入ってきた人は、サーバーから見ると全員 `anonymous` です。

次回は、このサーバーに「認可が要る」と言わせてみます。401 を返したとき、claude.ai がどこへ何を探しに行くのかを見ていきます。
