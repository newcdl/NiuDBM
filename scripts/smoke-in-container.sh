#!/bin/bash
# 容器内验证实际产物：平台接入、权限、镜像清单、MySQL 服务生命周期、备份恢复与失败现场。
#
# 这一版本只有 MySQL + Docker：
#   - 不挂 docker.sock / 没有 SMOKE_DOCKER=1 时，跑「不依赖容器」的那部分（接口契约、参数校验、权限、设置）。
#   - 挂了 docker.sock 时，再跑完整的建服务 → 真实 TCP 认证 → 备份恢复 → 删除回收。
set -euo pipefail
BIN="${1:?需要二进制路径}"
WORK="$(mktemp -d)"
chmod 755 "${WORK}"
SHARE="${WORK}/share/niudbm/backups"
SOCK="${WORK}/target/app.sock"
PID=''
ok() { echo "  [ok]   $* （第 ${SECONDS} 秒）"; }
skip() { echo "  [skip] $*"; }
die() {
  echo "  [fail] $*" >&2
  if [ -f "${WORK}/server.log" ]; then tail -n 20 "${WORK}/server.log" >&2; fi
  for log in "${TRIM_PKGVAR:-}/instances"/*/deploy.log; do
    [ -f "${log}" ] || continue
    echo "  --- ${log}" >&2
    tail -n 15 "${log}" >&2
  done
  cleanup_containers 2>/dev/null || true
  exit 1
}
DOCKER_SNAPSHOT=''
# 中途失败时容器还活着（重启策略会让它一直跑），真机上就是"服务没建好但端口被占着"。
# 只清本次新增的：跑之前拍快照，结束时对比，差集才是这个脚本造出来的东西。
cleanup_containers() {
  command -v docker >/dev/null 2>&1 || return 0
  [ -n "${DOCKER_SNAPSHOT:-}" ] && [ -f "${DOCKER_SNAPSHOT}" ] || return 0
  docker ps -a --filter 'label=niudbm.managed=true' --format '{{.Names}}' >/tmp/smoke-after.names 2>/dev/null || return 0
  while read -r c; do
    [ -n "${c}" ] || continue
    grep -qxF "${c}" "${DOCKER_SNAPSHOT}" || docker rm -f "${c}" >/dev/null 2>&1 || true
  done </tmp/smoke-after.names
  docker volume ls --format '{{.Name}}' 2>/dev/null | grep '^niudbm-' >/tmp/smoke-after.vols || true
  while read -r v; do
    [ -n "${v}" ] || continue
    grep -qxF "${v}" "${DOCKER_SNAPSHOT}.vol" 2>/dev/null || docker volume rm -f "${v}" >/dev/null 2>&1 || true
  done </tmp/smoke-after.vols
}
cleanup() {
  if [ -n "${PID}" ]; then kill "${PID}" 2>/dev/null || true; wait "${PID}" 2>/dev/null || true; fi
  cleanup_containers
}
trap cleanup EXIT
first_id() { sed -n 's/.*"data":{"id":"\([^"]*\)".*/\1/p' response.json; }
first_field() { sed -n "s/.*\"$1\":\"\\([^\"]*\\)\".*/\\1/p" response.json | head -n 1; }
contains() { grep -Fq "$1" response.json || die "响应缺少 $1：$(cat response.json)"; }
lacks() { if grep -Fq "$1" response.json; then die "响应不该包含 $1：$(cat response.json)"; fi; }
[ -x "${BIN}" ] || die '二进制不可执行'

# 容器里没有 docker CLI：静态下载一份（宿主的是 macOS 二进制，挂进来跑不了）。
# 拉不到就退化成"不依赖容器"的冒烟，而不是把整个构建判失败。
if [ "${SMOKE_DOCKER:-0}" = "1" ] && ! command -v docker >/dev/null 2>&1; then
  case "$(uname -m)" in
    aarch64 | arm64) DL_ARCH=aarch64 ;;
    *) DL_ARCH=x86_64 ;;
  esac
  for base in https://download.docker.com https://mirrors.aliyun.com/docker-ce; do
    url="${base}/linux/static/stable/${DL_ARCH}/docker-27.3.1.tgz"
    if curl -fsSL --max-time 120 "${url}" -o /tmp/docker.tgz 2>/dev/null; then
      tar -xzf /tmp/docker.tgz -C /tmp && export PATH="/tmp/docker:${PATH}" && break
    fi
  done
  command -v docker >/dev/null 2>&1 || echo '  [warn] 没装上 docker CLI，容器相关冒烟会跳过'
