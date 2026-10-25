#!/bin/bash
# 容器内冒烟：把刚编出来的二进制按真机方式跑起来，验证三条最容易在真机上坏的链路：
#   1. 开发模式（TCP）能否直接启动并免身份运行；
#   2. 平台模式（Unix Socket + 网关前缀 + 身份 Header）是否与飞牛网关的约定一致；
#   3. 业务闭环：建库 → 读写 → 结构 → 备份（落共享目录）→ 恢复 → 删除 → 审计。
# 由 scripts/smoke.sh 挂进容器执行，参数是容器内的二进制路径。
# 依赖：bash、curl（需支持 --unix-socket）、grep、sed。
set -u

BIN="${1:?需要二进制路径}"
PORT=18780
WORK="$(mktemp -d)"
# 模拟 config/resource 分配的共享目录：basename 里带 backup，服务端才会选中它
SHARE="$WORK/share/niudbm/backups"
SOCK="$WORK/target/app.sock"

ok() { echo "  [ok]   $*"; }
die() {
  echo "  [fail] $*" >&2
  if [ -f "$WORK/server.log" ]; then
    echo "  --- 服务日志 ---" >&2
    tail -n 20 "$WORK/server.log" >&2
  fi
  exit 1
}
first_id() { grep -o '"id":"[^"]*"' "$1" | head -1 | cut -d'"' -f4; }

[ -x "$BIN" ] || die "二进制不可执行：$BIN"

ver="$("$BIN" --version 2>/dev/null)"
[ -n "$ver" ] || die "--version 没有输出"
ok "版本 $ver"

# ---------- 1. 开发模式：没有 TRIM_* 时应能直接跑起来，且免身份 ----------
unset TRIM_APPDEST TRIM_PKGVAR TRIM_PKGETC TRIM_PKGTMP TRIM_PKGHOME TRIM_DATA_SHARE_PATHS
cd "$WORK" || die "无法进入工作目录"
"$BIN" --addr "127.0.0.1:$PORT" >dev.log 2>&1 &
DEV_PID=$!
trap 'kill "$DEV_PID" 2>/dev/null; wait "$DEV_PID" 2>/dev/null' EXIT

base="http://127.0.0.1:$PORT"
ready=0
for _ in $(seq 1 100); do
  if curl -fsS "$base/api/v1/health" -o dev-health.json 2>/dev/null; then
    ready=1
    break
  fi
  kill -0 "$DEV_PID" 2>/dev/null || die "开发模式进程启动后提前退出"
  sleep 0.1
done
[ "$ready" -eq 1 ] || die "10 秒内没有响应健康检查"
grep -q '"status":"ok"' dev-health.json || die "健康检查内容异常：$(cat dev-health.json)"
grep -q '"platform":false' dev-health.json || die "开发模式应报告 platform=false"
ok "开发模式可启动（无 TRIM_*，平台标记为 false）"

curl -fsS -X POST "$base/api/v1/instances" -H 'Content-Type: application/json' \
  -d '{"name":"开发库"}' -o dev-create.json || die "开发模式建库失败（$(cat dev-create.json 2>/dev/null)）"
grep -q '"name":"开发库"' dev-create.json || die "开发模式建库返回异常"
ok "开发模式免身份可写（本地调试不会被 401 挡住）"

kill "$DEV_PID" 2>/dev/null
wait "$DEV_PID" 2>/dev/null
trap - EXIT
rm -rf "${TMPDIR:-/tmp}/niudbm-dev" 2>/dev/null

# ---------- 2. 平台变量下的只读自检 ----------
export TRIM_APPNAME=niudbm
export TRIM_APPDEST="$WORK/target"
export TRIM_PKGVAR="$WORK/var"
export TRIM_PKGETC="$WORK/etc"
export TRIM_PKGTMP="$WORK/tmp"
export TRIM_PKGHOME="$WORK/home"
export TRIM_USERNAME=niudbm
export TRIM_UID=1000
export TRIM_GID=1000
export TRIM_SYS_ARCH=x86_64
export TRIM_DATA_SHARE_PATHS="$SHARE"
mkdir -p "$TRIM_APPDEST" "$TRIM_PKGVAR" "$TRIM_PKGETC" "$TRIM_PKGTMP" "$TRIM_PKGHOME" "$SHARE"

"$BIN" --diag >diag.txt 2>&1 || die "--diag 退出码非 0"
grep -q "平台自检" diag.txt || die "--diag 输出缺少标题"
# 容器里以 root 运行，权限类项必然 FAIL；这里只要求目录类检查真的跑出 PASS
pass_count="$(grep -c '^  PASS' diag.txt || true)"
[ "${pass_count:-0}" -ge 5 ] || die "--diag 通过项太少（${pass_count}）：$(cat diag.txt)"
ok "平台自检可执行（${pass_count} 项 PASS）"

# ---------- 3. 平台模式：Unix Socket + 网关前缀 + 身份 Header ----------
"$BIN" --socket "$SOCK" >server.log 2>&1 &
PID=$!
trap 'kill "$PID" 2>/dev/null; wait "$PID" 2>/dev/null' EXIT

