# 牛库管家 · MySQL 单引擎路线图

> 结论先行：产品收窄成「NAS 上用 Docker 跑 MySQL 的管理面板」。
> 只保留 MySQL 协议系（MySQL / MariaDB 官方镜像），只保留 Docker 运行时，
> 砍掉 SQLite、PostgreSQL、宿主二进制引擎、引擎下载与导入。
> 分 6 个阶段做，每个阶段都能独立发版、独立验证。

---

## 1. 定位与边界

**一句话**：在飞牛 NAS 上，用一个网页把 MySQL 容器管明白——建、连、管库、管账号、备份、恢复、看日志。

- **引擎**：只有 MySQL 协议系。镜像 tag 即版本：`mysql:8.4` / `mysql:8.0` / `mysql:5.7`、`mariadb:11.4` / `mariadb:10.11`。
- **运行方式**：只有 Docker。没有宿主 `mariadbd` 进程、没有引擎归档、没有 pid/socket、没有降权用户。
- **部署模型**：一个「服务」= 一个容器 = 一个端口 = 一份数据（命名卷或宿主目录）；「数据库」= 容器里的 schema；账号授权到库级。
- **不做**：多引擎、主从/集群、K8s、SQL 控制台（已删，不再加）、公网暴露、多租户。

---

## 2. 现状与要删的东西

### 结论表

| 区域 | 现状 | 处理 |
| --- | --- | --- |
| SQLite | `internal/engine` + `instances.go` 里的同步建库/导出/表浏览 | 删 |
| PostgreSQL | `internal/pgctl` + `httpapi/postgres.go` + 前端 PG 页签 | 删 |
| 引擎分发 | `enginecat`、`engines.go`、`enginetask.go`、`enginemigrate.go`、`Engines.vue` | 删（换成镜像管理） |
| 本机 MySQL | `mysqlctl` 里的 Asset/install-db/pid/sock/runAs 分支 | 删（只留容器操作） |
| 通用能力 | 备份、任务、审计、设置、通知、数据库、用户、参数、导入导出 | 保留并按 MySQL 重写 |

### 后端删除清单（M0）

- 整个包：`server/internal/engine/`、`server/internal/pgctl/`、`server/internal/enginecat/`
- 文件：`httpapi/postgres.go`、`engines.go`、`enginetask.go`、`enginemigrate.go`、`resolve_runtime_test.go`
- `instances.go`：SQLite 分支、`dataFile`、`view()` 里的 sqlite 分支、创建时 `engine` 默认值
- `mysqlctl`：`Asset`、`mariadb-install-db`、`SockDir/PidPath/RunAs/LogPath`（本机进程相关）、`ensureBase/procAttr/env/alive`
- `preflight`：去掉"本机引擎是否已装/可用版本"，保留 Docker、端口、磁盘
- `diag`：去掉引擎自检项，加"镜像是否可拉、数据盘余量"

### 前端删除清单（M0）

- `views/Instances.vue`：三引擎页签 → 单一服务列表；删引擎安装条、下载进度、fallback 版本表
- `views/Engines.vue` → 替换为 `views/Images.vue`
- `views/DeployService.vue`：删 native 选项、删引擎清单依赖，版本选择改镜像 tag
- `views/InstanceDetail.vue`：删 PG 专属块（连接串保留，MySQL 通用）
- `api.ts`：删 `listEngines/downloadEngine/importEngine/engineTask/uninstallEngine` 与 `EngineInfo`
- 导航：`概览 / 数据库 / 镜像 / 任务与记录 / 设置`

### 脚本

- `scripts/smoke-in-container.sh` 重写为 MySQL-only：镜像离线 `docker load` → 建服务 → 真实 TCP 认证 → 四档权限 → 备份恢复 → 失败现场 → 删除回收。
- 删 `fetch-engines.sh` 语义（改为 `fetch-images.sh`，导出/导入镜像 tar）。

---

## 3. 目标架构

```
Vue3 + Element Plus
        │  /api/v1 (JSON)
        ▼
httpapi ── 鉴权 / 审计 / 任务 / 部署进度
   ├── store        SQLite 元数据库：instances / backups / jobs / audit / settings / policy
   ├── dockerctl     docker 命令封装：run/exec/inspect/logs/rm/volume/images
   └── mysqlops      MySQL 侧操作：建库、账号、参数、dump、import
        │
        ▼  /var/run/docker.sock
   [mysql:8.4 / mariadb:11.4 容器]
```

关键原则：

1. **容器是唯一事实**：状态一律以 `docker inspect` 为准，台账只存"用户意图"（名字、镜像、端口、配额、存储）。
2. **管理操作走 `docker exec`**：建库、建账号、改参数、dump 都用镜像自带客户端，不依赖宿主任何二进制。
3. **业务查询走 TCP**：用户连接信息永远是 `host:port`，与容器内部实现无关。
4. **失败留现场**：容器只停不删，日志落 `deploy.log`（0.6.1 已实现，继续沿用）。

