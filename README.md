# BrowseAI

自托管 AI 浏览器与 GitHub CI 任务控制工作站。云端运行真实浏览器，本地通过 Windows Electron 客户端或 Web 管理界面查看和人工接管；AI 仅使用官方 ChatGPT 网页，不接入付费 OpenAI API。

## 设计文档（V1.0 规划，尚未实现）

- [产品开发需求](docs/PRD.md)
- [多用户、双端及节点架构](docs/MULTI-USER.md)
- [Git 状态控制协议](docs/GIT-CONTROL.md)
- [开发阶段与 CI 验收](docs/CI-ROADMAP.md)
- [开发执行与交接规则](AGENTS.md)
- [UI 1:1 视觉验收规范](design/UI-SPEC.md)
- [视觉基准清单](design/visual-baseline.json)

目标界面：深色三栏工作站，左侧项目列表，中间远程浏览器，右侧 GitHub CI、进度及任务控制；顶部多窗口标签和人工接管按钮。

默认人工确认网页指令发送；自动调度仅处理用户授权的 GitHub 项目状态。未经明确授权不启用无人值守网页交互，不抓取聊天正文、内部接口或凭证，不规避平台限制。CI 使用本地模拟站点和测试仓库，不连接真实 ChatGPT。

状态：V0.1 开发中；尚未通过 CI。V1.0 必须支持多用户独立 ChatGPT 登录环境、统一 Windows/Web UI、单机及分布式节点。

用户提供的 1536×864 原图为唯一桌面视觉基准，[原图已入库](design/screens/browseai-desktop.webp)。开发阶段（V0.1–V0.9）视觉门槛经用户批准为整体及各核心区域 <=15%，最终 V1.0 发布验收仍为 <=1%，关键边界始终 <=2px。普通 CI 使用 development；最终候选提交须运行严格 release profile（Actions 手动选择 release，或 v1.* 标签自动强制）。未通过适用的视觉 CI 不得宣称阶段完成。

## V0.1 本地模拟预览
`docker compose -f deploy/compose.yml up --build`，访问 http://127.0.0.1:8088。当前仅模拟项目和远程占位，不用于生产或真实账户登录。
