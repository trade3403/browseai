# 开发状态

日期：2026-11-27
当前：V0.1 开发中，分支 feat/v0.1-foundation。V1.0 多用户、Windows/Web 共享 UI 与分离节点架构已经用户确认，详见 MULTI-USER.md。

| 阶段 | 状态 |
|---|---|
| V0.1 基础工程、UI、租户边界 | IN_PROGRESS |
| V0.2 多租户认证、Git 控制协议、数据库 | NOT_STARTED |
| V0.3 GitHub CI 与独立授权 | NOT_STARTED |
| V0.4 独立浏览器节点及远程显示 | NOT_STARTED |
| V0.5 调度与确认 | NOT_STARTED |
| V0.6 交接恢复 | NOT_STARTED |
| V0.7 多用户管理、Electron | NOT_STARTED |
| V0.8 安全及部署 | NOT_STARTED |
| V0.9 综合验收及 Windows 打包 | NOT_STARTED |
| V1.0 发布 | NOT_STARTED |

已提交 Go mock API、Vue 三栏原型、状态转换单测、Playwright 功能与视觉测试、Compose、租户访问边界及 CI 工作流。Go 本地 go1.23.2 下 go test -race -cover、go vet、go build 已通过；覆盖率 73.5%，未达到整体目标 75%，尚需改进。代码证据提交 5f8285e5196c2c4d8b65dda8d5423cf0737d7618；CI 配置提交 4af7b597f1d4e1f018982ba4a55a275f2dad9cd5，远端 PR CI 已失败，详见下方记录。没有生产登录、数据库或真实浏览器能力。

阻塞：原图 design/screens/browseai-desktop.webp 尚未入库（SHA-256 已记录）；视觉门禁不得跳过。前端依赖 lockfile 尚未生成，CI 暂用 npm install，正式验收前提交 lockfile 并切换 npm ci。UI 误差未知，Compose 已通过，功能测试尚未运行成功。Go 整体覆盖率未达目标。默认网页发送需人工确认，无人值守授权未获得。

下一步：核查当前 PR 和 Actions，修复失败；上传原图原始二进制并验证指纹；提交 lockfile；执行真实截图对比和 UI 修正。全部必需检查通过之前不进入 V0.2。

## 2026-11-27 CI 修复记录
PR #1，上一轮证据 SHA 8b55da9691810d3c6263689a871ecb4fc799510e，运行 https://github.com/trade3403/browseai/actions/runs/45921782957：backend、compose 通过；web 因 ESLint Vue 解析器被 TypeScript 解析器覆盖失败；functional 因 Playwright 1.50.1 不支持 Ubuntu 26.04 失败；visual-desktop 缺失原始图失败；gate 失败。
本次修复 Vue ESLint parser 配置顺序、固定 Ubuntu 24.04，调整跨平台 E2E 启动目录与移动端最小宽度；新增 windows-foundation 实际 Windows Go EXE 测试、共用前端构建与 Chromium 功能测试；新增依赖锁文件 CI 产物用于后续固化。尚未完成 Electron，不得宣称 Windows 安装包已生成；完整客户端阶段门禁见 CI-ROADMAP §9。原始二进制、lockfile、视觉差异和覆盖率仍未达验收。新提交 CI 待核实，不进入 V0.2。
