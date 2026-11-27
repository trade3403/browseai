# BrowseAI 分阶段开发与 CI 验收

当前仓库初始化，仅设计文档，所有阶段均 NOT_STARTED。以下是必须实施的 CI 计划，非已运行工作流。默认 Ubuntu GitHub-hosted runner；可选专用隔离自建 runner 做 Docker/远程显示验收。不得将真实 ChatGPT 登录 profile、账号、私有仓库令牌传给 CI。

## 1. 阶段顺序（每阶段独立 PR、当前 SHA 验收后才进入下一阶段）

| 版本 | 工作内容 | CI/验收门槛 |
|---|---|---|
| V0.1 | Monorepo、Go/Vue 初始化、Docker 基础、三栏 UI | gofmt/vet/test、前端 lint/typecheck/test/build、模拟界面 E2E；桌面 1920/1366 截图，移动布局 |
| V0.2 | Git 控制 JSON Schema、任务 CRUD、PostgreSQL 迁移、状态机 | schema 正负样本、迁移 up/down、所有合法/非法状态边测试；repo+branch 唯一租约 |
| V0.3 | GitHub App、Webhook、CI 轮询 | HMAC/重复/乱序/分页/限流；SHA 绑定、PR merge SHA、必需 job、缺失/取消/失败测试 |
| V0.4 | 云端 Chrome/noVNC、标签及窗口管理、本地远程查看 | 本地模拟站点多窗、刷新/分屏/全屏、输入、权限、重连；真实登录只做本人手动验收 |
| V0.5 | 指令队列、人工确认、暂停/恢复/停止、调度并发 | 不确认不发送、独占接管、任务隔离、重复事件只建一条指令、STOPPED 无后续派发 |
| V0.6 | Git 交接、新聊天、历史窗口、网络和重启恢复 | mock 交接完整/缺失/冲突；断网/重启/浏览器崩溃/发送结果未知测试，不能盲目重发 |
| V0.7 | 多项目、日志、通知、WebSocket、响应式细节 | 3 活跃项目+10 排队；CI 等待释放槽；跨项目权限；事件补偿；视觉及可访问性 |
| V0.8 | 安全加固、备份/恢复、Docker 部署、本地模式 | secret scan、依赖/SBOM/容器扫描、CSRF/鉴权/远程桌面访问、备份恢复、升级回滚 |
| V0.9 | 全流程及性能、24h soak、文档、发布预演 | mock 全流程 10 次；故障注入；延迟与恢复时间实测；全套矩阵、无严重未关闭问题 |
| V1.0 | 发布验收、版本标签、镜像、安装指南 | 所有必需检查成功、用户签收、发布产物校验、可复现部署和回滚 |

## 2. CI 工作流规划
- .github/workflows/ci.yml：pull_request、main push；concurrency 按 PR 或分支取消过时运行；permissions contents: read；外部 Actions 固定可信完整 SHA，定期更新。
- jobs：lint-backend、unit-backend、lint-web、unit-web、schema、integration、build、e2e、security、docs；V0.1 先落地已有模块检查，新增模块时必需检查同步升级，不允许用空测试冒充。
- integration 使用 PostgreSQL service 和模拟 GitHub HTTP server；测试无真实账号。e2e 使用 Playwright + 本地 mock Chat 页面 + 测试远程桌面，不使用真实 chatgpt.com。
- nightly.yml：定时故障注入、长时间运行、安全扫描；24h soak 在独立测试环境，避免普通 PR 超时。
- release.yml：仅通过受保护 tag 或人工触发；构建 OCI 镜像、SBOM、checksum、部署包；发布权限单独授予，生产部署需审批。
- 控制分支 ai-control/** 不触发代码 CI；避免把必需检查因 path filter 变成永久 pending，必要时设置 always-run 总门禁 gate。

## 3. 标准命令（从对应模块建成后执行）
后端（services/controller）：gofmt -l .（非空即失败）、go vet ./...、go test -race -coverprofile=coverage.out ./...、go build ./...
前端（apps/web）：npm ci、npm run lint、npm run typecheck、npm run test:ci、npm run build
根目录：docker compose -f deploy/compose.yml config；docker compose -f deploy/compose.yml up -d --build；健康检查后运行集成/E2E，finally 归档日志并 down -v（仅临时 CI 环境）。
V0.2 后加入 schema 校验；V0.3 后加入模拟 GitHub 合约测试；V0.4 后加入远程浏览器测试；V0.8 后加入漏洞门禁。Go/Node 选实施时受支持版本，锁定工具链与 lockfile。

## 4. 测试质量门禁
- 调度状态机、Git 协议和权限关键路径覆盖所有规则；关键包行覆盖率目标 >=85%，整体 >=75%；测试必须包含真实断言及负样本。
- UI：1920x1080、1366x768、390x844，截图、交互录像仅针对 mock；1536x864 原图为权威基准，V0.1 必须按 design/UI-SPEC.md 实现对比门禁，以后固定环境视觉回归，变化须审批；禁止掩盖功能失败。
- CI 产物：JUnit、覆盖率、构建日志、mock 截图、Compose 测试日志、扫描结果；敏感信息脱敏，默认保留 14 天。
- 最终 gate 检查所有必需 jobs 成功；不得把失败、skip、cancel 或旧 SHA 视为成功。标记跳过的非必需检查必须说明原因。
- 性能及稳定性报告：运行环境、版本、样本量、P95、恢复耗时、重复派发数；与 PRD 目标对照。没有实测不得标注通过。

## 5. 每阶段执行步骤
1. 读取 PRD、GIT-CONTROL、当前阶段记录和 Git 真实状态；检查最新提交与 CI。
2. CI 运行中不启动冲突工作；失败先读日志，修复并重新提交；若测试设施不可用则记录阻塞。
3. 新建 feat/vX.Y-* 分支或继续当前未完成 PR；只实现本阶段范围，测试和文档同提交。
4. 先本地或 runner 实际执行可用命令，再提交；查看 GitHub Actions 当前 SHA 所有必需检查。
5. 在 docs/STATUS.md 记录版本、PR、代码 SHA、CI run URL、结果、证据、未完成、下一步；记录修改不替代代码 CI，需区分证据 SHA 与文档 SHA。
6. 通过全部门禁才能开始下一阶段；需要合并时按用户审批执行，不擅自合并/部署。
7. 上下文不足时先提交 handoff（目标、当前分支、SHA、CI、未完事项、下一条指令），停止后续代码修改；新聊天从 Git 恢复。
8. 全部完成后最终验收，停止调度。

## 6. V0.1 首次实施清单
- 建立 apps/web、services/controller、deploy、tests/e2e；README 增加启动命令。
- 首页完全按 PRD 三栏布局，mock 数据明确标记，真实可点击项目切换、标签、暂停/恢复、全屏及只读远程占位。
- Go healthz/readyz 和项目 mock API；Compose 可启动前后端；创建 ci.yml 实际运行基础 lint/test/build/e2e。
- 提交阶段 PR，当前 SHA CI 通过后才能宣称 V0.1 完成。

## 7. 1:1 UI 必需门禁
V0.1 必须新增 visual-desktop 检查：验证原图 SHA、固定 1536×864/DPR1、截图差异与关键区域统计、差异图归档、功能断言。参考 design/visual-baseline.json；原图缺失不得跳过或假报成功。后续各阶段持续回归。