fi

ok "版本 $("${BIN}" --version)"
cd "${WORK}"

# ---------- 开发模式：不依赖平台身份 ----------
unset TRIM_APPDEST TRIM_PKGVAR TRIM_PKGETC TRIM_PKGTMP TRIM_PKGHOME TRIM_DATA_SHARE_PATHS
"${BIN}" --addr 127.0.0.1:18780 >dev.log 2>&1 &
PID=$!
for _ in $(seq 1 100); do
  if curl -fsS http://127.0.0.1:18780/api/v1/health -o response.json 2>/dev/null; then break; fi
  kill -0 "${PID}" 2>/dev/null || die '开发模式提前退出'
  sleep 0.1
done
contains '"platform":false'
contains '"engine":"mysql"'
contains '"mongodb"'

# 名字校验：路径分隔符、超长、空值都必须在动手部署之前拒掉
curl -sS -X POST http://127.0.0.1:18780/api/v1/instances -H 'Content-Type: application/json' \
  -d '{"name":"a/b"}' -o response.json -w '%{http_code}' >dev.status
[ "$(cat dev.status)" = 400 ] || die "非法名字应 400，实际 $(cat dev.status)：$(cat response.json)"

# 不认识的引擎明确拒绝，不能悄悄建一个 MySQL
curl -sS -X POST http://127.0.0.1:18780/api/v1/instances -H 'Content-Type: application/json' \
  -d '{"name":"lite","engine":"sqlite"}' -o response.json -w '%{http_code}' >dev.status
[ "$(cat dev.status)" = 400 ] || die "sqlite 应 400，实际 $(cat dev.status)：$(cat response.json)"
contains '不支持的引擎'

# PostgreSQL 是支持的引擎：没有 Docker 时应卡在"要用 Docker"，而不是"不支持这个引擎"
curl -sS -X POST http://127.0.0.1:18780/api/v1/instances -H 'Content-Type: application/json' \
  -d '{"name":"pg","engine":"postgres"}' -o response.json -w '%{http_code}' >dev.status
[ "$(cat dev.status)" = 400 ] || die "无 Docker 时建 PG 应 400，实际 $(cat dev.status)：$(cat response.json)"
contains 'Docker'

# 镜像清单是版本唯一来源
curl -fsS http://127.0.0.1:18780/api/v1/images -o response.json
contains '"tag":"8.4"'
contains '"name":"mysql:8.4"'
contains '"family":"postgres"'
contains '"family":"mysql"'
cleanup
PID=''
ok '开发模式启动、健康检查、名字与引擎校验、镜像清单'

# ---------- 平台模式：网关注入身份 ----------
export TRIM_APPNAME=niudbm TRIM_USERNAME=niudbm TRIM_UID=1000 TRIM_GID=1000
export TRIM_APPDEST="${WORK}/target" TRIM_PKGVAR="${WORK}/var" TRIM_PKGETC="${WORK}/etc"
export TRIM_PKGTMP="${WORK}/tmp" TRIM_PKGHOME="${WORK}/home" TRIM_DATA_SHARE_PATHS="${SHARE}"
export TRIM_SYS_ARCH="$(uname -m)"
mkdir -p "${TRIM_APPDEST}" "${TRIM_PKGVAR}" "${TRIM_PKGETC}" "${TRIM_PKGTMP}" "${TRIM_PKGHOME}" "${SHARE}"
if ! id -u niudbm >/dev/null 2>&1; then useradd -u 1000 -m niudbm; fi
"${BIN}" --diag >diag.txt 2>&1
grep -q '平台自检' diag.txt || die '自检缺少标题'
[ "$(grep -c '^  PASS' diag.txt)" -ge 5 ] || die '自检通过项过少'
"${BIN}" --socket "${SOCK}" >server.log 2>&1 &
PID=$!
for _ in $(seq 1 100); do
  [ -S "${SOCK}" ] && break
  kill -0 "${PID}" 2>/dev/null || die '平台模式提前退出'
  sleep 0.1
