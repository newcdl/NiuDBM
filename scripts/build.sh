#!/bin/bash
# 牛库管家构建：前端产物 → Go 交叉编译 → 组装 fpk
#
# 用法：
#   scripts/build.sh                  检查 + 构建默认架构（amd64）
#   scripts/build.sh --arch arm64     构建 arm 包
#   scripts/build.sh --arch all       双架构
#   scripts/build.sh --skip-web       跳过前端构建（产物已就绪时）
#   scripts/build.sh --pack           检查通过后调用 fnpack 打包
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="$ROOT/niudbm"
WEB_DIR="$ROOT/web"
SERVER_DIR="$ROOT/server"
DIST_DIR="$ROOT/dist"
BUILD_DIR="$ROOT/.build"

# 本地无 go 时用镜像编译；可用 GO_IMAGE 覆盖
GO_IMAGE="${GO_IMAGE:-docker.m.daocloud.io/library/golang:1.24}"

ARCHS="amd64"
DO_PACK=0
SKIP_WEB=0

while [ $# -gt 0 ]; do
  case "$1" in
    --arch)      ARCHS="$2"; shift 2 ;;
    --arch=*)    ARCHS="${1#*=}"; shift ;;
    --pack)      DO_PACK=1; shift ;;
    --skip-web)  SKIP_WEB=1; shift ;;
    *)           echo "未知参数：$1" >&2; exit 2 ;;
  esac
done

[ "$ARCHS" = "all" ] && ARCHS="amd64 arm64"

VERSION="$(grep '^version=' "$APP_DIR/manifest" | cut -d= -f2)"

ok()   { echo "  [ok]   $*"; }
bad()  { echo "  [fail] $*"; FAIL=$((FAIL + 1)); }
skip() { echo "  [skip] $*"; }
FAIL=0

echo "牛库管家 v$VERSION · 架构 $ARCHS"
echo
echo "== 1. 静态检查 =="

