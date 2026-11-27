COMPOSE := docker compose -f deploy/local/compose.yaml

# MCP サーバーが信じる realm（テナント）。メタデータの authorization_servers にも、この順で並べる。
REALMS ?= tenant-a,tenant-b

# cloudflared のログから、quick tunnel の URL が出るまで待って取り出す。
tunnel_url = until url=$$($(COMPOSE) logs $(1) 2>&1 | grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' | tail -1); [ -n "$$url" ]; do sleep 1; done; echo $$url

# MCP サーバーに渡す、認可サーバー（issuer）の一覧と、それぞれのメタデータの置き場所（コンテナの中から読む）。
# $(1) は、認可サーバーの外から見える URL。
mcp_env = ISSUERS=$$(echo $(REALMS) | tr ',' '\n' | sed "s\#^\#$(1)/realms/\#" | paste -sd, -) \
	AS_METADATA=$$(echo $(REALMS) | tr ',' '\n' | sed "s\#.*\#http://keycloak:8080/realms/&/.well-known/openid-configuration\#" | paste -sd, -) \
	AUTH_SERVER=$(1) BREAK=$(BREAK) RESOURCE=$(RESOURCE) AUDIENCE=$(AUDIENCE)

.PHONY: up tunnel order realm-off realm-on smoke logs down

# MCP サーバー・Postgres・Keycloak・nginx を立ち上げて、Keycloak に realm を作る。
up:
	$(call mcp_env,http://127.0.0.1:8082) $(COMPOSE) up -d --build mcp db keycloak nginx
	sh deploy/local/keycloak-setup.sh

# トンネルを2本立てて、claude.ai に登録する URL を出す。
# どちらの URL もトンネルを立てるまでわからないので、先に2本とも立ててから、
# 認可サーバーの URL を Keycloak（外から見える URL）と MCP サーバー（メタデータに書く issuer）に渡して作り直し、
# MCP サーバーの URL を Keycloak の aud（Audience マッパー）に入れる。
# Keycloak は作り直すと中身が消えるので、realm もここで作り直す（パスワードも作り直して表示する）。
# cloudflared は作り直す（使い回すと、ログに残った前回の URL を拾ってしまう）。
# --no-deps は、依存先の mcp や keycloak を、AUTH_SERVER のない既定の設定で作り直させないため。
tunnel: up
	$(COMPOSE) --profile tunnel up -d --no-deps --force-recreate cloudflared-as cloudflared-mcp
	@as=$$($(call tunnel_url,cloudflared-as)); \
		mcp=$$($(call tunnel_url,cloudflared-mcp)); \
		$(call mcp_env,$$as) $(COMPOSE) up -d --no-deps mcp keycloak && \
		MCP_URL=$$mcp/mcp sh deploy/local/keycloak-setup.sh && \
		$(COMPOSE) restart nginx && \
		echo "認可サーバー: $(REALMS)（$$as/realms/…）" && \
		echo "claude.ai に登録する URL: $$mcp/mcp"

# トンネルはそのままで、MCP サーバーだけ作り直して、メタデータに並べる認可サーバーの順番を変える。
# 例：make order REALMS=tenant-b,tenant-a ／ make order（元に戻す）
order:
	@as=$$($(call tunnel_url,cloudflared-as)); \
		$(call mcp_env,$$as) $(COMPOSE) up -d --no-deps --force-recreate mcp && \
		echo "authorization_servers の順番: $(REALMS)"

# realm を止める／戻す（nginx で 503 を返す）。1つ目の認可サーバーが止まっていたら、claude.ai が2つ目へ行くかを見る。
# 例：make realm-off REALM=tenant-a ／ make realm-on REALM=tenant-a
realm-off:
	sh deploy/local/realm-down.sh $(REALM) on
realm-on:
	sh deploy/local/realm-down.sh $(REALM) off

smoke:
	sh deploy/local/smoke.sh

logs:
	$(COMPOSE) logs -f mcp keycloak nginx

# トンネルも含めて全部止める。試し終わったら必ず。
down:
	$(COMPOSE) --profile tunnel down
