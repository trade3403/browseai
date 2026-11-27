# BrowseAI 开发代理执行规则

目标仓库 trade3403/browseai。先读 README.md、docs/PRD.md、docs/GIT-CONTROL.md、docs/CI-ROADMAP.md、docs/STATUS.md（若存在）。以 Git 实际状态为准，不从头重复实现，不编造结果。

从 V0.1 开始逐阶段开发，当前 CI 成功且阶段验收通过才继续；CI 运行中只检查状态；CI 失败优先修复并重新测试；无证据不判定通过。每阶段小步提交、独立 PR，记录真实 SHA、CI URL、测试结果和未完成事项。保守估计上下文，提前提交交接文档并停止继续开发；全部完成后停止。

硬性约束：只使用官方 ChatGPT 网页作为 AI 交互渠道，不调用 OpenAI API；不得抓取聊天正文、提取凭证、逆向私有接口、规避限制。默认人工确认网页发送，真实无人值守交互必须有明确授权；所有 CI 用 mock。Git 仅保存正常工程产物和必要状态。公开仓库不得存秘密、真实会话 URL 或私人信息。不得未经批准合并、force push、发布或部署生产。遇到权限或证据不足，记录 NEED_HUMAN。

本阶段只交付实际代码、可运行测试、文档和可核验的 CI 证据，不承诺未来后台继续执行。

UI 硬性要求：先读 design/UI-SPEC.md 和 design/visual-baseline.json，按用户原图 1536×864 进行 1:1 还原。禁止擅自更改设计、放宽阈值或将整张截图作为 UI；原图缺失则视觉验收 BLOCKED。
