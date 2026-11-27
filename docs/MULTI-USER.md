# 多用户与双端架构（V1.0 强制需求）

## 1. 产品范围
- Windows Electron 客户端与 Web 管理端共享 apps/web 的 Vue3/TypeScript UI、API 类型和业务逻辑；用户已确认相同界面可接受。1536×864 原图是桌面权威视觉基准，不重新设计。
- Windows 安装包内置编译后的前端资源，不仅是打开服务器 URL；桌面桥接仅提供独立窗口、托盘、开机启动、连接设置等必要能力。Electron sandbox、contextIsolation 开启，nodeIntegration 关闭，IPC 白名单，远程网页不得获得桌面桥接。
- 云端模式：Go 管理中心与浏览器运行节点分离，客户端和 Web 端通过同一服务连接本人浏览器画面。支持单机 Compose 和多节点部署。纯本地模式：同一 Go 服务运行在本机，默认仅 loopback，独立本地 profile。
- 每位使用者拥有自己的 BrowseAI 登录、自己的 ChatGPT 账号及订阅、自己的 GitHub 授权；禁止共享订阅、转售 ChatGPT 访问或跨账号规避服务限制。ChatGPT 登录由本人在官方页面手动完成，不收集密码、Token 或 Cookie。ChatGPT 登录过期/额度不足时暂停并由本人处理。
- 默认人工确认网页指令发送；Git 项目状态及 CI 可自动监控、调度。未经明确平台授权不得启用无人值守网页交互，不读取聊天正文、截图 OCR 或内部请求。所有 CI 连接本地 mock，不使用真实 ChatGPT 账户。

## 2. 身份及权限模型
每个 BrowseAI 用户默认拥有一个独立 tenant/workspace；user_id 和 tenant_id 为独立不可猜测 UUID。允许后期扩展团队协作，但不共享个人浏览器登录会话。
- user：管理自己的项目、任务、会话、GitHub 授权与设置；无跨租户查询权限。
- platform_admin：管理用户启用/禁用、配额、节点健康、资源占用与脱敏事件；不具有读取私人聊天、凭证、远程画面或项目内容的默认权限。
- node_agent：机器身份，仅执行被指派租户/实例的生命周期与资源操作；短期凭证、轮换、双向 TLS 或等效认证。
- 登录：密码使用 Argon2id 或成熟 OIDC；支持吊销、会话期限、登录限速、CSRF、安全 Cookie；桌面端使用安全系统凭据存储，不保存明文密码。
- 服务端从经过认证的 principal 派生 tenant_id，禁止信任客户端 header/query/body 传入的身份。默认拒绝，无权限对象统一 404 或 403。WebSocket、导出、日志、分页、缓存及下载一并隔离。
- 管理员创建账号/禁用账号/配置资源配额必须审计；生产不默认开放注册，不提供默认口令。

## 3. 数据与 Git 控制隔离
users、tenants、memberships、auth_sessions、github_installations、nodes、browser_instances、browser_profiles、remote_leases、resource_limits。projects/tasks/stages/dispatches/ci_runs/events/leases/audit_logs 均带 tenant_id，外键及唯一约束包含租户键；敏感数据加密、密钥不入库，数据库行级策略与服务层校验双重保障。
- GitHub 授权按租户隔离，Webhook 通过安装 ID 映射获授权仓库，验签与去重；不得以请求参数切换安装身份。
- 同一 GitHub 仓库稳定 ID + 代码分支的写入租约为全局约束（跨租户），防止两个有权限的用户并发修改；冲突仅返回通用占用提示，不披露他人信息。
- 状态控制分支仅保存项目工作产物与必要状态，不保存用户凭证、私人会话 URL 或聊天记录；任务 ID 全局唯一、状态版本单调递增。
- 配额由管理中心事务化预留：每租户窗口/任务数、CPU/内存/存储限额；总容量不足排队。默认建议每人3个窗口和3个活跃项目，实际能力须压测，不能代表 ChatGPT 账号额度。

## 4. 浏览器节点与会话隔离
每用户独立浏览器运行容器或同等 OS 级隔离环境，独立加密 profile、文件目录、显示服务、网络策略、CPU/内存限制；同一用户可开多窗口共享本人 profile；禁止不同用户共享 Chrome profile、VNC 显示器、控制端口、缓存、剪贴板或上传目录。每个 profile 仅一个写进程。
- 管理中心通过注册、心跳、能力/容量、分配租约、fencing token 管理节点；节点断线先暂停再对账，禁止双重启动同一 profile。
- 远程显示通过受认证的短期访问票据绑定 tenant/user/instance，读与控制权限分离，控制租约独占、到期/登出即回收；VNC/CDP 不暴露公网；禁止跨租户转发票据，禁止录屏及输入日志。
- 删除账户时吊销会话和 GitHub 授权，释放租约，清理本人 profile/数据；备份和恢复严格隔离。平台运维拥有宿主机特权的现实风险须向用户说明；应用层管理员不等于隐私绝对不可访问。

## 5. 客户端与 Web
同一套三栏 UI，登录页与管理员页另做；Windows 端填写服务器地址后登录 BrowseAI，再在自己远程浏览器中登录 ChatGPT。Web 同账号共享自己的云端实例；在一个端接管时另一端只读。桌面独立窗口、全屏、分屏、托盘、本地/云端切换；手机可管理与接管。系统设置内提供节点、用户配额及脱敏健康状态（仅管理员）。
发布物：Windows NSIS 安装版、便携版 EXE、Web ZIP、Linux Go 二进制、Compose、OCI 镜像、校验文件。Windows 打包使用 Windows runner；签名凭证不在仓库，未配置签名时明确标注未签名。

## 6. 必须通过的安全和功能测试
- 用户 A/B 使用 mock 登录，项目/任务/日志/文件/会话/凭证/WS 互不可见；伪造 tenant_id、对象 ID、短期票据、过期票据均失败。
- A/B 不同浏览器 profile；cookie/storage/clipboard 隔离；关闭 A 不影响 B，重启后本人会话恢复。
- admin 能禁用用户与调配额度，但不能默认查看聊天画面；禁用即时撤销控制租约。
- 并发资源争抢不超配；跨租户同 repo+branch 全局锁；节点故障不重复分配。
- 同一 Vue3 构建产物供 Web/Electron 使用；Windows 安装/便携、启动、连接、本地模式、窗口与退出测试；CI 不连接真实 ChatGPT。
- V1.0 之前进行至少两个测试租户端到端验收，真实个人 ChatGPT 登录由用户手动验证；不宣称现已实现或获得平台授权。