### 数据模型（instances 精简）

```
id, name, image, image_tag, container, port,
status, status_reason, storage_kind, storage_path,
cpu_limit, mem_limit_mb, created_at, updated_at, deleted_at
```

- 去掉：`engine`、`engine_version`、`runtime`、`mode`、`data_dir`（`image` + `image_tag` 已表达引擎与版本）。
- 保留表：`backups`、`backup_policies`、`jobs`、`audit_logs`、`settings`、`instance_settings`（参数覆盖）。
- 不做 `images` 表：镜像以 `docker images` 实时结果为准，避免两边不一致。

### API 收敛

保留并强化：`/health`、`/diag`、`/instances*`、`/deploy-tasks/{id}`、`/instances/{id}/deploy-log`、`/instances/{id}/{databases,users,config,backups,backup-policy}`、`/instances/{id}/{import,export}`、`/jobs*`、`/audit`、`/overview`、`/settings*`。

删除：`/engines*`、`/engine-tasks*`。

新增（按阶段）：

| 阶段 | 接口 | 用途 |
| --- | --- | --- |
| M0 | `GET /preflight` | 只报 Docker / 端口 / 磁盘 |
| M1 | `GET /images`、`POST /images/pull`、`POST /images/import`、`DELETE /images/{ref}` | 镜像管理 |
| M1 | `GET /instances/{id}/stats` | CPU / 内存 / 磁盘 / 连接数 |
| M2 | `GET /instances/{id}/logs?tail=` | 容器日志 |
| M2 | `GET /instances/{id}/health` | 容器健康 + 探测结果 |
| M3 | `POST /instances/{id}/backups/{bid}/verify` | 备份校验 |
| M4 | `POST /instances/{id}/transfer` | 跨服务迁移 |

---

## 4. UI 信息架构

```
概览        数字卡片（服务数/运行中/异常/磁盘）+ 最近备份 + 最近任务
数据库      服务列表（名称、镜像、状态、端口、库数、账号数、体积、最近备份）
             └ 详情 Tabs：概览｜数据库｜用户｜参数｜备份｜导入导出｜日志｜危险操作
镜像        本地镜像（tag/大小/被几个服务用）｜拉取（加速站/官方源）｜离线导入 tar
任务与记录  进行中任务 + 历史（含审计，合并两个页签）
设置        镜像加速、备份保留、通知、诊断环境体检
```

要点：引擎概念从界面彻底消失——用户看到的只有「服务」和「数据库」。

---

## 5. 阶段计划

### M0 · 瘦身（0.1.1）

**目标**：把产品收窄成 MySQL + Docker，其他一律删掉，界面只剩一条主线。

功能/改动：
1. 后端删包删文件（见第 2 节清单），`instances` 表迁移（老 `engine=sqlite/postgres` 与 `runtime=native` 记录标 `status='unsupported'`，列表顶部提示导出后删除）。
2. 前端三页签 → 单列表；引擎页 → 镜像页（先只读：列出本地镜像 + 拉取）。
3. 冒烟脚本重写为 MySQL-only。
4. 版本 0.1.1，manifest 同步。

验收：
- 代码里 `sqlite`/`postgres`/`pgctl`/`enginecat` 零引用（`git grep` 为空）。
- 建服务、连库、建库、建账号、备份恢复、删除回收，冒烟全绿。
- 升级安装后旧实例（SQLite/PG/native）直接从列表消失，台账软删除，不报错崩溃。

风险：删代码面大 → 分 3 个 commit（后端、前端、脚本），每步都能编译能跑。

### M1 · 部署闭环（0.1.2）

**目标**：从"能装"到"装得省心"。

1. 部署向导：镜像 tag → root 密码（生成/复制）→ 存储（命名卷/宿主目录）→ 资源（CPU/内存）→ 端口（自动/指定）→ 初始化库名。
2. 部署进度五阶段：检查 → 拉镜像 → 起容器 → 初始化账号 → 就绪；失败留现场 + 日志（沿用 0.6.1）。
3. 镜像管理：加速站优先、失败回退官方源、离线 tar 导入、展示"被几个服务使用"、删除前检查占用。
4. 连接信息：host/port/初始库/账号/密码一次给全，一键复制 + 二维码（手机连）。
5. 端口管理：占用检测（台账 + 系统监听 + docker 端口映射）。
6. 容器守护：`--restart unless-stopped`（可在设置里改）、健康检查（`mysqladmin ping` via exec）、启动超时与重试。

验收：全新机器，从零到可用连接串 ≤ 3 次点击；镜像拉取失败能给出可执行结论（加速站/网络/磁盘）；同一端口不会被建两次。