done
[ -S "${SOCK}" ] || die '未创建平台 socket'
APP='http://localhost/app/niudbm'
CURL=(curl -sS --max-time 600 --unix-socket "${SOCK}")
ADMIN=(-H 'X-Trim-Userid: 1000' -H 'X-Trim-Username: admin' -H 'X-Trim-Isadmin: true')
GUEST=(-H 'X-Trim-Userid: 1001' -H 'X-Trim-Username: guest' -H 'X-Trim-Isadmin: false')
JAR="${WORK}/cookies.txt"

# 身份头只用来换会话：先登录一次，之后所有请求都带 Cookie
login_status="$("${CURL[@]}" "${ADMIN[@]}" -X POST "${APP}/api/v1/auth/login" -c "${JAR}" -o response.json -w '%{http_code}')"
[ "${login_status}" = 200 ] || die "管理员登录应 200，实际 ${login_status}：$(cat response.json)"
grep -qi 'niudbm_session' "${JAR}" || die '登录成功但没有下发会话 Cookie'

req() {
  local expected="$1" method="$2" route="$3" body="${4:-}" mime="${5:-application/json}" status rc=''
  status="$("${CURL[@]}" --connect-timeout 5 -b "${JAR}" -X "${method}" "${APP}/api/v1${route}" \
    -H "Content-Type: ${mime}" --data-binary "${body}" -o response.json -w '%{http_code}')" || rc=$?
  if [ -n "${rc}" ]; then
    die "${method} ${route} 请求没有完成（curl 退出码 ${rc}）：$(cat response.json 2>/dev/null)"
  fi
  [ "${status}" = "${expected}" ] || die "${method} ${route} 应为 ${expected}，实际 ${status}：$(cat response.json)"
  case "${expected}" in
    2*) contains '"code":0' ;;
    *) if grep -q '"code":0' response.json; then die '错误响应不应返回成功码'; fi ;;
  esac
}
# 服务没有默认库了：SQL 脚本必须点名落在哪个库上
sql() { req "$1" POST "/instances/$2/import/sql?database=$3" "$4" text/plain; }
wait_task() {
  local id="$1" expect="${2:-ok}"
  for _ in $(seq 1 600); do
    req 200 GET "/deploy-tasks/${id}"
    if grep -q "\"status\":\"${expect}\"" response.json; then return 0; fi
    if [ "${expect}" = ok ] && grep -q '"status":"failed"' response.json; then
      die "部署失败：$(cat response.json)"
    fi
    sleep 1
  done
  die '部署任务超时'
}
deploy() {
  local body="$1" id
  req 200 POST /instances "${body}"
  contains '"async":true'
  id="$(first_field taskId)"
  [ -n "${id}" ] || die '创建实例没有返回 taskId'
  wait_task "${id}"
  first_field instanceId
}

# 身份与权限：只认会话，Header 只用于登录
"${CURL[@]}" "${GUEST[@]}" -o response.json -w '%{http_code}' "${APP}/api/v1/health" >guest.status
[ "$(cat guest.status)" = 200 ] || die '普通用户读健康检查应 200'
GUESTJAR="${WORK}/guest-cookies.txt"
"${CURL[@]}" "${GUEST[@]}" -X POST "${APP}/api/v1/auth/login" -c "${GUESTJAR}" -o response.json -w '%{http_code}' >guest.status
[ "$(cat guest.status)" = 200 ] || die "普通成员登录应 200，实际 $(cat guest.status)"
"${CURL[@]}" -b "${GUESTJAR}" -o response.json -w '%{http_code}' "${APP}/api/v1/settings" >guest.status
[ "$(cat guest.status)" = 403 ] || die "普通成员读取全局设置应 403，实际 $(cat guest.status)"
"${CURL[@]}" -o response.json -w '%{http_code}' "${APP}/api/v1/instances" >guest.status
[ "$(cat guest.status)" = 401 ] || die "没有身份应 401，实际 $(cat guest.status)"
# 只带身份头、不带会话：退出登录之后不该被自动带进来
"${CURL[@]}" "${ADMIN[@]}" -o response.json -w '%{http_code}' "${APP}/api/v1/instances" >guest.status
[ "$(cat guest.status)" = 401 ] || die "只有身份头没有会话应 401，实际 $(cat guest.status)"
# 退出登录后旧会话立即失效，再重新登录继续后面的用例
"${CURL[@]}" -b "${JAR}" -X POST "${APP}/api/v1/auth/logout" -o response.json -w '%{http_code}' >guest.status
[ "$(cat guest.status)" = 200 ] || die "退出登录应 200，实际 $(cat guest.status)"
"${CURL[@]}" -b "${JAR}" -o response.json -w '%{http_code}' "${APP}/api/v1/instances" >guest.status
[ "$(cat guest.status)" = 401 ] || die "退出后旧会话应失效，实际 $(cat guest.status)"
login_status="$("${CURL[@]}" "${ADMIN[@]}" -X POST "${APP}/api/v1/auth/login" -c "${JAR}" -o response.json -w '%{http_code}')"
[ "${login_status}" = 200 ] || die "重新登录应 200，实际 ${login_status}：$(cat response.json)"
ok '网关身份、管理员登录与退出'

