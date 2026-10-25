#!/bin/bash
# NiuDBM 入口页（CGI）
# 平台按 /cgi/ThirdParty/niudbm/index.cgi/ 访问本文件，因此资源一律使用相对路径。
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
exec /bin/bash "$HERE/../server/diagnose.sh" --html
