COMPOSE := docker compose -f deploy/local/compose.yaml

# cloudflared のログから、quick tunnel の URL が出るまで待って取り出す。
tunnel_url = until url=$$($(COMPOSE) logs $(1) 2>&1 | grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' | tail -1); [ -n "$$url" ]; do sleep 1; done; echo $$url

.PHONY: up tunnel smoke logs down

# MCP サーバー・Keycloak・nginx を立ち上げて、Keycloak に realm を作る。
up:
	BREAK=$(BREAK) AUTH_SERVER=$(AUTH_SERVER) $(COMPOSE) up -d --build mcp keycloak nginx
	sh deploy/local/keycloak-setup.sh

# トンネルを2本立てて、claude.ai に登録する URL を出す。
# 認可サーバーの URL はトンネルを立てるまでわからないので、先に認可サーバーのトンネルを立て、
# その URL を Keycloak（外から見える URL）と MCP サーバー（メタデータに書く issuer）に渡して作り直してから、MCP のトンネルを立てる。
# Keycloak は作り直すと中身が消えるので、realm もここで作り直す。
# cloudflared は作り直す（使い回すと、ログに残った前回の URL を拾ってしまう）。
# --no-deps は、依存先の mcp や keycloak を、AUTH_SERVER のない既定の設定で作り直させないため。
tunnel: up
	$(COMPOSE) --profile tunnel up -d --no-deps --force-recreate cloudflared-as
	@as=$$($(call tunnel_url,cloudflared-as)); \
		BREAK=$(BREAK) AUTH_SERVER=$$as $(COMPOSE) up -d --no-deps mcp keycloak && \
		sh deploy/local/keycloak-setup.sh && \
		$(COMPOSE) restart nginx && \
		$(COMPOSE) --profile tunnel up -d --no-deps --force-recreate cloudflared-mcp && \
		mcp=$$($(call tunnel_url,cloudflared-mcp)) && \
		echo "認可サーバー: $$as/realms/mcp" && \
		echo "claude.ai に登録する URL: $$mcp/mcp"

smoke:
	sh deploy/local/smoke.sh

logs:
	$(COMPOSE) logs -f mcp keycloak nginx

# トンネルも含めて全部止める。試し終わったら必ず。
down:
	$(COMPOSE) --profile tunnel down
