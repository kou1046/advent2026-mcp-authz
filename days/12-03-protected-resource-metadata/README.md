# 12/3 「認可が要る」と言わせる：401 と MCP のメタデータ

<!-- TODO: 記事を書いたら、内容に合わせて書き直す。Qiita の記事を公開したらリンク -->

作成中です。

## reqlog

届いたリクエストをすべて JSON Lines でログに出すだけのサーバーです。claude.ai が、どこを、どの順で叩くかを観察するために使います。

```sh
go run ./cmd/reqlog -mode=404   # すべて 404 を返す
go run ./cmd/reqlog -mode=401   # 401 + WWW-Authenticate と Protected Resource Metadata（RFC 9728）を返す
go run ./cmd/reqlog -mode=as    # 401 に加えて、自分自身を偽の認可サーバーとして広告する
```

`-mode=as` では、`-dcr=false` や `-cimd=false` で、広告する登録方式を切り替えられます。

`Authorization` と `Cookie` の中身は、ログでは伏せます。
