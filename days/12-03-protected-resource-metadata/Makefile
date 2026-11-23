COMPOSE := docker compose -f deploy/local/compose.yaml

# cloudflared のログから、quick tunnel の URL が出るまで待って取り出す。
tunnel_url = until url=$$($(COMPOSE) logs $(1) 2>&1 | grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' | tail -1); [ -n "$$url" ]; do sleep 1; done; echo $$url

.PHONY: up tunnel smoke logs down

# MCP サーバー・仮の認可サーバー・nginx を立ち上げる。広告する登録方式は make up DCR=false CIMD=true のように渡す。
up:
	BREAK=$(BREAK) DCR=$(DCR) CIMD=$(CIMD) AUTH_SERVER=$(AUTH_SERVER) $(COMPOSE) up -d --build mcp authserver nginx

# トンネルを2本立てて、claude.ai に登録する URL を出す。
# 認可サーバーの URL はトンネルを立てるまでわからないので、先に認可サーバーのトンネルを立て、
# その URL を MCP サーバーに渡して起動し直してから、MCP のトンネルを立てる。
# cloudflared は作り直す（使い回すと、ログに残った前回の URL を拾ってしまう）。
# --no-deps は、依存先の mcp や authserver を、AUTH_SERVER や DCR のない既定の設定で作り直させないため。
tunnel: up
	$(COMPOSE) --profile tunnel up -d --no-deps --force-recreate cloudflared-as
	@as=$$($(call tunnel_url,cloudflared-as)); \
		BREAK=$(BREAK) AUTH_SERVER=$$as $(COMPOSE) up -d --no-deps mcp && \
		$(COMPOSE) restart nginx && \
		$(COMPOSE) --profile tunnel up -d --no-deps --force-recreate cloudflared-mcp && \
		mcp=$$($(call tunnel_url,cloudflared-mcp)) && \
		echo "認可サーバー: $$as" && \
		echo "claude.ai に登録する URL: $$mcp/mcp"

smoke:
	sh deploy/local/smoke.sh

logs:
	$(COMPOSE) logs -f mcp authserver nginx

# トンネルも含めて全部止める。試し終わったら必ず。
down:
	$(COMPOSE) --profile tunnel down
