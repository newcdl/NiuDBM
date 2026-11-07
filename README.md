# 牛库管家 NiuDBM

牛库管家是运行在飞牛 NAS（fnOS）上的数据库管理与协作应用。它既可以接管已有数据库，也可以在应用内创建和管理独立的数据库服务，适合个人、开发者和中小型团队。

当前版本：**v1.0.0**

## 功能

- 接入已有 MySQL / MariaDB、PostgreSQL、MongoDB。
- 在应用内通过 Docker 创建托管 MySQL / MariaDB、PostgreSQL、MongoDB 服务。
- 管理逻辑数据库、集合、账号和库级权限。
- 查看服务状态、配置文件、运行诊断和部署日志。
- 按库执行备份与恢复，支持定时备份和全局备份中心。
- 提供任务中心、操作审计、全局设置和 Webhook 通知。
- 服务级成员权限：只读成员、运维者、服务管理员。
- 支持 PC 和手机页面，支持深链、刷新与浏览器返回。
- 提供 x86_64（amd64）与 ARM64 两套飞牛安装包。

## 支持范围

| 引擎 | 外部接入 | 应用内托管 | 备份恢复 | 配置文件 |
| --- | --- | --- | --- | --- |
| MySQL / MariaDB | 支持 | 支持 | 托管服务支持 | `conf.d` 片段 |
| PostgreSQL | 支持 | 支持 | 托管服务支持 | `postgresql.conf`、`pg_hba.conf`、`postgresql.auto.conf` |
| MongoDB | 支持 | 支持 | 托管服务支持 | `mongod.conf` |

外部数据库由原数据库主机负责备份和配置文件。在牛库管家侧，只管理连接、数据库、账号和权限。

## 技术架构

- 后端：Go 1.24，标准库 HTTP，SQLite 元数据库。
- 前端：Vue 3 + TypeScript + Vite + Element Plus。
- 托管引擎：Docker 官方镜像。
- 飞牛接入：Unix Socket + 统一网关，页面前缀 `/app/niudbm`。
- 单二进制发布：前端产物通过 `go:embed` 打入后端。

## 目录

```text
niudbm/                 飞牛应用清单、生命周期脚本和资源声明
server/                 Go 服务、数据库适配器和测试
web/                    Vue 前端
scripts/                构建、打包和冒烟测试
doc/                    当前版本产品、开发和测试文档
dist/                   本地构建产物，不进入 Git
```

## 本地开发

### 环境要求

- Go 1.24+
- Node.js 20+
- pnpm
- Docker（验证托管数据库时需要）

### 启动后端

```bash
cd server
go run ./cmd/niudbm --addr 127.0.0.1:8780 --prefix /app/niudbm
```

开发模式不读取平台身份头，会以本地管理员身份运行。

### 启动前端

```bash
cd web
pnpm install
pnpm dev
```

访问 `http://127.0.0.1:5173`。

## 验证

```bash
cd server
go vet ./...
go test ./...

cd ../web
pnpm exec vue-tsc --noEmit
pnpm build
```

完整发布构建：

```bash
scripts/build.sh --pack
```

默认同时构建 `amd64` 和 `arm64`，产物位于 `dist/`。

## GitHub 自动发布

推送 `vMAJOR.MINOR.PATCH` 格式的标签会触发 GitHub Actions：

- 校验标签版本与 `niudbm/manifest` 一致。
- 运行 Go 检查、单元测试、前端类型检查和生产构建。
- 使用飞牛官方 `fnpack` 生成 `amd64` 与 `arm64` 两个 FPK。
- 上传工作流产物并创建 GitHub Release。

```bash
git tag -a v1.0.1 -m "牛库管家 v1.0.1"
git push origin v1.0.1
```

发布新版本前，需要同步更新 `niudbm/manifest`、服务默认版本、前端版本和 `CHANGELOG.md`。

## 飞牛部署

- `niudbm/main` 负责启动 Go 服务。
- 页面通过 `app.sock` 由飞牛统一网关访问 `/app/niudbm`。
- 托管数据库依赖 Docker，应用运行用户需要加入 `docker` 组。
- 数据目录、备份目录和导出目录由 `niudbm/config/resource` 声明。
- 普通飞牛账号登录后只能看到被授权的服务；NAS 管理员可管理全部服务。

## 数据与安全

- 元数据库位于应用私有 `var/niudbm.db`。
- 外部连接密码使用 AES-GCM 加密后保存在应用私有 `etc/secrets`。
- 会话 Cookie 为 HttpOnly，数据库只保存令牌哈希。
- 托管 MongoDB 强制开启认证，配置文件固定数据目录和容器端口。
- 删除服务时可选择保留数据；彻底删除会产生操作审计。

## 当前不做

- 不提供通用 SQL IDE、表结构设计器或 ETL。
- 不进行远程多机纳管。
- 不管理 NAS 上的所有 Docker 容器，只管理本应用创建的数据库服务。
- 外部数据库暂不由本应用直接执行备份和恢复。

## 文档

- [产品与技术方案](./doc/产品与技术方案.md)
- [开发与验证](./doc/开发与验证.md)
- [真机测试清单 v1.0.0](./doc/真机测试清单-v1.0.0.md)
- [发布与版本管理规范](./doc/发布与版本管理规范.md)
- [变更日志](./CHANGELOG.md)
