#!/bin/bash
# 牛库管家构建：静态检查 → 前端构建 → 后端测试 → 双架构编译 → 组装 fpk
#
# 默认同时产出 amd64(x86) 与 arm64(arm) 两个包：发布必须成对，避免漏包。
# 引擎不随包分发：数据库以 Docker 官方镜像运行，版本清单见 server/internal/images。
# 测试不通过直接中止，不出包。
#
# 用法：
#   scripts/build.sh                  测试 + 双架构构建
#   scripts/build.sh --arch amd64     只构建 x86 包（排查问题时用）
#   scripts/build.sh --arch arm64     只构建 arm 包
#   scripts/build.sh --skip-web       跳过前端构建（产物已就绪时）
#   scripts/build.sh --skip-test      跳过后端测试（仅本地调试，禁止用于发布）
#   scripts/build.sh --pack           检查通过后调用 fnpack 打包
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="$ROOT/niudbm"
WEB_DIR="$ROOT/web"
SERVER_DIR="$ROOT/server"
DIST_DIR="$ROOT/dist"
BUILD_DIR="$ROOT/.build"
CACHE_DIR="$ROOT/.cache"

# 本地无 go 时用镜像编译；可用 GO_IMAGE 覆盖
GO_IMAGE="${GO_IMAGE:-docker.m.daocloud.io/library/golang:1.24}"
GO_PROXY="${GO_PROXY:-https://goproxy.cn,direct}"
# 同一个 tag 下 amd64/arm64 是两个镜像，不指定平台就会用到上次拉下来的那个。
# 在 x86 机器上误用 arm64 工具链时，编译与测试全变成模拟执行，慢到会被误判成卡死。
case "$(uname -m)" in
  x86_64|amd64)  HOST_PLATFORM=linux/amd64 ;;
  aarch64|arm64) HOST_PLATFORM=linux/arm64 ;;
  *)             HOST_PLATFORM=linux/amd64 ;;
esac

ARCHS="all"
DO_PACK=0
SKIP_WEB=0
SKIP_TEST=0

while [ $# -gt 0 ]; do
  case "$1" in
    --arch)      ARCHS="$2"; shift 2 ;;
    --arch=*)    ARCHS="${1#*=}"; shift ;;
    --pack)      DO_PACK=1; shift ;;
    --skip-web)  SKIP_WEB=1; shift ;;
    --skip-test) SKIP_TEST=1; shift ;;
    *)           echo "未知参数：$1" >&2; exit 2 ;;
  esac
done

[ "$ARCHS" = "all" ] && ARCHS="amd64 arm64"

# fnpack 是飞牛官方的打包工具，不在 PATH 时也认 ~/bin 下的安装
FNPACK="$(command -v fnpack 2>/dev/null || true)"
if [ -z "$FNPACK" ] && [ -x "$HOME/bin/fnpack" ]; then
  FNPACK="$HOME/bin/fnpack"
fi

VERSION="$(grep '^version=' "$APP_DIR/manifest" | cut -d= -f2)"

ok()   { echo "  [ok]   $*"; }
bad()  { echo "  [fail] $*"; FAIL=$((FAIL + 1)); }
skip() { echo "  [skip] $*"; }
FAIL=0

echo "牛库管家 v$VERSION · 架构 $ARCHS"
echo
echo "== 1. 静态检查 =="

