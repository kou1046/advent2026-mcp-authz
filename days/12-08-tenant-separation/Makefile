COMPOSE := docker compose -f deploy/local/compose.yaml

# cloudflared のログから、quick tunnel の URL が出るまで待って取り出す。
tunnel_url = until url=$$($(COMPOSE) logs $(1) 2>&1 | grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' | tail -1); [ -n "$$url" ]; do sleep 1; done; echo $$url

.PHONY: start up tunnel smoke logs down

# MCP サーバー・Postgres・Keycloak・nginx を立ち上げる（realm はまだ作らない）。
start:
	BREAK=$(BREAK) $(COMPOSE) up -d --build mcp db keycloak nginx

# 手元だけで試すとき。立ち上げて、手元の URL を aud にした realm を作る。
up: start
	sh deploy/local/keycloak-setup.sh

# トンネルを2本立てて、claude.ai に登録する URL を出す。
# どちらの URL もトンネルを立てるまでわからないので、先に2本とも立ててから、
# 認可サーバーの URL を Keycloak（外から見える URL）と MCP サーバー（メタデータに書く issuer）に渡して作り直し、
# MCP サーバーの URL を Keycloak の aud（Audience マッパー）に入れる。
# Keycloak は作り直すと中身が消えるので、realm もここで作り直す（パスワードも作り直して表示する）。
# cloudflared は作り直す（使い回すと、ログに残った前回の URL を拾ってしまう）。
# --no-deps は、依存先の mcp や keycloak を、AUTH_SERVER のない既定の設定で作り直させないため。
# 12/8 から、MCP のトンネルは3本。やり方2 用と、やり方1 のテナントごと（テナントはホスト名で見分ける）。
# 最初はパス（/t/{テナント}/mcp）で分けたが、claude.ai ではログインのあと繋がらなかったので、ホスト名で分ける。
# 12/8 から、realm はトンネルの URL がわかってから1回だけ作る（up を経由すると、手元用の realm を作ってすぐ消すことになる）。
tunnel: start
	$(COMPOSE) --profile tunnel up -d --no-deps --force-recreate cloudflared-as cloudflared-mcp cloudflared-mcp-a cloudflared-mcp-b
	@as=$$($(call tunnel_url,cloudflared-as)); \
		mcp=$$($(call tunnel_url,cloudflared-mcp)); \
		mcpa=$$($(call tunnel_url,cloudflared-mcp-a)); \
		mcpb=$$($(call tunnel_url,cloudflared-mcp-b)); \
		AUTH_SERVER=$$as TENANT_HOSTS=tenant-a=$${mcpa#https://},tenant-b=$${mcpb#https://} BREAK=$(BREAK) \
			$(COMPOSE) up -d --no-deps mcp keycloak && \
		MCP_URL_SHARED=$$mcp/mcp MCP_URL_A=$$mcpa/mcp MCP_URL_B=$$mcpb/mcp sh deploy/local/keycloak-setup.sh && \
		$(COMPOSE) restart nginx && \
		echo "認可サーバー: $$as/realms/{tenant-a,tenant-b,shared}" && \
		echo "claude.ai に登録する URL" && \
		echo "  やり方1（テナント A）: $$mcpa/mcp" && \
		echo "  やり方1（テナント B）: $$mcpb/mcp" && \
		echo "  やり方2            : $$mcp/mcp"

smoke:
	sh deploy/local/smoke.sh

logs:
	$(COMPOSE) logs -f mcp keycloak nginx

# トンネルも含めて全部止める。試し終わったら必ず。
down:
	$(COMPOSE) --profile tunnel down
