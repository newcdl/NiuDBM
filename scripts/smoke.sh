#!/bin/bash
# 对单个打包产物做冒烟测试：在一个干净的 linux 容器里真跑一遍。
#
# 用法：scripts/smoke.sh <二进制路径> <arch>
# 退出码：0 通过 / 1 失败 / 2 跳过（本机无法执行该架构）
#
# 之所以要走容器：交叉编译出来的 ELF 在 macOS 上没法直接执行，
# 只有真跑过才能确认 embed 的前端、路径处理与平台接入都没问题。
#
# 这一版本只有 MySQL + Docker：
#   默认只跑不依赖容器的部分（接口契约、参数校验、权限、设置）。
#   设置 SMOKE_DOCKER=1 且宿主有 docker.sock 时，额外把 socket 与 docker CLI
#   一起挂进去，跑完整的建服务 → 真实 TCP 认证 → 备份恢复 → 删除回收。
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

EXTRA=()
if [ "${SMOKE_DOCKER:-0}" = "1" ] && [ -S /var/run/docker.sock ] && command -v docker >/dev/null 2>&1; then
  # 只挂 socket：容器里的 docker CLI 由 smoke 脚本自己装（宿主的是 macOS 二进制，
  # 挂进 Linux 容器跑不起来），这样被测程序就能像在 NAS 上一样真的拉起 MySQL 容器。
  EXTRA+=(-v /var/run/docker.sock:/var/run/docker.sock --network host)
  EXTRA+=(-e SMOKE_DOCKER=1)
fi

out="$(docker run --rm --platform "linux/$ARCH" \
  -v "$BIN:/app/niudbm:ro" \
  -v "${ROOT}/scripts/smoke-in-container.sh:/smoke.sh:ro" \
  -v "${ROOT}:/src:ro" -v "${ROOT}/.cache:/cache" \
  ${EXTRA[@]+"${EXTRA[@]}"} \
  -e GOCACHE=/cache/gocache -e GOMODCACHE=/cache/gomod -e GOPROXY="${GO_PROXY:-https://goproxy.cn,direct}" \
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