for f in "${APP_DIR}"/cmd/* "${ROOT}"/scripts/*.sh; do
  [ -f "${f}" ] || continue
  if bash -n "$f" 2>/dev/null; then ok "$(basename "$f") 语法"; else bad "$(basename "$f") 语法错误"; fi
done

if command -v python3 >/dev/null 2>&1; then
  # 向导文件是 JSON（无扩展名）；写坏了 fnpack 不报错，真机上卸载时才炸
  for f in "$APP_DIR"/config/* "$APP_DIR"/app/ui/config "$APP_DIR"/wizard/*; do
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

# 入口声明写错时 fnpack 会直接报错，真机上也打不开；这里提前拦住
if [ -f "$APP_DIR/app/ui/config" ] && command -v python3 >/dev/null 2>&1; then
  entry="$(grep '^desktop_applaunchname=' "$APP_DIR/manifest" | head -1 | cut -d= -f2 | tr -d ' ')"
  if [ -z "$entry" ]; then
    bad "manifest 缺少 desktop_applaunchname"
  elif ui_info="$(python3 "$ROOT/scripts/check-ui-config.py" "$APP_DIR/app/ui/config" "$entry" 2>&1)"; then
    ok "入口声明：${ui_info}"
  else
    bad "入口声明有误：${ui_info}"
  fi
fi

for icon in ICON.PNG ICON_256.PNG; do
  if [ -s "$APP_DIR/$icon" ]; then ok "$icon"; else bad "缺少 $icon"; fi
done

for f in "$APP_DIR"/cmd/*; do
  [ -f "$f" ] || continue
  if [ -x "$f" ]; then ok "$(basename "$f") 可执行位"; else bad "$(basename "$f") 缺少可执行权限"; fi
done

# 前端产物在下一步才生成，只有跳过构建时才要求它已经存在
if [ "$SKIP_WEB" -eq 1 ]; then
  if [ -f "$SERVER_DIR/internal/webui/dist/index.html" ]; then
    ok "前端产物已就绪（--skip-web）"
  else
    bad "指定了 --skip-web 但缺少前端产物，请先执行 pnpm build"
  fi
fi

echo
if [ "$FAIL" -gt 0 ]; then
  echo "静态检查未通过：$FAIL 项失败"
  exit 1
fi

echo "== 2. 前端构建 =="
if [ "$SKIP_WEB" -eq 1 ]; then
  skip "--skip-web 已指定"
elif [ -x "$WEB_DIR/node_modules/.bin/vue-tsc" ] && [ -x "$WEB_DIR/node_modules/.bin/vite" ]; then
  # 本地已有锁定依赖时直接调用可执行文件，避免 pnpm 在无 TTY 环境下尝试重装。
  if ( cd "$WEB_DIR" && ./node_modules/.bin/vue-tsc --noEmit && ./node_modules/.bin/vite build >/dev/null ); then
    ok "前端产物已更新（本地依赖）"
  else
    echo "  [fail] 前端构建失败" >&2
    exit 1
  fi
elif ! command -v pnpm >/dev/null 2>&1; then
  echo "  [fail] 未安装 pnpm，且项目没有可用的 node_modules" >&2
  exit 1
elif ( cd "$WEB_DIR" && CI=true pnpm install --silent && pnpm build >/dev/null ); then
  ok "前端产物已更新"
else
  echo "  [fail] 前端构建失败" >&2
  exit 1
fi

echo "== 3. 后端测试 =="
# 测试不过就不该出包：这里失败直接退出，不进入编译。
if [ "$SKIP_TEST" -eq 1 ]; then
  skip "--skip-test 已指定（发布包不要用这个开关）"
else
  mkdir -p "$BUILD_DIR" "$CACHE_DIR"
  TEST_LOG="$BUILD_DIR/test.log"
  if command -v go >/dev/null 2>&1; then
    if ( cd "${SERVER_DIR}" && test -z "$(gofmt -l cmd internal)" && go vet -buildvcs=false ./... && go test -buildvcs=false -count=1 ./... ) >"$TEST_LOG" 2>&1; then
      ok "go vet + go test 全部通过（本地 go）"
      grep -E '^(ok|\?|---)' "$TEST_LOG" | sed 's/^/         /'
    else
      echo "  [fail] 后端测试未通过，详见 $TEST_LOG" >&2
      tail -n 30 "$TEST_LOG" >&2
      exit 1
    fi
  elif command -v docker >/dev/null 2>&1; then
    if docker run --rm --platform "${HOST_PLATFORM}" --user "$(id -u):$(id -g)" \
        -e HOME=/tmp -e GOCACHE=/cache/gocache -e GOMODCACHE=/cache/gomod -e GOPROXY="$GO_PROXY" \
        -v "$ROOT:/src" -v "$CACHE_DIR:/cache" -w /src/server "$GO_IMAGE" \
        sh -c 'test -z "$(gofmt -l cmd internal)" && go vet -buildvcs=false ./... && go test -buildvcs=false -count=1 ./...' >"${TEST_LOG}" 2>&1; then
      ok "go vet + go test 全部通过（${GO_IMAGE}）"
      grep -E '^(ok|\?|---)' "$TEST_LOG" | sed 's/^/         /'
    else
      echo "  [fail] 后端测试未通过，详见 $TEST_LOG" >&2
      tail -n 30 "$TEST_LOG" >&2
      exit 1
    fi
  else
    echo "  [fail] 需要本地 go 或 docker 才能跑测试" >&2
    exit 1
  fi
fi

echo "== 4. 编译 Go =="
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
    mkdir -p "$CACHE_DIR"
    if docker run --rm --platform "${HOST_PLATFORM}" --user "$(id -u):$(id -g)" \
        -e HOME=/tmp -e GOCACHE=/cache/gocache -e GOMODCACHE=/cache/gomod -e GOPROXY="$GO_PROXY" \
        -v "$ROOT:/src" -v "$CACHE_DIR:/cache" -w /src/server "$GO_IMAGE" \
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

echo "== 5. 组装包目录 =="
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

  # 引擎不进包：数据库以官方镜像运行，安装包里只有程序本身
  # manifest 按架构生成，源文件始终保持默认值，避免工作区被构建污染
  sed "s/^platform=.*/platform=$platform/" "$APP_DIR/manifest" > "$pkg/manifest"

  ok "${pkg}（platform=${platform}）"
done

echo "== 6. 冒烟测试 =="
# 单元测试跑的是源码，这里跑的是「刚编出来的那个二进制」：
# 能不能启动、embed 的前端在不在、SQLite 能不能读写。
if [ "$SKIP_TEST" -eq 1 ]; then
  skip "--skip-test 已指定（发布包不要用这个开关）"
