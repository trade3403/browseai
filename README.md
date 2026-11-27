# BrowseAI

自托管 AI 浏览器与 GitHub CI 任务控制工作站。云端运行真实浏览器，本地通过 Web 管理界面查看和人工接管；AI 仅使用官方 ChatGPT 网页，不接入付费 OpenAI API。

## 设计文档（V1.0 规划，尚未实现）

- [产品开发需求](docs/PRD.md)
- [Git 状态控制协议](docs/GIT-CONTROL.md)
- [开发阶段与 CI 验收](docs/CI-ROADMAP.md)
- [开发执行与交接规则](AGENTS.md)
- [UI 1:1 视觉验收规范](design/UI-SPEC.md)
- [视觉基准清单](design/visual-baseline.json)

目标界面：深色三栏工作站，左侧项目列表，中间远程浏览器，右侧 GitHub CI、进度及任务控制；顶部多窗口标签和人工接管按钮。

默认人工确认网页指令发送；自动调度仅处理用户授权的 GitHub 项目状态。未经明确授权不启用无人值守网页交互，不抓取聊天正文、内部接口或凭证，不规避平台限制。CI 使用本地模拟站点和测试仓库，不连接真实 ChatGPT。

状态：需求设计阶段；没有实现代码或 CI 验收结果。

用户提供的 1536×864 原图为唯一桌面视觉基准，原图待上传至 design/screens/browseai-desktop.webp。未通过视觉 CI 不得宣称 V0.1 完成。
