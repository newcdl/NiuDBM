#!/bin/bash
# 牛库管家构建检查与打包
# 用法：scripts/build.sh        仅检查
#       scripts/build.sh --pack 检查通过后调用 fnpack 打包
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="$ROOT/niudbm"
FAIL=0

ok()  { echo "  [ok]   $*"; }
bad() { echo "  [fail] $*"; FAIL=$((FAIL + 1)); }
skip(){ echo "  [skip] $*"; }

echo "== 1. 脚本语法 =="
for f in "$APP_DIR"/cmd/* "$APP_DIR"/app/server/*.sh "$APP_DIR"/app/ui/index.cgi; do
  [ -f "$f" ] || continue
  if bash -n "$f" 2>/dev/null; then
    ok "$(basename "$f")"
  else
    bad "$(basename "$f") 存在语法错误"
  fi
done

echo "== 2. 配置 JSON =="
if command -v python3 >/dev/null 2>&1; then
  for f in "$APP_DIR"/config/* "$APP_DIR"/app/ui/config; do
    [ -f "$f" ] || continue
    if python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$f" 2>/dev/null; then
      ok "$(basename "$f")"
    else
      bad "$(basename "$(dirname "$f")")/$(basename "$f") JSON 解析失败"
    fi
  done
else
  skip "未安装 python3，跳过 JSON 校验"
fi

echo "== 3. manifest 字段 =="
for key in appname version display_name source platform os_min_version ctl_stop; do
  if grep -q "^${key}=" "$APP_DIR/manifest"; then
    ok "$key"
  else
    bad "缺少必需字段 $key"
  fi
done

if grep -q "^service_port=" "$APP_DIR/manifest"; then
  bad "不应声明 service_port（当前走 CGI/网关，由平台分配）"
else
  ok "未声明 service_port"
fi

if grep -q "^platform=all$" "$APP_DIR/manifest"; then
  ok "platform=all（本包无架构相关二进制，符合规则）"
fi

echo "== 4. 图标 =="
for icon in ICON.PNG ICON_256.PNG; do
  if [ -s "$APP_DIR/$icon" ]; then
    ok "$icon"
  else
    bad "缺少 $icon"
  fi
done

echo "== 5. 可执行位 =="
for f in "$APP_DIR"/cmd/* "$APP_DIR"/app/server/*.sh "$APP_DIR"/app/ui/index.cgi; do
  [ -f "$f" ] || continue
  if [ -x "$f" ]; then ok "$(basename "$f")"; else bad "$(basename "$f") 缺少可执行权限"; fi
done

echo
if [ "$FAIL" -gt 0 ]; then
  echo "检查未通过：$FAIL 项失败"
  exit 1
fi
echo "检查全部通过"

if [ "${1:-}" = "--pack" ]; then
  echo
  if command -v fnpack >/dev/null 2>&1; then
    VERSION="$(grep '^version=' "$APP_DIR/manifest" | cut -d= -f2)"
    ( cd "$APP_DIR" && fnpack build ) || exit 1
    echo "产物：$APP_DIR/niudbm-${VERSION}-all.fpk"
    if command -v shasum >/dev/null 2>&1; then
      ( cd "$APP_DIR" && shasum -a 256 "niudbm-${VERSION}-all.fpk" > "niudbm-${VERSION}-SHA256SUMS.txt" )
      echo "校验文件已生成"
    fi
  else
    echo "未找到 fnpack，请先从飞牛开发者中心获取打包工具后重试"
    exit 1
  fi
fi