# 概览 / 设置 / 任务 / 镜像
req 200 GET /overview
contains '"images"'
req 200 GET /images
contains '"tag":"8.4"'
contains '"arch"'
req 200 GET /preflight
contains '"docker"'
req 200 GET /jobs
contains '"total":0'
req 200 GET /settings
contains '"backupRetentionCount"'
req 200 PATCH /settings '{"backupRetentionCount":5}'
req 200 GET /audit
req 404 GET /deploy-tasks/nope
ok '概览、镜像、设置、任务与未知任务 404'

# 参数校验：全部在异步之前挡住
req 400 POST /instances '{"name":"x","engine":"sqlite"}'
contains '不支持的引擎'
req 400 POST /instances '{"name":"x","engineVersion":"9.9"}'
contains '不支持这个版本'
req 400 POST /instances '{"name":"x","users":[{"name":"u","password":"123","level":"read"}]}'
req 400 POST /instances '{"name":"x","users":[{"name":"u","password":"password123","level":"root"}]}'
req 400 POST /instances '{"name":"a/b"}'
req 400 POST /instances/connect '{"name":"外部库","engine":"mysql","host":"mysql://bad","port":3306,"username":"root","password":"12345678"}'
contains '不要带协议'
if [ "${SMOKE_DOCKER:-0}" != "1" ]; then
  # 没有 Docker 时，参数合法的请求也要在动手之前说清楚原因，而不是挂一个注定的任务
  req 400 POST /instances '{"name":"校验库"}'
  contains 'Docker'
  ok '参数校验（含 Docker 不可用时明确拒绝）'
else
  ok '参数校验'
fi


# ---------- 依赖 Docker 的部分 ----------
"${CURL[@]}" -b "${JAR}" "${APP}/api/v1/health" -o health.json 2>/dev/null || true
DOCKER_OK="$(sed -n 's/.*"docker":\(true\|false\).*/\1/p' health.json 2>/dev/null | head -n 1)"
if [ "${SMOKE_DOCKER:-0}" = "1" ] && [ "${DOCKER_OK:-false}" != "true" ]; then
  echo "  [info] 服务端读不到 Docker：$(cat health.json 2>/dev/null)"
  echo "  [info] docker CLI=$(command -v docker || echo 无) sock=$(ls -l /var/run/docker.sock 2>&1 | head -1)"
fi
if [ "${DOCKER_OK:-false}" != "true" ] || [ "${SMOKE_DOCKER:-0}" != "1" ]; then
  skip '未启用 Docker（SMOKE_DOCKER=1 且挂载 docker.sock 才跑服务生命周期）'
  exit 0
fi

PW='SmokePass_Only42'

# 拍一份"跑之前就有的容器与卷"：失败中断时 cleanup 靠差集清理，不误删用户自己的实例
if command -v docker >/dev/null 2>&1; then
  DOCKER_SNAPSHOT="/tmp/docker-before.$$"
  docker ps -a --filter 'label=niudbm.managed=true' --format '{{.Names}}' > "${DOCKER_SNAPSHOT}" 2>/dev/null || true
  docker volume ls --format '{{.Name}}' | grep '^niudbm-' > "${DOCKER_SNAPSHOT}.vol" 2>/dev/null || true
fi

