---
title: SDK なしで MCP サーバーを書いてみたら、initialize がなくなっていた
tags: MCP JSON-RPC Go Claude
---
<!-- TODO: タイトルは仮 -->

## 今日の問い

前回は、claude.ai にカスタムコネクターを登録するとき、裏で何が起きているのかを見ました。今回は認可をいったん脇に置いて、もっと手前のところを確かめてみます。

MCP サーバーとして claude.ai に認識してもらうには、最低限なにが要るのでしょうか。

SDK を使えば数行で書けてしまいますが、それだと中で何が行き来しているのかが見えませんよね。なので今回は、SDK を使わずに JSON-RPC を直接書いて、Go で MCP サーバーを作ってみます。ツールはメモを読む `read_note` と、書く `write_note` の2つだけです。

## MCP サーバーに最低限要るもの

仕様：[MCP 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28)

MCP のメッセージは [JSON-RPC 2.0](https://www.jsonrpc.org/specification) です。remote の MCP サーバーなら、1つの URL で POST を受けて、JSON-RPC のリクエストに JSON-RPC のレスポンスを返します。ここまでは昔から変わりません。

変わったのは、会話の始め方です。2025-11-25 までの MCP は、最初に `initialize` でバージョンや機能を交換してから、ツールを呼ぶ流れでした。

```
initialize → notifications/initialized → tools/list → tools/call
```

この `initialize` は、JSON-RPC ではなく MCP が決めていたルールです。エディタと言語サーバーをつなぐ [LSP](https://microsoft.github.io/language-server-protocol/) にならった形で、最初に挨拶して「どの版で話すか」「お互い何ができるか」を決めてから、本題に入っていました。ただ、決めた内容をサーバーが会話の間ずっと覚えておく必要があり、そのためにセッション ID も要っていました。

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

最新の版で、サーバーが応えるべきメソッドはこの3つです。

| メソッド | 役割 |
|---|---|
| `server/discover` | サーバーの名前、対応している版、機能（ツールを持っている、など）を返す。`initialize` の代わり |
| `tools/list` | ツールの一覧と、それぞれの引数の形（JSON Schema）を返す |
| `tools/call` | ツールを実行して、結果を返す |

HTTP のヘッダーにも決まりが増えています。`MCP-Protocol-Version`、`Mcp-Method`、`tools/call` なら `Mcp-Name` を付けて、本文の中身と同じ値にします。本文を読まずに振り分けたいプロキシのためのもので、食い違っていたらサーバーは 400 で断ります。

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

## claude.ai から繋いでみる

<p align="center"><img src="images/architecture.drawio.svg" alt="claude.ai から MCP サーバーまでの構成" width="720"></p>
<p align="center"><sub>図1：claude.ai → Cloudflare → cloudflared → nginx → MCP サーバー</sub></p>

claude.ai は Anthropic のサーバーから繋いでくるので、手元の `localhost` には届きません。なので、Cloudflare のトンネル（cloudflared）で一時的に外へ出しました。まだ認可がないので、前に nginx を置いて、claude.ai が繋ぎに来る IP の範囲（[Anthropic が公開しています](https://platform.claude.com/docs/en/api/ip-addresses)）だけを通しています。試し終わったら、トンネルは閉じておきましょう。

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
<p align="center"><sub>図2：「メモを書く」「メモを読む」を順に使って、書いたメモの一覧を返してくれた</sub></p>

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

## まとめ：MCP サーバーに最低限要るもの

<!-- TODO: 壊してみた結果を受けて書き直す -->

- 1つの URL で POST を受けて、JSON-RPC で応える
- `server/discover`：自分の名前、対応している版、機能を返す
- `tools/list`：ツールの一覧と、引数の形（JSON Schema）を返す
- `tools/call`：ツールを実行する。ツールとしての失敗は `isError` で返す
- 毎回のリクエストで、`_meta` の版とヘッダーを確かめる

## 感想

<!-- TODO -->

## 次回

今回は claude.ai の IP だけに絞りました。でも、claude.ai を使う人なら誰でも、URL さえ知っていればこのサーバーに入れます。そして入ってきた人は、サーバーから見ると全員 `anonymous` です。

次回は、認可なしで公開するとどうなるのかを、実際に見てみます。
