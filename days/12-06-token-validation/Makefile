COMPOSE := docker compose -f deploy/local/compose.yaml

# cloudflared のログから、quick tunnel の URL が出るまで待って取り出す。
tunnel_url = until url=$$($(COMPOSE) logs $(1) 2>&1 | grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' | tail -1); [ -n "$$url" ]; do sleep 1; done; echo $$url

.PHONY: up tunnel resource audience smoke logs down

# MCP サーバー・Keycloak・nginx を立ち上げて、Keycloak に realm を作る。
up:
	BREAK=$(BREAK) RESOURCE=$(RESOURCE) AUDIENCE=$(AUDIENCE) AUTH_SERVER=$(AUTH_SERVER) $(COMPOSE) up -d --build mcp keycloak nginx
	sh deploy/local/keycloak-setup.sh

# トンネルを2本立てて、claude.ai に登録する URL を出す。
# どちらの URL もトンネルを立てるまでわからないので、先に2本とも立ててから、
# 認可サーバーの URL を Keycloak（外から見える URL）と MCP サーバー（メタデータに書く issuer）に渡して作り直し、
# MCP サーバーの URL を Keycloak の aud（Audience マッパー）に入れる。
# Keycloak は作り直すと中身が消えるので、realm もここで作り直す。
# cloudflared は作り直す（使い回すと、ログに残った前回の URL を拾ってしまう）。
# --no-deps は、依存先の mcp や keycloak を、AUTH_SERVER のない既定の設定で作り直させないため。
tunnel: up
	$(COMPOSE) --profile tunnel up -d --no-deps --force-recreate cloudflared-as cloudflared-mcp
	@as=$$($(call tunnel_url,cloudflared-as)); \
		mcp=$$($(call tunnel_url,cloudflared-mcp)); \
		BREAK=$(BREAK) RESOURCE=$(RESOURCE) AUDIENCE=$(AUDIENCE) AUTH_SERVER=$$as $(COMPOSE) up -d --no-deps mcp keycloak && \
		MCP_URL=$$mcp/mcp sh deploy/local/keycloak-setup.sh && \
		$(COMPOSE) restart nginx && \
		echo "認可サーバー: $$as/realms/mcp" && \
		echo "claude.ai に登録する URL: $$mcp/mcp"

# トンネルはそのままで、MCP サーバーだけ作り直して、メタデータで名乗る resource を変える（宛先を偽る実験）。
# 例：make resource RESOURCE=https://example.com/mcp ／ make resource（元に戻す）
resource:
	@as=$$($(call tunnel_url,cloudflared-as)); \
		BREAK=$(BREAK) RESOURCE=$(RESOURCE) AUDIENCE=$(AUDIENCE) AUTH_SERVER=$$as $(COMPOSE) up -d --no-deps --force-recreate mcp && \
		echo "resource: $(if $(RESOURCE),$(RESOURCE),（自分の URL）)"

# トンネルはそのままで、MCP サーバーだけ作り直して、自分宛てとみなす aud を変える。
# 本物のトークンが、よそ宛てに見えるようになる（aud が違うときの claude.ai を見る実験）。
# 例：make audience AUDIENCE=https://example.com/mcp ／ make audience（元に戻す）
audience:
	@as=$$($(call tunnel_url,cloudflared-as)); \
		BREAK=$(BREAK) RESOURCE=$(RESOURCE) AUDIENCE=$(AUDIENCE) AUTH_SERVER=$$as $(COMPOSE) up -d --no-deps --force-recreate mcp && \
		echo "audience: $(if $(AUDIENCE),$(AUDIENCE),（自分の URL）)"

smoke:
	sh deploy/local/smoke.sh

logs:
	$(COMPOSE) logs -f mcp keycloak nginx

# トンネルも含めて全部止める。試し終わったら必ず。
down:
	$(COMPOSE) --profile tunnel down
