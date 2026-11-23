COMPOSE := docker compose -f deploy/local/compose.yaml

.PHONY: up tunnel smoke logs down

# MCP サーバーと nginx を立ち上げる。壊すモードは make up BREAK=bad-version のように渡す。
up:
	BREAK=$(BREAK) $(COMPOSE) up -d --build mcp nginx

# トンネルも立てて、claude.ai に登録する URL を出す。
# cloudflared は作り直す（使い回すと、ログに残った前回の URL を拾ってしまう）。
tunnel: up
	$(COMPOSE) --profile tunnel up -d --force-recreate cloudflared
	@until url=$$($(COMPOSE) logs cloudflared 2>&1 | grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' | tail -1); [ -n "$$url" ]; do sleep 1; done; \
		echo "claude.ai に登録する URL: $$url/mcp"

smoke:
	sh deploy/local/smoke.sh

logs:
	$(COMPOSE) logs -f mcp nginx

# トンネルも含めて全部止める。試し終わったら必ず。
down:
	$(COMPOSE) --profile tunnel down