**交付拆分（2026-10-29 定）**：M1 拆两批发小版本，每批都能装真机独立验：
- **0.1.2**：第 1、2、4、5、6 条（向导、五段进度、连接信息、端口占用检测、重启策略）＋ 修复连接地址 `0.0.0.0`、命名卷删除语义、容器无重启策略三个缺陷。清单见 [真机测试清单-v0.1.2](./真机测试清单-v0.1.2.md)。
- **0.1.3**：按真机反馈先做交互（2026-10-29）：服务列表改卡片、概览连接信息给 root + `mysql://` 完整连接串、库与账号合成一页并在库上直接建号、备份恢复改成按库导出 `.sql`。
- **0.1.4**（2026-10-29）：按真机反馈改数据模型——新建服务不再自带 `app` 库、账号支持多库授权、库列表带授权用户、用户管理改左侧抽屉、所有列表支持表格/卡片双视图。
- **0.1.5**（2026-10-30）：账号访问来源（所有人/指定 IP）、密码查看与复制、库级授权管理弹窗、配置页的运行状态与按内存档位的性能方案、可调参数扩到 24 项。
- **0.1.6**：第 3 条镜像管理补全（拉取/导入/删除、"被几个服务使用"、删除前占用检查）＋ 健康检查与启动超时重试、连接二维码。M2 从 0.1.7 起。

### M2 · 日常运维（0.1.3）

1. 生命周期：启动/停止/重启/重建（改端口或配额）、删除（保留数据/移入回收站）。
2. 参数：常用项分组（连接、InnoDB、日志、字符集），写 `conf.d` + 需重启提示；不可改项明确置灰。
3. 日志：容器日志（tail + 跟随）、错误日志、慢查询开关。
4. 数据库：列表（`information_schema` 实时）、新建/删除、默认库保护、字符集/排序规则选择。
5. 账号：建号/改密/锁定/删除、库级权限（read/write/all/none）、系统账号保护、密码强度。
6. 监控：CPU/内存/磁盘/连接数/慢查询数，容器状态异常高亮。

验收：所有运维动作都有审计与任务记录；参数改动后重启生效可验证；不允许删默认库和系统的 `root/niudbm`。

### M3 · 备份恢复（0.1.4）

1. 手动备份：逻辑 dump（`docker exec mysqldump --single-transaction`）与冷备（停容器打包卷）二选一，默认逻辑。
2. 定时策略：每日/每周 + 保留份数/天数，后台调度（已有骨架，改 MySQL-only）。
3. 恢复：整库恢复（先校验 dump 头 + 大小 + checksum），恢复前自动备份一次。
4. 备份管理：列表、下载、上传、删除、校验、备注；备份目录可在设置里改。
5. 通知：成功/失败 Webhook（已有，改文案与字段）。

验收：备份→改数据→恢复→数据一致（CSV 比对）；备份文件可下载后在新机器导入恢复；保留策略真的会删旧备份。

### M4 · 数据流转（0.1.5）

1. SQL 导入（脚本）、整库导出（dump）、单表导出（CSV）。
2. CSV 导入导出：表头映射、空值与 NULL 区分、编码/BOM、批量大小。
3. 跨服务迁移：源 dump → 目标导入，进度可看、可取消。
4. 大文件：分片上传、断点续传（可选）、进度与耗时。

验收：10 万行 CSV 导入不超时；中断后可重试且不留半张表（事务或临时表 + rename）。

### M5 · 打磨发布（0.1.6）

1. 概览仪表盘与异常提醒（容器退出、端口失效、磁盘不足）。
2. 诊断页：Docker 可用性（含 socket 权限提示）、端口、磁盘、镜像、时区。
3. 设置收口：加速站、保留策略、通知、默认资源模板。
4. 发布：fpk 双架构、`join-groups: ["docker"]` 权限声明、升级迁移脚本、上架文档与截图。

验收：干净 fnOS 安装 → 建服务 → 备份 → 恢复 → 卸载，全程无手工命令；升级 0.6.x → 1.0 不丢数据（只丢已不支持的 SQLite/PG 实例，且有提示）。

---

## 6. 兼容与迁移

- **0.6.x → 0.1.1**：`engine=sqlite|postgres` 或 `runtime=native` 的实例一律标 `unsupported`，界面显示"该版本不再支持，请先导出数据"；不自动删除数据目录，避免误伤。
- 版本号从 0.1.1 重新起算：每个阶段只加小版本（0.1.1 → 0.1.2 …），合并到 main 才升大版本（0.2.0）。
- 提供 `doc/从本机引擎迁移到容器.md`：`mysqldump` 导出 → 新服务导入的具体命令。

---

## 7. 实施顺序建议

先 M0（删），再 M1（部署闭环），之后按 M2 → M3 → M4 → M5 推进。
理由：不删干净就做功能，每加一个特性都要在 SQLite/PG/native 三种分支上验证一遍，成本翻倍。
M0 建议拆成三个可独立提交的步骤：后端删除 → 前端收敛 → 冒烟改造。