# 部署失败必须留现场：非法初始化 SQL 让 Provision 阶段失败，
# 台账里要留一条 failed，能读原因，也能拿到当时的容器/引擎日志。
req 200 POST /instances '{"name":"失败现场","engineVersion":"8.4","seedSql":"SELECT * FROM not_a_table;"}'
FAILTASK="$(first_field taskId)"
[ -n "${FAILTASK}" ] || die '失败用例没有返回 taskId'
wait_task "${FAILTASK}" failed
BADID="$(first_field instanceId)"
[ -n "${BADID}" ] || die '失败任务没有带回实例号，列表里就无法呈现'
req 200 GET "/instances/${BADID}"
contains '"status":"failed"'
contains '"statusReason"'
req 200 GET "/instances/${BADID}/deploy-log"
contains '"available":true'
contains '阶段 provision 失败'
req 200 DELETE "/instances/${BADID}" '{"confirmName":"失败现场","keepData":false}'
[ ! -d "${TRIM_PKGVAR}/instances/${BADID}" ] || die '失败实例删除后目录未回收'
ok '部署失败保留现场、台账可见与部署日志'

PGID="$(deploy "{\"name\":\"冒烟MySQL\",\"engineVersion\":\"8.4\",\"rootPassword\":\"${PW}\"}")"
[ -n "${PGID}" ] || die '无法解析实例号'
req 200 GET "/instances/${PGID}"
contains '"status":"running"'
contains '"engineVersion":"8.4"'
# 默认落在宿主机目录：路径真实存在，体积算得出来（先读详情，最后再读连接信息，
# 否则 response.json 被覆盖，下面的账号端口会取错响应）
req 200 GET "/instances/${PGID}"
contains '"storageKind":"bind"'
DBDIR="$(first_field filePath)"
[ -d "${DBDIR}" ] || die "数据目录不存在：${DBDIR}"
req 200 GET "/instances/${PGID}/connection"
contains '"running":true'
# 连接地址不能是监听地址 0.0.0.0：用户复制到客户端一定连不上
if grep -q '"host":"0.0.0.0"' response.json; then die '连接信息给的是监听地址 0.0.0.0'; fi
# 概览给的必须是 root（带密码能直接进），也不该再带库名——一个服务下有多个库
contains '"user":"root"'
lacks '"database"'
MYUSER="$(first_field user)"
MYPORT="$(sed -n 's/.*"port":\([0-9]*\).*/\1/p' response.json | head -n 1)"
MYPASS="$(first_field password)"
[ -n "${MYPORT}" ] || die '连接信息里没有端口'
[ -n "${MYPASS}" ] || die '连接信息里没有密码'
ok "服务部署完成（${MYUSER}@127.0.0.1:${MYPORT}）"

# 镜像页：部署过的版本要显示"已在本机 + 谁在用"，在用时不许删
req 200 GET /images
if ! grep -qF '"usedBy":["冒烟MySQL"]' response.json; then
  die "镜像占用没算对：$(head -c 400 response.json)"
fi
if ! grep -qF '"local":true' response.json; then
  die '部署完成后镜像没有标成本地已有'
fi
req 409 DELETE /images/8.4
contains '个服务在用'
# 已在本机时再拉一次要直接回 cached，不再走网络
req 200 POST /images/pull '{"tag":"8.4"}'
contains '"cached":true'
req 400 POST /images/pull '{"tag":"9.9"}'
ok '镜像占用统计、在用时禁删、已存在免拉取'

# 服务建出来是空的：不再自动带一个 app 库
req 200 GET "/instances/${PGID}/databases"
contains '"items":[]'
ok '新建服务里没有任何库'

# 建库：后面所有表与账号都落在这个库上
DB=shop
req 201 POST "/instances/${PGID}/databases" "{\"name\":\"${DB}\"}"
contains '"name":"shop"'

# 建表 + 账号矩阵：真实 TCP 认证，不用管理账号冒充
sql 200 "${PGID}" "${DB}" "CREATE TABLE t (id INT AUTO_INCREMENT PRIMARY KEY, v VARCHAR(64)); INSERT INTO t(v) VALUES ('hello');"
req 201 POST "/instances/${PGID}/users" '{"name":"reader","password":"SmokePass_Only42","grants":[{"database":"shop","level":"read"}]}'
req 201 POST "/instances/${PGID}/users" '{"name":"writer","password":"SmokePass_Only42","grants":[{"database":"shop","level":"write"}]}'
req 201 POST "/instances/${PGID}/users" '{"name":"blocked","password":"SmokePass_Only42","grants":[{"database":"shop","level":"none"}]}'
req 200 GET "/instances/${PGID}/users"
contains '"name":"reader"'

PROBE=/tmp/mysql-access.test
go -C /src/server test -buildvcs=false -c -o "${PROBE}" ./internal/mysqlctl >/dev/null 2>&1 \
  || die '编译真实认证探针失败'
