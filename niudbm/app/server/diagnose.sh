#!/bin/bash
# NiuDBM 平台能力自检
# 用途：在真实 fnOS 设备上验证目录映射、权限边界、运行身份与端口可用性
# 用法：diagnose.sh            输出纯文本
#       diagnose.sh --html     输出 CGI 响应（供 index.cgi 调用）
# 注意：本脚本不读取、不打印任何凭据内容，仅判断是否存在。
set -u

MODE="text"
for arg in "$@"; do
  case "$arg" in
    --html) MODE="html" ;;
    --text) MODE="text" ;;
  esac
done

APP="niudbm"
BASE="/var/apps/$APP"

# CGI 环境或手工执行时 TRIM_* 可能不存在，按平台默认布局回退，并在报告中标注
TRIM_APPDEST="${TRIM_APPDEST:-$BASE/target}"
TRIM_PKGVAR="${TRIM_PKGVAR:-$BASE/var}"
TRIM_PKGETC="${TRIM_PKGETC:-$BASE/etc}"
TRIM_PKGTMP="${TRIM_PKGTMP:-$BASE/tmp}"
TRIM_PKGHOME="${TRIM_PKGHOME:-$BASE/home}"

PASS=0
WARN=0
FAIL=0
ROWS="$(mktemp -t niudbm_diag.XXXXXX 2>/dev/null || echo "/tmp/niudbm_diag.$$")"
: > "$ROWS"

emit() {
  # $1=结论 $2=检查项 $3=详情
  case "$1" in
    PASS) PASS=$((PASS + 1)) ;;
    WARN) WARN=$((WARN + 1)) ;;
    *)    FAIL=$((FAIL + 1)) ;;
  esac
  printf '%s|%s|%s\n' "$1" "$2" "$3" >> "$ROWS"
}

try_write() {
  # 返回 0 可写，1 不可写
  local target="$1/probe.$$"
  if ( : > "$target" ) 2>/dev/null; then
    rm -f "$target" 2>/dev/null
    return 0
  fi
  return 1
}

check_writable() {
  local path="$1" label="$2" real
  if [ ! -d "$path" ]; then
    emit FAIL "$label" "目录不存在（期望：${path}）"
    return
  fi
  real="$(readlink -f "$path" 2>/dev/null || echo "$path")"
  if try_write "$path"; then
    emit PASS "$label" "可读写 · $real"
  else
    emit FAIL "$label" "目录存在但不可写 · $real"
  fi
}

check_denied() {
  # 期望访问被拒绝（越权边界验证）
  local path="$1" label="$2"
  if [ ! -e "$path" ]; then
    emit PASS "$label" "路径不存在，天然无法访问"
    return
  fi
  # socket 不做写入探测，只看权限位，避免污染服务端的连接
  if [ -S "$path" ]; then
    if [ -w "$path" ]; then
      emit FAIL "$label" "socket 对当前用户可写，存在越权风险 · $path"
    else
      emit PASS "$label" "socket 不可写，边界有效"
    fi
    return
  fi
  if try_write "$path"; then
    emit FAIL "$label" "意外写入成功，存在越权风险 · $path"
  else
    emit PASS "$label" "写入被拒绝，边界有效"
  fi
}

# ---------- 1. 运行身份 ----------
UID_NOW="$(id -u 2>/dev/null || echo '?')"
GID_NOW="$(id -g 2>/dev/null || echo '?')"
NAME_NOW="$(id -un 2>/dev/null || echo '?')"

if [ "$UID_NOW" = "0" ]; then
  emit FAIL "运行身份" "当前以 root 运行（uid=0），不符合最小权限要求"
else
  emit PASS "运行身份" "uid=$UID_NOW gid=$GID_NOW user=$NAME_NOW"
fi

if [ -n "${TRIM_UID:-}" ]; then
  if [ "$UID_NOW" = "$TRIM_UID" ]; then
    emit PASS "身份与声明一致" "TRIM_UID=$TRIM_UID 与实际一致"
  else
    emit WARN "身份与声明不一致" "TRIM_UID=${TRIM_UID}，实际 uid=$UID_NOW"
  fi
else
  emit WARN "身份变量缺失" "未注入 TRIM_UID（CGI 环境可能不提供）"
fi

if [ -n "${TRIM_RUN_USERNAME:-}" ] && [ "$TRIM_RUN_USERNAME" = "root" ] && [ "$UID_NOW" != "0" ]; then
  emit WARN "脚本执行身份" "脚本曾以 root 执行，后续需降权"
fi

