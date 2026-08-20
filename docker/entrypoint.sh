#!/bin/sh
# 容器入口脚本：serve 启动服务；healthcheck 供 HEALTHCHECK 调用。
set -eu

case "${1:-serve}" in
  serve)
    exec /usr/local/bin/server
    ;;
  healthcheck)
    # 无网络外部依赖，仅探测本进程 HTTP 端口
    if command -v curl >/dev/null 2>&1; then
      curl -fsS "http://127.0.0.1:${PORT:-8080}/healthz" >/dev/null
    else
      # debian-slim 默认无 curl，用 bash 重定向探测
      (exec 3<>/dev/tcp/127.0.0.1/"${PORT:-8080}" && printf 'GET /healthz HTTP/1.0\r\n\r\n' >&3 && head -c 200 <&3 | grep -q '"status":"ok"') 2>/dev/null
    fi
    ;;
  *)
    echo "用法: entrypoint.sh [serve|healthcheck]" >&2
    exit 2
    ;;
esac