access() {
  local user="$1" password="$2" query="$3" code="${4:-}"
  MYSQL_SMOKE_PORT="${MYPORT}" MYSQL_SMOKE_USER="${user}" MYSQL_SMOKE_PASSWORD="${password}" \
    MYSQL_SMOKE_DB="${DB}" MYSQL_SMOKE_SQL="${query}" MYSQL_SMOKE_ERROR="${code}" \
    "${PROBE}" -test.run '^TestTCPAccessSmoke$' >/dev/null 2>&1 \
    || die "账号 ${user} 执行 ${query} 结果不符合预期（错误码 ${code:-无}）"
}
access reader "${PW}" 'SELECT * FROM t'
access reader wrong_password 'SELECT 1' 1045
access reader "${PW}" "INSERT INTO t(v) VALUES ('denied')" 1142
access writer "${PW}" "BEGIN; INSERT INTO t(v) VALUES ('x'); UPDATE t SET v='y'; DELETE FROM t; ROLLBACK;"
access writer "${PW}" 'CREATE TABLE nope(id int)' 1142
access blocked "${PW}" 'SELECT 1' 1044
access "${MYUSER}" "${MYPASS}" 'SELECT * FROM t'
ok '真实 TCP 认证与读写/只读/无权限矩阵'

# 多库：再来一个，验证同一服务下多个库互不干扰
req 201 POST "/instances/${PGID}/databases" "{\"name\":\"report\"}"
contains '"name":"report"'
# 库列表要带"授权用户"：列表页那一列直接用
req 200 GET "/instances/${PGID}/databases"
contains '"users":["reader"'
# 一个账号两个库：降级与撤销都必须按库生效，不能一把全收
req 200 PATCH "/instances/${PGID}/users/reader" '{"grants":[{"database":"shop","level":"read"},{"database":"report","level":"write"}]}'
req 200 GET "/instances/${PGID}/users"
contains '"database":"report"'
# 只授 report 的账号
req 201 POST "/instances/${PGID}/databases/report/users" '{"name":"report_app","password":"SmokePass_Only42","level":"write"}'
contains '"database":"report"'
req 200 GET "/instances/${PGID}/users"
contains '"name":"report_app"'
# 给不存在的库建账号必须被拒：不能建出空授权账号
req 404 POST "/instances/${PGID}/databases/nosuchdb/users" '{"name":"ghost","password":"SmokePass_Only42","level":"read"}'
# 引擎自带的库不能删
req 400 DELETE "/instances/${PGID}/databases/mysql"
ok '多库、按库授权与系统库保护'

# 账号来源（hosts）：所有人 % 与指定 IP 两种形态
req 201 POST "/instances/${PGID}/users" '{"name":"ipuser","password":"SmokePass_Only42","hosts":["172.16.10.111"],"grants":[{"database":"shop","level":"read"}]}'
contains '"172.16.10.111"'
# 再改来源：加第二个 IP，原来那个要保留
req 200 PATCH "/instances/${PGID}/users/ipuser" '{"hosts":["172.16.10.111","172.16.10.112"]}'
req 200 GET "/instances/${PGID}/users"
contains '"172.16.10.112"'
# 密码回显：本机保存的那一份能查回来（MySQL 里只有密文）
req 200 GET "/instances/${PGID}/users/ipuser/password"
contains '"password":"SmokePass_Only42"'
# 网段来源：界面填的是 CIDR，落库必须转成 MySQL 认的点分掩码，否则永远匹配不上
req 201 POST "/instances/${PGID}/users" '{"name":"cidruser","password":"SmokePass_Only42","hosts":["172.16.30.0/24"],"grants":[{"database":"shop","level":"read"}]}'
contains '"172.16.30.0/255.255.255.0"'
# 掩码非法要在参数层拒掉，不能写进 mysql.user 变成一条废账号
req 400 POST "/instances/${PGID}/users" '{"name":"badcidr","password":"SmokePass_Only42","hosts":["172.16.30.0/33"]}'
contains '掩码不合法'
ok '访问来源支持网段（CIDR 转点分掩码）'

