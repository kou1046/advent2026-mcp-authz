---
title: claude.ai に登録できる MCP サーバーに最低限要るものを、壊しながら確かめた
tags: MCP JSON-RPC Claude
---

## 今日の問い

[前回](https://qiita.com/kou1046/items/8a45872acc41e9d14919)は、claude.ai にカスタムコネクターを登録するとき、裏で何が起きているのかを見ました。今回は認可をいったん脇に置いて、もっと手前のところを確かめてみます。

MCP サーバーとして claude.ai に認識してもらうには、最低限なにが要るのでしょうか。

SDK を使えば数行で書けてしまいますが、それだと中で何が行き来しているのかが見えませんよね。なので今回は、SDK を使わずに JSON-RPC を直接書いて、MCP サーバーを作ってみます。ツールはメモを読む `read_note` と、書く `write_note` の2つだけです。

今回作るのは、「誰でも叩ける場所に置かれた、認可のない MCP サーバー」です。OAuth はまだ導入しません。まずは、これを動くところまで持っていきます。

先に言ってしまうと、最新の仕様どおりに作るだけでは、claude.ai には繋がりませんでした。何が足りなかったのかは、後半でサーバーを壊しながら確かめます。

## claude.ai に登録できる MCP サーバーに最低限要るもの

要るものは、大きく2つに分かれます。MCP として正しく話せること（プロトコルの要件）と、claude.ai から届く場所にいること（ネットワークの要件）です。

### プロトコルの要件

仕様：[MCP 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28)

#### JSON-RPC で話す

MCP のメッセージは [JSON-RPC 2.0](https://www.jsonrpc.org/specification) です。やりとりするメッセージは3種類あります。

- リクエスト：MCP サーバーに何かしてほしいときに送る。`id` と `method` を持ち、必ず返事が来る
- レスポンス：リクエストへの答え。同じ `id` に、`result`（成功）か `error`（失敗）を載せて返す
- 通知：MCP サーバーに伝えるだけで、返事のいらないときに送る。`id` がない。MCP の仕様では名前が `notifications/` で始まる

![JSON-RPC のリクエスト・レスポンスと通知](https://qiita-image-store.s3.ap-northeast-1.amazonaws.com/0/4536232/83628045-48c3-44f7-b5c4-f557a44ade26.png)
***図1：リクエストには同じ id でレスポンスが返り、通知には返事がない***

上から順に、リクエスト、レスポンス、通知の例です。

```json
{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}
{"jsonrpc": "2.0", "id": 1, "result": {"tools": []}}
{"jsonrpc": "2.0", "method": "notifications/initialized"}
```

remote の MCP サーバーでは、これを HTTP に載せます。1つの URL で POST を受けて、本文のメッセージに応えます。リクエストならレスポンスを返し（`application/json` か、SSE の `text/event-stream`）、通知なら本文なしの 202 を返すだけです。ここは最新の版でも変わりません。

#### 以前の流れ：最初に initialize する

以前の版と最新の版（2026-07-28）で大きく変わったのは、会話の始め方です。2025-11-25 までの MCP は、最初に `initialize` でバージョンや機能を交換してから、ツールを呼ぶ流れでした。

![以前の流れ：initialize で挨拶してから、セッション ID を付けてツールを呼ぶ](https://qiita-image-store.s3.ap-northeast-1.amazonaws.com/0/4536232/7749b4e3-d7f2-481c-a882-09ea354ff8f4.png)
***図2：以前の流れ。initialize で決めたことを、サーバーがセッションとして覚えておく***

この `initialize` は、JSON-RPC ではなく MCP が決めていたルールです。エディタと言語サーバーをつなぐ [LSP](https://microsoft.github.io/language-server-protocol/) にならって、最初に「どの版で話すか」「お互い何ができるか」を決めてから、本題に入っていました。続く `notifications/initialized` は、さっきの通知です。「準備できました」と伝えるだけで、返事は待ちません。

ただ、決めた内容をサーバーが会話の間ずっと覚えておく必要があり、そのためにセッション ID も要っていました。これだとスケールさせにくそうです。サーバーを増やすと、同じセッションのリクエストを同じサーバーに届け続けるか、サーバー同士でセッションを共有する必要があります。

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

1回1回のリクエストで話が完結するので、サーバーは前のリクエストを覚えておく必要がありません。セッション ID（`Mcp-Session-Id`）も廃止されました。サーバーを何台に増やしても、どれが受けてもいいわけです。

![最新の流れ：毎回のリクエストの _meta に版と機能を載せる](https://qiita-image-store.s3.ap-northeast-1.amazonaws.com/0/4536232/bee262e3-c110-4e3a-9abe-6636ba2711e7.png)
***図3：最新の流れ。毎回 _meta で名乗るので、サーバーは何も覚えなくていい（キーの接頭辞 io.modelcontextprotocol/ は省略）***

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

作るのは、メモを読み書きするだけの MCP サーバーです。サーバーは手元の PC で動かして、claude.ai から届くように、試すあいだだけインターネットに公開します。

### 作るもの

ツールは2つです。

| ツール | 画面での名前 | 引数 | すること |
|---|---|---|---|
| `read_note` | メモを読む | `id`（省略できる） | `id` を渡すとその1件、省略すると一覧を返す |
| `write_note` | メモを書く | `title`、`body` | 新しいメモを書いて、その ID を返す |

メモはサーバーのメモリに置くだけなので、止めると消えます。誰でも書ける場所に出すので、タイトルは 100 文字、本文は 2000 文字までにしています。

もう1つ、今は認可なしで一般公開しているので、セキュリティはほぼありません。誰が何を書いてくるかわからないので、メモがプロンプトインジェクションの入り口になります。なので、認可を実装するまでは、書き込み・読み取りともに、中身は全部伏せることにします。

![作成する MCP サーバー](https://qiita-image-store.s3.ap-northeast-1.amazonaws.com/0/4536232/ad585d07-d8fa-449b-bd1e-14ae49edbe9e.png)
***図4：作成する MCP サーバー***

メモは、こんな形で返ります。

```json
{"id": "n1", "owner": "anonymous", "title": "（認可がないので伏せています）", "body": "（認可がないので伏せています）", "created_at": "…"}
```

### 公開する

![claude.ai から MCP サーバーまでの構成](https://qiita-image-store.s3.ap-northeast-1.amazonaws.com/0/4536232/11862c7e-00c7-4be1-87cd-84d1e2e4f13a.png)
***図5：トンネルの URL を claude.ai に登録すると、Cloudflare → cloudflared → nginx を通って手元の MCP サーバーに届く***

ネットワークの要件を満たすために、Cloudflare のトンネル（cloudflared）で一時的に外へ出しました。`https://….trycloudflare.com` の URL がもらえて、IPv4 でも届きます。

ただ、OAuth を使っていないので、URL を知っていれば誰でも書き込めてしまいます。そこで、前に nginx を置いて、claude.ai が繋ぎに来る IP の範囲だけを通しています。試し終わったら、トンネルは閉じておきましょう。

### 実際に作ったもの

作ったものはこちらです。

- https://github.com/kou1046/advent2026-mcp-authz/tree/main/days/12-02-minimal-mcp

Docker と make があれば、このディレクトリで同じものを動かせます。

```sh
make tunnel   # MCP サーバー・nginx・cloudflared を立ち上げて、登録する URL を表示する
make logs     # 届いたリクエストのログを見る
make down     # 試し終わったら、トンネルごと止める
```

`make tunnel` が表示する `https://….trycloudflare.com/mcp` を、claude.ai のカスタムコネクターに「サインインなし」で登録すれば繋がります。

## claude.ai から繋いでみる

登録できたので、チャットから使ってみます。

### ツールを使わせてみる

チャットで「メモを書いて、それから一覧を見せて」と頼んでみました。

![claude.ai でメモを書いて一覧を見せてもらう](https://qiita-image-store.s3.ap-northeast-1.amazonaws.com/0/4536232/6f3e60f1-1f40-4de3-a89d-f53e06687656.png)
***図6：「メモを書く」「メモを読む」を順に使ってくれた。「メモを読む」のレスポンスでは、タイトルと本文が伏せられている***

画面には、ツール名の `write_note` ではなく「メモを書く」と出ていますね。`tools/list` で返した `title` が、そのまま表示に使われています。

「メモを読む」を開くと、サーバーが返したレスポンスがそのまま見られます。タイトルと本文は、ちゃんと伏せられていますね。「書いたメモ」の欄の中身は、Claude が自分で送った内容を覚えているだけです。

一覧には、Claude が書いた `n2` のほかに、`n1` も並んでいます。これは、その少し前に手元から curl で書いたメモです。どちらも `owner` は `anonymous` で、Claude も「`n1` は私が書いたものではない」としか言えていません。認証がないので、誰が書いたのかはサーバーにもわかりません。

### 届いたリクエストを見てみる

裏でサーバーに何が届いていたのかも見てみます。まずは、登録したときです。

| 時刻 (UTC) | User-Agent | 版 | メソッド |
|---|---|---|---|
| 05:21:04 | python-httpx/0.28.1 | 2025-11-25 | `initialize` |
| 05:21:09 | python-httpx/0.28.1 | 2025-11-25 | `initialize` |
| 05:21:09 | Claude-User | 2026-07-28 | `server/discover` |
| 05:21:10 | Claude-User | 2026-07-28 | `tools/list` |
| 05:21:11 | Claude-User | 2026-07-28 | `server/discover` |
| 05:21:11 | Claude-User | 2026-07-28 | `tools/list` |

claude.ai は、もう新しい版で話しかけてきていました。`Claude-User` のリクエストは `server/discover` から始まっていて、`initialize` は使っていません。

ただ、その前に、別の User-Agent（`python-httpx`）から、古い版の謎の `initialize` も届いています。送ったきりなので、登録のときの疎通確認でしょうか。これは、あとで壊して確かめます。このサーバーは新旧どちらの版にも応えるので、こちらにも返事ができています。

続いて、チャットでツールを使ったときです。

| 時刻 (UTC) | メソッド | JSON-RPC の id |
|---|---|---|
| 05:21:35 | `server/discover` | `366981048` |
| 05:21:39 | `server/discover` | `"server-discover-probe-1"` |
| 05:21:39 | `tools/list` | `0` |
| 05:21:39 | `tools/call`（`write_note`） | `1` |
| 05:21:43 | `tools/call`（`read_note`） | `2` |

`server/discover` で確かめて、`tools/list` で一覧を取って、`tools/call` で呼ぶ。さっきの3つのメソッドが、そのまま順に使われています。先頭の `server/discover` だけは、4秒ほど早く届いていて、id も大きな数字です。なぜ2回確かめるのかは、謎です。

## 壊してみる

ここまで、最新の版（2026-07-28）に沿って「最低限要るもの」を見てきました。ところが、さっきのログでは、claude.ai は登録のときに、なぜか古い版の `initialize` も送ってきていました。本当に要るのはどっちなんでしょうか。片方ずつ壊して、claude.ai がどうなるかを見てみます。

今回作ったサーバーには、わざと壊すためのオプションを付けてあります。`make tunnel BREAK=no-initialize` で古い版の `initialize` だけを、`make tunnel BREAK=no-discover` で `server/discover` だけを壊せます。

### initialize だけ壊す

まずは `BREAK=no-initialize` で立ち上げて、コネクターを登録し直してみました。


![サーバーに接続していますが「見つかりません」になる](https://qiita-image-store.s3.ap-northeast-1.amazonaws.com/0/4536232/fb9e9f3c-267e-4c86-8db7-78554bee6ad2.png)
***図7：「サーバーに接続しています」が「見つかりません」になる***

追加のダイアログが、最初の「サーバーに接続しています」で止まりました。このとき届いていたのは、`python-httpx` の謎の `initialize` だけです。この確認の正体は、あの謎の `initialize` だったわけです。

「このまま続ける」を押せば、手動で「サインインなし」を選んで追加まではできます。ただ、使おうとするとこう言われて、使えませんでした。

![確認時にサーバーからサインインを求められました](https://qiita-image-store.s3.ap-northeast-1.amazonaws.com/0/4536232/028ee691-03b1-4b61-8b89-1a78976679f1.png)
***図8：サインインは求めていないのに、求められたことにされる***

`Claude-User` は、最後まで一度も来ませんでした。正常な `server/discover` が待っているのに、呼ばれもしません。

### server/discover だけ壊す

今度は逆に、`BREAK=no-discover` で試しました。`server/discover` には、HTTP のステータス 404 と、JSON-RPC のエラー（`-32601`）が返ります。

すると、登録はすんなり通りました。「サインインなし」も、最初から自動で選ばれています。チャットでも、普通にメモを読み書きできました。ログを見ると、こうなっていました。

| 時刻 (UTC) | 版 | メソッド | 返事 |
|---|---|---|---|
| 05:52:45 | 2026-07-28 | `server/discover`（id は `"server-discover-probe-1"`） | 404 |
| 05:52:45 | （なし） | `initialize` | 200 |
| 05:52:45 | 2025-11-25 | `notifications/initialized` | 202 |
| 05:52:46 | 2025-11-25 | `tools/list` | 200 |
| 05:52:46 | 2025-11-25 | `tools/call`（`write_note`） | 200 |
| 05:52:50 | 2025-11-25 | `tools/call`（`read_note`） | 200 |

claude.ai は、まず `server/discover` で新しい版を試して、だめなら `initialize` からの古い版に切り替えていました。`"server-discover-probe-1"` という id は、新しい版で話せるかの「お試し（probe）」だったようです。

| 壊したもの | 登録 | 使えるか |
|---|---|---|
| `initialize` だけ | 確認で止まる（手動で追加はできる） | 使えない |
| `server/discover` だけ | 通る（「サインインなし」も自動で選ばれる） | 使える（古い版で話す） |

### Claude のドキュメントも見てみた

念のため、Claude のコネクターのドキュメント（[Building connectors](https://claude.com/docs/connectors/building) まわり）も見てみました。2026-11-23 時点では、MCP の版は認可の仕様として 2025-11-25 までが挙がっているだけで、2026-07-28 も `server/discover` も出てきません。`initialize` のことは書いてあります。

最新の版に沿ってここまで書いてきたのに、今の claude.ai の本命は、まだ古い版の `initialize` でした。先にドキュメントを読んでおけばよかったです。

## 感想

ログを見る限り、claude.ai の中では、`initialize` で確認する係（`python-httpx`）と、チャットする係（`Claude-User`）が分かれているんだろうな、と思います。User-Agent も、使う版も、送ってくるタイミングも違うからです。

そう考えると、いちばん面白かったのは、この2つの係のちぐはぐさです。

![確認する係は古い版しか話せず、最新の版だけのサーバーはチャットする係が話しかける前に門前払いされる](https://qiita-image-store.s3.ap-northeast-1.amazonaws.com/0/4536232/041e2f87-a12e-48b8-8dca-eb2d98ab4e0e.png)
***図9：チャットする係は最新の版で話せるのに、確認する係が古い版しか話せない***

チャットする係は、新しい版を試して、だめなら古い版に落ちる、行儀のいい作りでした。それなのに、確認する係は古い版しか話せません。チャットする係は最新の版に追いついているのに、確認する係だけが取り残されている感じです。最新の版だけを実装したサーバーは、チャットする係が話しかける前に門前払いされてしまいます。

仕様は 2026-07-28 で大きく変わりましたが、クライアントもドキュメントも、まだ移り変わりの途中なんだと思います。仕様を読むだけでは、こういうことはわかりませんでした。SDK を使わずに書いて、届いたリクエストを1つずつ見たからこそ気づけたところです。

## まとめ：claude.ai に登録できる MCP サーバーに最低限要るもの

最新の仕様（2026-07-28）どおりなら、`initialize` はなくなり、`server/discover`・`tools/list`・`tools/call` の3つで足りるはずでした。ところが、2026-11-23 時点の claude.ai で試してみると、本当に最低限要ったのはこうでした。

- 1つの URL で POST を受けて、JSON-RPC で応える
- 古い版の `initialize` に応えて、`notifications/initialized` を受け取る。これがないと、確認する係に止められて、claude.ai は話しかけてこない
- `tools/list`：ツールの一覧と、引数の形（JSON Schema）を返す
- `tools/call`：ツールを実行して、結果を返す
- `server/discover`：あれば、claude.ai は最新の版で話してくれる。なくても古い版で動く

MCP の最新の仕様（2026-07-28）だけを見て実装すると、claude.ai には門前払いされてしまいます。

いちばんの教訓は、MCP サーバーを作る側は、どの版で話しかけられても応えられるようにしておく、ということです。仕様が新しくなっても、クライアントがすぐに追いつくとは限りません。今回は claude.ai で試しましたが、ChatGPT など別のクライアントなら、また違う結果になるはずです。どのクライアントが、どの版で来るかは、サーバーからは選べません。

## 次回

今回のサーバーは、公開されていて、OAuth を使っていません。claude.ai の IP だけに絞ったとはいえ、claude.ai を使う人なら誰でも、URL さえ知っていればこのサーバーに入れます。そして入ってきた人は、サーバーから見ると全員 `anonymous` です。

次回は、このサーバーに「認可が要る」と言わせてみます。401 を返したとき、claude.ai がどこへ何を探しに行くのかを見ていきます。