else
  for arch in $ARCHS; do
    SMOKE_LOG="$BUILD_DIR/smoke-$arch.log"
    if "${ROOT}/scripts/smoke.sh" "${DIST_DIR}/niudbm-linux-${arch}" "${arch}" >"${SMOKE_LOG}" 2>&1; then
      rc=0
    else
      rc=$?
    fi
    if [ "$rc" -eq 0 ]; then
      ok "linux/${arch} 冒烟通过"
      sed 's/^/         /' "$SMOKE_LOG"
    elif [ "${rc}" -eq 2 ] && [ "${DO_PACK}" -eq 0 ]; then
      skip "linux/${arch} 冒烟跳过：$(head -n 1 "${SMOKE_LOG}")"
    else
      echo "  [fail] linux/${arch} 冒烟失败，详见 $SMOKE_LOG" >&2
      tail -n 30 "$SMOKE_LOG" >&2
      exit 1
    fi
  done
fi

echo "== 7. 打包 =="
if [ "$DO_PACK" -eq 0 ]; then
  skip "未指定 --pack，跳过 fnpack"
elif [ -z "$FNPACK" ]; then
  echo "  [fail] 未找到 fnpack，请从飞牛开发者中心获取后重试" >&2
  exit 1
else
  for arch in $ARCHS; do
    pkg="$BUILD_DIR/pkg-$arch"
    FPLOG="$BUILD_DIR/fnpack-$arch.log"
    # fnpack 把 fpk 生成在「当前目录」，所以必须先进包目录再调用
    if ( cd "$pkg" && "$FNPACK" build >"$FPLOG" 2>&1 ); then
      produced="$(ls "$pkg"/*.fpk 2>/dev/null | head -1)"
      if [ -z "$produced" ]; then
        echo "  [fail] fnpack 未生成 fpk（详见 $FPLOG）" >&2
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
      echo "  [fail] fnpack 打包失败（${arch}，详见 $FPLOG）" >&2
      tail -n 15 "$FPLOG" >&2
      exit 1
    fi
  done
fi

echo "== 8. 产物校验 =="
# 二进制架构、静态链接、包内 manifest 是上机后最容易出问题的地方，出包前先核一遍。
for arch in $ARCHS; do
  bin="$DIST_DIR/niudbm-linux-$arch"
  pkg="$BUILD_DIR/pkg-$arch"
  case "$arch" in
    amd64) want="x86-64"; want_platform=x86 ;;
    arm64) want="ARM aarch64"; want_platform=arm ;;
    *)     want=""; want_platform="" ;;
  esac

  if [ ! -s "$bin" ]; then
    bad "缺少二进制：$bin"
  elif [ "$(wc -c < "$bin" | tr -d ' ')" -lt 1048576 ]; then
    bad "$(basename "$bin") 体积异常偏小"
  else
    ok "$(basename "$bin") $(wc -c < "$bin" | tr -d ' ') 字节"
  fi

  if [ -n "$want" ] && [ -s "$bin" ]; then
    if command -v file >/dev/null 2>&1; then
      desc="$(file -b "$bin" 2>/dev/null || echo '')"
      case "$desc" in
        *"$want"*) ok "架构匹配 ${want}" ;;
        *)          bad "架构不符（期望 ${want}）：$desc" ;;
      esac
      case "$desc" in
        *"statically linked"*) ok "静态链接（无 glibc 依赖）" ;;
        *)                     skip "非静态链接：$desc" ;;
      esac
    else
      skip "无 file 命令，跳过架构校验"
    fi
  fi

  if [ -n "$want_platform" ] && [ -f "$pkg/manifest" ]; then
    if grep -q "^platform=$want_platform$" "$pkg/manifest"; then
      ok "manifest platform=${want_platform}"
    else
      bad "manifest platform 不是 ${want_platform}：$(grep '^platform=' "$pkg/manifest")"
    fi
    if [ -x "$pkg/app/niudbm" ]; then ok "包内二进制可执行" ; else bad "包内 niudbm 缺少可执行位"; fi
  fi

  # 引擎应该是空的：误把引擎或镜像 tar 打进包会让安装包从 20MB 变成几百 MB
  if [ -d "$pkg/app/engines" ] && [ -n "$(ls -A "$pkg/app/engines" 2>/dev/null)" ]; then
    bad "包内不应包含引擎：$pkg/app/engines"
  else
    ok "包内不含引擎/镜像（数据库用官方镜像）"
  fi

done

if [ "$FAIL" -gt 0 ]; then
  echo
  echo "产物校验未通过：$FAIL 项失败"
  exit 1
fi

echo
echo "完成。产物目录：$DIST_DIR"
ls -1 "$DIST_DIR"/niudbm-linux-* 2>/dev/null | sed 's/^/  /'
ls -1 "$DIST_DIR"/*.fpk 2>/dev/null | sed 's/^/  /'