# root 远程访问：直接查 mysql.user 里 root 的 host 列表。
# 不拿"从宿主连不连得上"做断言——macOS 上 Docker Desktop 的端口转发会把源地址保留成
# 127.0.0.1，MySQL 反解成 localhost，于是删掉 root@'%' 之后照样能连；那是转发层的假象，
# 真机（Linux 桥接）从局域网连会被 1130 拒，这条放到真机清单里验。
req 200 GET "/instances/${PGID}/connection"
CONTAINER="$(first_field containerHost)"
[ -n "${CONTAINER}" ] || die '连接信息里没有容器名'
rootHosts() {
  docker exec "${CONTAINER}" mysql -uroot -p"${MYPASS}" -N \
    -e "SELECT host FROM mysql.user WHERE user='root'" 2>/dev/null | tr '\n' ' '
}
req 200 GET "/instances/${PGID}/root-access"
contains '"remote":true'
case "$(rootHosts)" in *%*) : ;; *) die "初始状态应有 root@'%'：$(rootHosts)" ;; esac
req 200 PUT "/instances/${PGID}/root-access" '{"remote":false}'
contains '"remote":false'
case "$(rootHosts)" in *%*) die "关闭后 root@'%' 仍在：$(rootHosts)" ;; esac
case "$(rootHosts)" in *localhost*) : ;; *) die "关闭不该删掉 root@localhost：$(rootHosts)" ;; esac
req 200 PUT "/instances/${PGID}/root-access" '{"remote":true}'
contains '"remote":true'
case "$(rootHosts)" in *%*) : ;; *) die "重新开启后没有 root@'%'：$(rootHosts)" ;; esac

# root 密码重设：太短与禁用字符在参数层挡住，改完旧密码立刻失效
req 400 PUT "/instances/${PGID}/root-password" '{"password":"short"}'
contains '至少 8 位'
req 400 PUT "/instances/${PGID}/root-password" '{"password":"NiuSmoke$2026"}'
req 200 PUT "/instances/${PGID}/root-password" '{"password":"NiuSmoke_2026"}'
contains 'root 密码已更新'
access "${MYUSER}" "${MYPASS}" 'SELECT 1' 1045
access "${MYUSER}" 'NiuSmoke_2026' 'SELECT * FROM t'
req 200 GET "/instances/${PGID}/connection"
contains '"password":"NiuSmoke_2026"'
MYPASS='NiuSmoke_2026'
ok 'root 远程访问开关与 root 密码重设'

# 性能方案与运行状态
req 200 GET "/instances/${PGID}/config/presets"
contains '"label":"4-8GB"'
req 200 GET "/instances/${PGID}/status"
contains '"buffer_pool_hit"'
ok '账号来源、密码回显、性能方案与运行状态'

# 配置文件原文：能读、能写、空内容被拦、能恢复上一版
# 这是白名单之外参数的唯一入口，写坏了实例会起不来，所以四条都要在
req 200 GET "/instances/${PGID}/config/file"
contains '"backupExists":false'
# 部署完就该是一份完整配置，而不是等第一次保存参数才有内容
contains '"missing":[]'
contains 'collation_server = utf8mb4_unicode_ci'
contains 'innodb_redo_log_capacity'
contains '# ---- InnoDB：内存与刷盘 ----'
# 先用参数页保存一次，保证配置文件已经存在（第一次手写时没有"上一版"可备份）
req 200 PUT "/instances/${PGID}/config" '{"values":{"max_connections":"200"}}'
contains '"needRestart":true'
req 200 PUT "/instances/${PGID}/config/file" '{"content":"[mysqld]\nmax_connections = 300\n"}'
contains '"needRestart":true'
req 200 GET "/instances/${PGID}/config/file"
contains 'max_connections = 300'
contains '"backupExists":true'
# 空内容会让实例起不来，必须在参数层就拒掉
req 400 PUT "/instances/${PGID}/config/file" '{"content":"   "}'
contains '不能为空'
req 200 POST "/instances/${PGID}/config/file/restore"
contains '已恢复上一版'
req 200 GET "/instances/${PGID}/config/file"
if grep -qF 'max_connections = 300' response.json; then
  die '恢复上一版之后手写的内容还在，恢复没生效'
fi
contains 'max_connections = 200'
ok '配置文件原文读写、空内容拦截与恢复上一版'


