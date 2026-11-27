#!/bin/sh
# realm を止める／戻す。使い方：realm-down.sh REALM on|off（on で止める）
# Keycloak で realm を無効にしても、メタデータは返ってしまう。なので、外からの入口（nginx）で 503 を返して、
# 認可サーバーが止まっているのと同じに見せる。MCP サーバーはコンテナの中から Keycloak を直接読むので、影響しない。
set -eu

REALM=${1:?realm を指定してください（例：tenant-a）}
DIR=$(dirname "$0")/down
COMPOSE="docker compose -f $(dirname "$0")/compose.yaml"

case ${2:?on か off を指定してください} in
on)
	cat > "$DIR/$REALM.conf" <<EOF
if (\$uri ~ "^(/realms/${REALM}/|/\.well-known/[^/]+/realms/${REALM}$)") { return 503; }
EOF
	;;
off) rm -f "$DIR/$REALM.conf" ;;
esac
$COMPOSE exec -T nginx nginx -s reload
# 読み直しの直後は、古いワーカーがまだ受けていることがあるので、少し待つ。
sleep 2
echo "止めている realm: $(ls "$DIR" | grep '\.conf$' | sed 's/\.conf$//' | paste -sd, - || true)"