for _ in $(seq 1 100); do
  [ -S "$SOCK" ] && break
  kill -0 "$PID" 2>/dev/null || die "平台模式进程启动后提前退出"
  sleep 0.1
done
[ -S "$SOCK" ] || die "10 秒内没有在 TRIM_APPDEST 下创建 socket：$SOCK"
ok "socket 落在 TRIM_APPDEST 下（${SOCK#"$WORK"/}）"

APP="http://localhost/app/niudbm"
CURL=(curl -sS --unix-socket "$SOCK")
ADMIN=(-H 'X-Trim-Userid: 1000' -H 'X-Trim-Username: admin' -H 'X-Trim-Isadmin: true')
GUEST=(-H 'X-Trim-Userid: 1001' -H 'X-Trim-Username: guest' -H 'X-Trim-Isadmin: false')

# 网关是否剥离 gatewayPrefix 未知，两种路径都必须能命中
"${CURL[@]}" "${ADMIN[@]}" "$APP/api/v1/health" -o gw1.json || die "带前缀的健康检查失败"
grep -q '"status":"ok"' gw1.json || die "带前缀健康检查内容异常：$(cat gw1.json)"
"${CURL[@]}" "${ADMIN[@]}" "http://localhost/api/v1/health" -o gw2.json || die "剥离前缀后的健康检查失败"
grep -q '"status":"ok"' gw2.json || die "剥离前缀后健康检查内容异常：$(cat gw2.json)"
grep -q '"platform":true' gw1.json || die "平台模式应报告 platform=true"
ok "网关路径双兼容（带 /app/niudbm 与剥离前缀都可命中）"

# 身份三道闸：无头 → 401，普通用户 → 401，管理员 → 放行
for case_name in anon guest; do
  case "$case_name" in
    anon)  extra=();            label="没有身份头" ;;
    guest) extra=("${GUEST[@]}"); label="普通用户" ;;
  esac
  status="$("${CURL[@]}" "${extra[@]}" -o "deny-$case_name.json" -w '%{http_code}' \
    -X POST "$APP/api/v1/instances" -H 'Content-Type: application/json' \
    -d "{\"name\":\"${case_name}库\"}")"
  [ "$status" = "401" ] || die "${label}仍能写库（HTTP $status）：$(cat "deny-$case_name.json")"
  grep -q '"code":10002' "deny-$case_name.json" || die "${label}被拒时错误码不对：$(cat "deny-$case_name.json")"
done
ok "无身份头 / 普通用户的写操作都被拦下（401 / 10002）"

"${CURL[@]}" "${GUEST[@]}" "$APP/api/v1/instances" -o guest-list.json || die "普通用户读列表失败"
grep -q '"items"' guest-list.json || die "普通用户读列表内容异常"
ok "普通用户可读列表"

"${CURL[@]}" "${GUEST[@]}" "$APP/api/v1/diag" -o guest-diag.json || die "普通用户读自检失败"
grep -q '"isAdmin":false' guest-diag.json || die "普通用户自检里的 isAdmin 应为 false"
"${CURL[@]}" "${ADMIN[@]}" "$APP/api/v1/diag" -o admin-diag.json || die "管理员读自检失败"
grep -q '"isAdmin":true' admin-diag.json || die "管理员自检里的 isAdmin 应为 true"
ok "身份 Header 解析正确（isAdmin 随网关透传变化）"

"${CURL[@]}" "${ADMIN[@]}" -X POST "$APP/api/v1/instances" \
  -H 'Content-Type: application/json' \
  -d '{"name":"冒烟库","seedSql":"CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT);"}' \
  -o create.json || die "建库请求失败（$(cat create.json 2>/dev/null)）"
grep -q '"name":"冒烟库"' create.json || die "建库返回异常：$(cat create.json)"
ID="$(first_id create.json)"
[ -n "$ID" ] || die "无法解析实例 ID"
grep -q '"tableCount":1' create.json || die "建库后表数量应为 1：$(cat create.json)"
ok "建库成功（带初始化 SQL，1 张表）"

"${CURL[@]}" "${ADMIN[@]}" -X POST "$APP/api/v1/instances/$ID/query" \
  -H 'Content-Type: application/json' \
  -d "{\"sql\":\"INSERT INTO t (v) VALUES ('hello')\"}" -o w.json || die "写入请求失败"
grep -q '"rowsAffected":1' w.json || die "写入结果异常：$(cat w.json)"
ok "SQL 写入成功"

"${CURL[@]}" "${ADMIN[@]}" -X POST "$APP/api/v1/instances/$ID/query" \
  -H 'Content-Type: application/json' -d '{"sql":"SELECT v FROM t"}' -o r.json || die "查询请求失败"
grep -q 'hello' r.json || die "查询结果异常：$(cat r.json)"
ok "SQL 查询成功"

# 护栏：一次只能执行一条语句
status="$("${CURL[@]}" "${ADMIN[@]}" -o guard.json -w '%{http_code}' \
  -X POST "$APP/api/v1/instances/$ID/query" -H 'Content-Type: application/json' \
  -d '{"sql":"SELECT 1; SELECT 2"}')"