# ---------- 2. 目录映射 ----------
check_writable "$TRIM_PKGVAR"  "数据目录 var"
check_writable "$TRIM_PKGETC"  "配置目录 etc"
check_writable "$TRIM_PKGTMP"  "临时目录 tmp"
check_writable "$TRIM_APPDEST" "安装目录 target"
[ -d "$TRIM_PKGHOME" ] && check_writable "$TRIM_PKGHOME" "用户目录 home"

# ---------- 3. 越权边界 ----------
check_denied "/etc" "写入 /etc"
check_denied "/var/run/docker.sock" "访问 Docker Socket"
check_denied "/var/apps" "写入其他应用根目录"

# ---------- 4. 共享目录（data-share） ----------
if [ -n "${TRIM_DATA_SHARE_PATHS:-}" ]; then
  IFS=':' read -r -a SHARES <<< "$TRIM_DATA_SHARE_PATHS"
  for s in "${SHARES[@]}"; do
    [ -n "$s" ] && check_writable "$s" "共享目录 $(basename "$s")"
  done
else
  emit WARN "共享目录变量" "未注入 TRIM_DATA_SHARE_PATHS，无法验证 data-share"
fi

if [ -d "$BASE/share" ]; then
  for link in "$BASE/share"/*; do
    [ -e "$link" ] || continue
    real="$(readlink -f "$link" 2>/dev/null || echo "$link")"
    emit PASS "共享目录软链 $(basename "$link")" "$real"
  done
fi

# ---------- 5. 系统环境 ----------
ARCH_NOW="$(uname -m 2>/dev/null || echo '?')"
if [ -n "${TRIM_SYS_ARCH:-}" ] && [ "$TRIM_SYS_ARCH" != "$ARCH_NOW" ]; then
  emit WARN "架构一致性" "TRIM_SYS_ARCH=${TRIM_SYS_ARCH}，uname=${ARCH_NOW}"
else
  emit PASS "架构" "${ARCH_NOW}（TRIM_SYS_ARCH=${TRIM_SYS_ARCH:-未注入}）"
fi

if [ -r /etc/os-release ]; then
  OS_NAME="$(. /etc/os-release 2>/dev/null; echo "${PRETTY_NAME:-unknown}")"
  emit PASS "操作系统" "$OS_NAME"
fi

GLIBC_VER="$(ldd --version 2>/dev/null | head -n 1)"
if [ -n "$GLIBC_VER" ]; then
  emit PASS "C 运行库" "$GLIBC_VER"
else
  emit WARN "C 运行库" "无法读取 ldd 版本，引擎兼容性判断会受影响"
fi

if [ -r /proc/meminfo ]; then
  MEM_MB="$(awk '/MemTotal/ {printf "%d", $2/1024}' /proc/meminfo 2>/dev/null)"
  CPU_N="$(nproc 2>/dev/null || echo '?')"
  emit PASS "硬件资源" "内存 ${MEM_MB:-?} MB · CPU ${CPU_N} 核"
fi

# ---------- 6. 端口可用性 ----------
PORT_HIT=""
for p in 33000 33001 33002 33003 33004; do
  if command -v ss >/dev/null 2>&1; then
    ss -ltn 2>/dev/null | grep -q ":$p " && PORT_HIT="$PORT_HIT $p"
  fi
done

if ! command -v ss >/dev/null 2>&1; then
  emit WARN "端口探测" "系统无 ss 命令，请手工确认 33000-33099 未被占用"
elif [ -z "$PORT_HIT" ]; then
  emit PASS "端口池" "33000-33004 均空闲"
else
  emit WARN "端口池" "以下端口已被占用：$PORT_HIT"
fi

# ---------- 7. 平台注入变量 ----------
TRIM_COUNT="$(env 2>/dev/null | grep -c '^TRIM_')"
if [ "$TRIM_COUNT" -gt 0 ]; then
  emit PASS "平台变量注入" "共注入 $TRIM_COUNT 个 TRIM_* 变量"
  while IFS= read -r line; do
    case "$line" in
      TRIM_API_TOKEN=*) emit PASS "TRIM_API_TOKEN" "已注入（内容不展示）" ;;
      TRIM_APPNAME=*|TRIM_APPVER=*|TRIM_APP_STATUS=*|TRIM_SYS_VERSION=*)
        KEY="${line%%=*}"
        VAL="${line#*=}"
        emit PASS "$KEY" "${VAL:-空}"
        ;;
    esac
  done <<< "$(env 2>/dev/null | grep '^TRIM_')"
else
  emit WARN "平台变量注入" "未检测到 TRIM_*，当前可能不在应用运行环境内"
fi

# ---------- 8. 生命周期标记 ----------
if [ -f "$TRIM_PKGVAR/installed_at" ]; then
  emit PASS "安装标记" "$(cat "$TRIM_PKGVAR/installed_at" 2>/dev/null)"
else
  emit WARN "安装标记" "未找到 installed_at，install_callback 可能未执行"
fi

if [ -f "$TRIM_PKGVAR/heartbeat" ]; then
  emit PASS "服务心跳" "最近心跳 $(cat "$TRIM_PKGVAR/heartbeat" 2>/dev/null)"
else
  emit WARN "服务心跳" "未找到 heartbeat（ctl_stop=true 且未启动时属正常）"
fi

# ---------- 输出 ----------
TOTAL=$((PASS + WARN + FAIL))

if [ "$MODE" = "html" ]; then
  printf 'Content-Type: text/html; charset=utf-8\r\n\r\n'
  cat <<HTML
<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>牛库管家 · 平台自检</title>
<style>
  :root { --bg:#f7f8fa; --fg:#1f2329; --card:#fff; --line:#e5e6eb; --ok:#0f9d58; --warn:#d97706; --bad:#d93026; --muted:#6b7280; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#15171a; --fg:#e6e8ea; --card:#1d2024; --line:#2c3036; --ok:#4ade80; --warn:#fbbf24; --bad:#f87171; --muted:#9aa0a6; }
  }
  * { box-sizing:border-box; }
  body { margin:0; padding:20px; background:var(--bg); color:var(--fg);
         font:14px/1.6 -apple-system, system-ui, "PingFang SC", "Noto Sans CJK SC", sans-serif; }
  h1 { font-size:18px; margin:0 0 4px; }
  .sub { color:var(--muted); font-size:12px; margin-bottom:16px; }
  .sum { display:flex; gap:10px; flex-wrap:wrap; margin-bottom:16px; }
  .chip { background:var(--card); border:1px solid var(--line); border-radius:8px;
          padding:8px 12px; font-size:13px; }
  .chip b { font-size:18px; margin-right:4px; }
  .ok b { color:var(--ok); } .warn b { color:var(--warn); } .bad b { color:var(--bad); }
  table { width:100%; border-collapse:collapse; background:var(--card);
          border:1px solid var(--line); border-radius:10px; overflow:hidden; }
  th, td { text-align:left; padding:10px 12px; border-bottom:1px solid var(--line); vertical-align:top; }
  th { font-size:12px; color:var(--muted); font-weight:600; }
  tr:last-child td { border-bottom:0; }
  td.st { width:64px; font-weight:600; white-space:nowrap; }
  .s-PASS { color:var(--ok); } .s-WARN { color:var(--warn); } .s-FAIL { color:var(--bad); }
  .detail { color:var(--muted); word-break:break-all; }
  footer { color:var(--muted); font-size:12px; margin-top:16px; }
</style>
</head>
<body>
<h1>牛库管家 · 平台自检</h1>
<div class="sub">用于验证目录映射、权限边界与运行身份。请把本页结果作为核对清单 P-02 / P-03 的证据。</div>
<div class="sum">
  <div class="chip ok"><b>${PASS}</b>通过</div>
  <div class="chip warn"><b>${WARN}</b>需确认</div>
  <div class="chip bad"><b>${FAIL}</b>未通过</div>
  <div class="chip">共 ${TOTAL} 项</div>
</div>
<table>
<thead><tr><th>结论</th><th>检查项</th><th>详情</th></tr></thead>
<tbody>
HTML
  while IFS='|' read -r st name detail; do
    [ -z "${st:-}" ] && continue
    printf '<tr><td class="st s-%s">%s</td><td>%s</td><td class="detail">%s</td></tr>\n' \
      "$st" "$st" "$name" "$detail"
  done < "$ROWS"
  cat <<HTML
</tbody>
</table>
<footer>报告生成时间：$(date '+%Y-%m-%d %H:%M:%S') · 运行身份：${NAME_NOW}(uid=${UID_NOW})</footer>
</body>
</html>
HTML
else
  echo "=============================================="
  echo " 牛库管家 · 平台自检报告"
  echo " 时间：$(date '+%Y-%m-%d %H:%M:%S')   身份：${NAME_NOW}(uid=${UID_NOW})"
  echo "=============================================="
  while IFS='|' read -r st name detail; do
    [ -z "${st:-}" ] && continue
    printf '[%s] %s — %s\n' "$st" "$name" "$detail"
  done < "$ROWS"
  echo "----------------------------------------------"
  echo " 通过 ${PASS} · 需确认 ${WARN} · 未通过 ${FAIL} · 共 ${TOTAL} 项"
  if [ "$FAIL" -gt 0 ]; then
    echo " 结论：存在未通过项，请逐条对照核对清单处理后再继续。"
  elif [ "$WARN" -gt 0 ]; then
    echo " 结论：无阻断项，需确认项请人工判断。"
  else
    echo " 结论：全部通过。"
  fi
fi

rm -f "$ROWS" 2>/dev/null
exit 0