for f in "$APP_DIR"/cmd/*; do
  [ -f "$f" ] || continue
  if bash -n "$f" 2>/dev/null; then ok "$(basename "$f") 语法"; else bad "$(basename "$f") 语法错误"; fi
done

if command -v python3 >/dev/null 2>&1; then
  for f in "$APP_DIR"/config/* "$APP_DIR"/app/ui/config; do
    [ -f "$f" ] || continue
    if python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$f" 2>/dev/null; then
      ok "JSON $(basename "$(dirname "$f")")/$(basename "$f")"
    else
      bad "JSON 解析失败：$f"
    fi
  done
else
  skip "未安装 python3，跳过 JSON 校验"
fi

for key in appname version display_name source platform os_min_version ctl_stop; do
  if grep -q "^${key}=" "$APP_DIR/manifest"; then ok "manifest.$key"; else bad "缺少 manifest.$key"; fi
done

if grep -q "^platform=all$" "$APP_DIR/manifest"; then
  bad "包含二进制的包不得使用 platform=all，必须按架构分别打包"
else
  ok "platform 已按架构区分"
fi

if grep -q "^service_port=" "$APP_DIR/manifest"; then
  bad "不应声明 service_port（走统一网关）"
else
  ok "未声明 service_port"
fi

for icon in ICON.PNG ICON_256.PNG; do
  if [ -s "$APP_DIR/$icon" ]; then ok "$icon"; else bad "缺少 $icon"; fi
done

for f in "$APP_DIR"/cmd/*; do
  [ -f "$f" ] || continue
  if [ -x "$f" ]; then ok "$(basename "$f") 可执行位"; else bad "$(basename "$f") 缺少可执行权限"; fi
done

if [ -f "$SERVER_DIR/internal/webui/dist/index.html" ]; then
  ok "前端产物已就绪"
else
  bad "缺少前端产物，请先执行 pnpm build"
fi

echo
if [ "$FAIL" -gt 0 ]; then
  echo "静态检查未通过：$FAIL 项失败"
  exit 1
fi

echo "== 2. 前端构建 =="
if [ "$SKIP_WEB" -eq 1 ]; then
  skip "--skip-web 已指定"
elif ! command -v pnpm >/dev/null 2>&1; then
  echo "  [fail] 未安装 pnpm，无法构建前端" >&2
  exit 1
elif ( cd "$WEB_DIR" && pnpm install --silent && pnpm build >/dev/null ); then
  ok "前端产物已更新"
else
  echo "  [fail] 前端构建失败" >&2
  exit 1
fi

echo "== 3. 编译 Go =="
mkdir -p "$DIST_DIR"
for arch in $ARCHS; do
  ldflags="-s -w -X main.version=$VERSION"
  if command -v go >/dev/null 2>&1; then
    if ( cd "$SERVER_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
        go build -trimpath -buildvcs=false -ldflags "$ldflags" \
        -o "$DIST_DIR/niudbm-linux-$arch" ./cmd/niudbm ); then
      ok "linux/${arch}（本地 go）"
    else
      echo "  [fail] linux/$arch 编译失败" >&2
      exit 1
    fi
  elif command -v docker >/dev/null 2>&1; then
    if docker run --rm --user "$(id -u):$(id -g)" \
        -e GOCACHE=/tmp/gc -e GOMODCACHE=/tmp/gm \
        -v "$ROOT:/src" -w /src/server "$GO_IMAGE" \
        sh -c "CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -buildvcs=false -ldflags '$ldflags' -o /src/dist/niudbm-linux-$arch ./cmd/niudbm"; then
      ok "linux/${arch}（${GO_IMAGE}）"
    else
      echo "  [fail] linux/$arch 编译失败" >&2
      exit 1
    fi
  else
    echo "  [fail] 需要本地 go 或 docker 才能编译" >&2
    exit 1
  fi
done

echo "== 4. 组装包目录 =="
for arch in $ARCHS; do
  case "$arch" in
    amd64) platform=x86 ;;
    arm64) platform=arm ;;
    *)     platform=x86 ;;
  esac

  pkg="$BUILD_DIR/pkg-$arch"
  rm -rf "$pkg"
  mkdir -p "$pkg/app"

  cp -R "$APP_DIR/config" "$APP_DIR/cmd" "$APP_DIR/wizard" "$pkg/"
  cp -R "$APP_DIR/app/ui" "$pkg/app/"
  cp "$APP_DIR/ICON.PNG" "$APP_DIR/ICON_256.PNG" "$pkg/"

  cp "$DIST_DIR/niudbm-linux-$arch" "$pkg/app/niudbm"
  chmod 755 "$pkg/app/niudbm"

  # manifest 按架构生成，源文件始终保持默认值，避免工作区被构建污染
  sed "s/^platform=.*/platform=$platform/" "$APP_DIR/manifest" > "$pkg/manifest"

  ok "${pkg}（platform=${platform}）"
done

echo "== 5. 打包 =="
if [ "$DO_PACK" -eq 0 ]; then
  skip "未指定 --pack，跳过 fnpack"
elif ! command -v fnpack >/dev/null 2>&1; then
  echo "  [fail] 未找到 fnpack，请从飞牛开发者中心获取后重试" >&2
  exit 1
else
  for arch in $ARCHS; do
    pkg="$BUILD_DIR/pkg-$arch"
    if ( cd "$pkg" && fnpack build >/dev/null ); then
      produced="$(ls "$pkg"/*.fpk 2>/dev/null | head -1)"
      if [ -z "$produced" ]; then
        echo "  [fail] fnpack 未生成 fpk" >&2
        exit 1
      fi
      target="$DIST_DIR/niudbm-$VERSION-$arch.fpk"
      mv "$produced" "$target"
      if command -v sha256sum >/dev/null 2>&1; then
        ( cd "$DIST_DIR" && sha256sum "niudbm-$VERSION-$arch.fpk" > "niudbm-$VERSION-$arch-SHA256SUMS.txt" )
      elif command -v shasum >/dev/null 2>&1; then
        ( cd "$DIST_DIR" && shasum -a 256 "niudbm-$VERSION-$arch.fpk" > "niudbm-$VERSION-$arch-SHA256SUMS.txt" )
      fi
      ok "$target"
    else
      echo "  [fail] fnpack 打包失败（${arch}）" >&2
      exit 1
    fi
  done
fi

echo
echo "完成。产物目录：$DIST_DIR"