[ "$status" = "400" ] || die "多条语句未被拒绝（HTTP $status）：$(cat guard.json)"
ok "多语句护栏生效"

"${CURL[@]}" "${ADMIN[@]}" "$APP/api/v1/instances/$ID/schema" -o schema.json || die "结构请求失败"
grep -q '"name":"t"' schema.json || die "结构信息里没有建出的表"
ok "表结构读取成功"

"${CURL[@]}" "${ADMIN[@]}" -X POST "$APP/api/v1/instances/$ID/backups" \
  -H 'Content-Type: application/json' -d '{"note":"冒烟备份"}' -o bk.json || die "备份请求失败"
grep -q '"status":"succeeded"' bk.json || die "备份状态异常：$(cat bk.json)"
grep -q '"checksum":"[0-9a-f]\{64\}"' bk.json || die "备份缺少校验和：$(cat bk.json)"
BID="$(first_id bk.json)"
[ -n "$BID" ] || die "无法解析备份 ID"
# 备份必须落在平台分配的共享目录，用户才能在文件管理器里看到
"${CURL[@]}" "${ADMIN[@]}" "$APP/api/v1/instances/$ID/backups" -o bl.json || die "备份列表请求失败"
grep -Fq "\"backupRoot\":\"$SHARE\"" bl.json || die "备份根目录没选中共享目录：$(cat bl.json)"
bkfile="$(ls "$SHARE/$ID"/*.db 2>/dev/null | head -1)"
[ -n "$bkfile" ] || die "共享目录下没有备份文件：$SHARE/$ID"
ok "备份成功并落到共享目录（$(basename "$bkfile")）"

"${CURL[@]}" "${ADMIN[@]}" "$APP/api/v1/instances/$ID/backups/$BID/download" -o dl.db || die "备份下载失败"
[ -s dl.db ] || die "下载到的备份为空"
ok "备份可下载"

"${CURL[@]}" "${ADMIN[@]}" -X POST "$APP/api/v1/instances/$ID/query" \
  -H 'Content-Type: application/json' \
  -d "{\"sql\":\"INSERT INTO t (v) VALUES ('second')\"}" -o w2.json || die "第二次写入失败"
"${CURL[@]}" "${ADMIN[@]}" -X POST "$APP/api/v1/instances/$ID/backups/$BID/restore" \
  -H 'Content-Type: application/json' -d '{"confirmName":"冒烟库"}' \
  -o restore.json || die "恢复请求失败"
grep -q '"restored":true' restore.json || die "恢复结果异常：$(cat restore.json)"
ok "备份恢复成功"

# 恢复后应只剩 1 行（备份时只有 hello）
"${CURL[@]}" "${ADMIN[@]}" -X POST "$APP/api/v1/instances/$ID/query" \
  -H 'Content-Type: application/json' -d '{"sql":"SELECT COUNT(*) AS n FROM t"}' -o cnt.json ||
  die "恢复后查询失败"
grep -q '\[1\]' cnt.json || die "恢复后行数不对（期望 1）：$(cat cnt.json)"
ok "恢复后数据回到备份时刻"

# ---------- 4. 前端资源（网关前缀下） ----------
"${CURL[@]}" "${ADMIN[@]}" "$APP" -o index.html || die "首页请求失败"
grep -q 'id="app"' index.html || die "首页内容异常"
grep -q '<base href="/app/niudbm/">' index.html || die "首页 base 注入不对：$(grep -o '<base[^>]*>' index.html)"
asset="$(grep -o 'assets/[A-Za-z0-9_-]*\.js' index.html | head -1)"
[ -n "$asset" ] || die "首页没有引用 JS 资源"
"${CURL[@]}" "${ADMIN[@]}" "$APP/$asset" -o /dev/null || die "带前缀的静态资源访问失败：$asset"
ok "前端页面与资源在网关前缀下可访问（$asset）"

"${CURL[@]}" "${ADMIN[@]}" "$APP/api/v1/audit" -o audit.json || die "审计请求失败"
grep -q '"instance.create"' audit.json || die "审计缺少建库记录"
grep -q '"backup.restore"' audit.json || die "审计缺少恢复记录"
ok "审计记录可读（建库、恢复都有留痕）"

"${CURL[@]}" "${ADMIN[@]}" -X DELETE "$APP/api/v1/instances/$ID" \
  -H 'Content-Type: application/json' \
  -d '{"confirmName":"冒烟库","keepData":false}' -o del.json || die "删除请求失败"
grep -q '"keepData":false' del.json || die "删除返回异常：$(cat del.json)"
[ ! -d "$TRIM_PKGVAR/instances/$ID" ] || die "删除后实例目录仍在：$TRIM_PKGVAR/instances/$ID"
ok "删除实例成功（数据已移入回收站）"

echo "  [ok]   冒烟通过：开发模式、平台自检、socket、网关前缀、身份闸、建库、读写、护栏、结构、备份落盘、下载、恢复、前端、审计、删除"