# 备份按库走：先备空库 shop，导出的 SQL 里只该有 shop
req 201 POST "/instances/${PGID}/backups" '{"note":"按库备份","database":"shop"}'
contains '"status":"succeeded"'
contains '"database":"shop"'
# 备份落在 backups/<实例号>/ 下，用 find 而不是 glob：
# compgen 无匹配时返回非零，会把整个脚本提前带走
SHOPSQL="$(find "${SHARE}" -name '*-shop-*.sql' -print -quit)"
[ -n "${SHOPSQL}" ] || die '备份文件名里没有库名'
grep -q 'CREATE DATABASE' "${SHOPSQL}" || die '备份文件里没有建库语句'
if grep -q '`app`' "${SHOPSQL}"; then die '备份文件里混进了别的库'; fi
ok '按库备份，导出的是这个库的 SQL 脚本'

# 备份 → 改数据 → 恢复 → 比对
sql 200 "${PGID}" "${DB}" "INSERT INTO t(v) VALUES ('before-backup');"
req 201 POST "/instances/${PGID}/backups" '{"note":"冒烟备份","database":"shop"}'
contains '"status":"succeeded"'
# 记录里要写清这份备份属于哪个库
contains '"database":"shop"'
# 不传库名必须被拒：没有默认库可以兜底
BID="$(first_id)"
# 不传库名必须被拒：没有默认库可以兜底
req 400 POST "/instances/${PGID}/backups" '{"note":"没选库"}'
[ -n "${BID}" ] || die '没有拿到备份 ID'
compgen -G "${SHARE}/*" >/dev/null || die "备份目录里没有文件：${SHARE}"
sql 200 "${PGID}" "${DB}" "INSERT INTO t(v) VALUES ('after-backup');"
req 200 POST "/instances/${PGID}/backups/${BID}/restore" '{"confirmName":"冒烟MySQL"}'
contains '"restored":true'
sql 200 "${PGID}" "${DB}" "SELECT COUNT(*) FROM t WHERE v='after-backup';"
ok '备份、改数据、恢复与校验'

# 删除：容器与数据目录都要回收
req 200 DELETE "/instances/${PGID}" '{"confirmName":"冒烟MySQL","keepData":false}'
[ ! -d "${TRIM_PKGVAR}/instances/${PGID}" ] || die '实例目录未回收'
if command -v docker >/dev/null 2>&1; then
  docker ps -a --format '{{.Names}}' | grep -qx "mysql-8.4-${MYPORT}" && die '容器未删除'
fi
ok '删除实例并回收容器与目录'

# 命名卷的删除语义：keepData=false 必须连卷一起删（以前无条件删卷，
# "保留数据"反而把数据删了；现在两个方向都要验）
if command -v docker >/dev/null 2>&1; then
  VOLID="$(deploy '{"name":"卷库","engineVersion":"8.4","storageKind":"volume"}')"
  [ -n "${VOLID}" ] || die '无法解析卷实例号'
  VOLNAME="niudbm-$(printf '%s' "${VOLID}" | tr 'A-Z' 'a-z')-data"
  docker volume inspect "${VOLNAME}" >/dev/null 2>&1 || die "命名卷没有创建：${VOLNAME}"
  req 200 GET "/instances/${VOLID}"
  contains '"storageKind":"volume"'
  req 200 DELETE "/instances/${VOLID}" '{"confirmName":"卷库","keepData":false}'
  if docker volume inspect "${VOLNAME}" >/dev/null 2>&1; then
    docker volume rm -f "${VOLNAME}" >/dev/null 2>&1 || true
    die '删除数据后命名卷仍然存在'
  fi
  ok '命名卷随数据一起删除'

  # 反方向：keepData=true 必须留住数据卷，否则"保留数据"是假的
  KEEPID="$(deploy '{"name":"留库","engineVersion":"8.4","storageKind":"volume"}')"
  [ -n "${KEEPID}" ] || die '无法解析留库实例号'
  KEEPNAME="niudbm-$(printf '%s' "${KEEPID}" | tr 'A-Z' 'a-z')-data"
  docker volume inspect "${KEEPNAME}" >/dev/null 2>&1 || die "命名卷没有创建：${KEEPNAME}"
  req 200 DELETE "/instances/${KEEPID}" '{"confirmName":"留库","keepData":true}'
  docker volume inspect "${KEEPNAME}" >/dev/null 2>&1 || die '选择保留数据后命名卷却被删了'
  # 自检留下的卷不能堆在开发机上
  docker volume rm -f "${KEEPNAME}" >/dev/null 2>&1 || true
  ok '保留数据时命名卷仍在'
else
  skip '没有 docker CLI，无法核对命名卷'
fi
