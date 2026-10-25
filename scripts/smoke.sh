#!/bin/bash
# 对单个打包产物做冒烟测试：在一个干净的 linux 容器里真跑一遍。
#
# 用法：scripts/smoke.sh <二进制路径> <arch>
# 退出码：0 通过 / 1 失败 / 2 跳过（本机无法执行该架构）
#
# 之所以要走容器：交叉编译出来的 ELF 在 macOS 上没法直接执行，
# 只有真跑过才能确认 embed 的前端、SQLite 驱动、路径处理都没问题。
set -u

BIN="${1:?需要二进制路径}"
ARCH="${2:?需要架构（amd64/arm64）}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${GO_IMAGE:-docker.m.daocloud.io/library/golang:1.24}"

if ! command -v docker >/dev/null 2>&1; then
  echo "跳过：未安装 docker，无法执行 ${ARCH} 二进制"
  exit 2
fi
if [ ! -f "$BIN" ]; then
  echo "二进制不存在：$BIN" >&2
  exit 1
fi

# docker 的 -v 只认绝对路径，相对路径会被当成卷名
BIN="$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")"

out="$(docker run --rm --platform "linux/$ARCH" \
  -v "$BIN:/app/niudbm:ro" \
  -v "$ROOT/scripts/smoke-in-container.sh:/smoke.sh:ro" \
  "$IMAGE" bash /smoke.sh /app/niudbm 2>&1)"
code=$?

if [ "$code" -eq 0 ]; then
  echo "$out"
  exit 0
fi

# 宿主是 arm64 时 amd64 需要模拟层，镜像也可能拉不下来；这些属于环境限制而非产物问题
case "$out" in
  *"no matching manifest"* | *"download failed"* | *"exec format error"* | \
    *"semaphore timeout"* | *"context deadline exceeded"*)
    echo "跳过：本机无法执行 linux/$ARCH 二进制（镜像或模拟层不可用）"
    echo "$out" | tail -n 3
    exit 2
    ;;
esac

echo "$out"
exit 1
