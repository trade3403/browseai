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

## V0.1 第二轮 CI 与覆盖率修复
证据 SHA 929fc80c6984a208cb1d3e2f8378a580267d3d53，PR CI https://github.com/trade3403/browseai/actions/runs/45922087583：backend、web、functional、compose、Windows foundation 全部通过；visual-desktop 缺失原图失败，gate 失败。Windows 已实际执行 Go EXE 编译/启动和 Chromium 功能测试，非 Electron 安装包验收。
本次增加 HTTP 服务配置、响应头、JSON 编码错误测试；本地 go test -race、go vet、go build 通过，覆盖率 85.3%，新增 CI 75% 硬门槛。视觉测试调整为先保存实际截图，再校验原图存在及 SHA，缺失/不符仍必定失败并保留 BLOCKED 报告，不变更阈值。锁文件已从上述 CI 的 artifact 15165390426 下载并验证；原始图与 lockfile 已生成并验证可应用的 Git 二进制补丁，尚待实际入库。当前连接器无法将本地二进制文件或完整锁文件直接传到 GitHub；须通过用户 Git 上传或其他已授权文件传输方式解决。正式验收前 npm ci 与 Docker 依赖锁定仍待处理。新提交 CI 待核实，不进入 V0.2。

## 原始图和 lockfile 已解决
证据 SHA fbab00814a24fde55bcb56f81c7ffa50d74750ac，PR CI https://github.com/trade3403/browseai/actions/runs/45922396803：backend（含75%覆盖率门禁）、web、functional、compose、Windows foundation 均通过；visual-desktop 因缺失原图失败，gate 失败。以上旧记录保留。
本次从经本地验证的 Git 二进制补丁无损解码原始图并调用 GitHub create_blob(base64)，返回 SHA cf3eca7dd025ca45b430980bdf203e3cdd8a8812，与本地原图 Git SHA 完全一致。原始 WebP 路径 design/screens/browseai-desktop.webp；103632 bytes，1536×864，SHA-256 cbbcf34029c6fff16e8ac2449da923a80e83eff8cb980bb531dcd32de3a59c05。CI lockfile 从同一补丁恢复，Git SHA 7f4f6bdde077acb1c65877a8bc23c3ca03ce6c19，与 CI 产物一致；一并入库。CI 和 Docker 改用 npm ci，禁止缺锁回退。原图上传及依赖锁定阻塞已解决；视觉差异仍待新 CI 实测，严禁改阈值，仍在 V0.1，不进入 V0.2。

## 原图入库后首次真实视觉测量
证据 SHA 67c88a53b75c5460ed0ad5f0998c9800087bbd9e；PR CI https://github.com/trade3403/browseai/actions/runs/45922696654。backend、web、functional、compose、Windows foundation 均通过；visual-desktop 和 gate 失败。报告 artifact 15165421705：整图差异14.2137%，外壳16.0748%（不含远程画面），header15.6817%、sidebar14.0646%、workspace_toolbar18.2910%、right_panel19.5277%、footer11.1146%，远程边界误差1.453125 CSS px（已通过2px）。已实际查看 reference/actual 图。首次外壳修订：按原图采样颜色及定位调节导航/项目卡片、标签、右侧卡片及底栏布局，保持真实未连接提示、禁用状态和响应式功能。新差异须由新 CI 测量；基准与1%门槛不变。不进入V0.2。

## 第二次真实视觉测量与定向调整
证据 SHA a0c2b9d151f37b73c864fd85996f88c0c9c2f0f9；PR CI https://github.com/trade3403/browseai/actions/runs/45923184640，push CI https://github.com/trade3403/browseai/actions/runs/45923179558，均已结束失败。backend/web/functional/compose/windows-foundation 五项通过；visual-desktop/gate 未通过。实测 artifact 15165206822：外壳15.2792%（前16.0748%，改善0.7956个百分点），整图13.7859%，header14.3675%、sidebar14.5787%、workspace_toolbar16.8802%、right_panel17.9198%、footer11.0091%，远程边界1.453125px。已核对实际及原图截图；右栏第一卡片文字换行导致整体高度偏移。修订2：限制桌面卡片高度、避免标签换行、调整底栏网格和进度位置、状态点尺寸，并在视觉报告追加DOM几何坐标，供后续定位；保持真实未连接状态、禁用交互、基准原图及1%门槛。新结果尚待CI，不进入V0.2。

## 第三次真实视觉测量及修订
证据 SHA 14e879af30deeea89035f6673886dd5a3d237147；PR CI https://github.com/trade3403/browseai/actions/runs/45926036895，push CI https://github.com/trade3403/browseai/actions/runs/45926031996，均完成失败。backend、web、functional、compose、Windows foundation 五项通过。artifact 15166307886：外壳14.2434%（前15.2792%），整图13.2247%，header14.6701%、sidebar14.2697%、workspace_toolbar16.4994%、right_panel15.5251%、footer10.9219%，远程边界1.453125px。已核对截图及DOM几何。修订3：调整卡片高度和标签位置，补右栏状态图标及明确标记的 mock CI 样例（非真实 GitHub 状态），增加 mock/真实边界 E2E 断言，保持视觉门槛及原图不变。新结果待CI。不进入V0.2。

## 第四次真实视觉测量及定向修订
证据 SHA c2d3ac63cda31bf8d7d11eaaebb9facc614ee58c；PR CI https://github.com/trade3403/browseai/actions/runs/45926699028 与 push CI https://github.com/trade3403/browseai/actions/runs/45926691243 均完成失败。backend/web/functional/compose/windows-foundation 五项通过，视觉与gate失败。artifact 15167150705：外壳13.3510%（前14.2434%），整图12.7449%，header14.2916%、sidebar13.3768%、workspace_toolbar16.0942%、right_panel14.0789%、footer10.4479%，边界1.453125px。实查截图、DOM坐标及采样颜色。修订4：调整工具栏尺寸、进度条基线、面板颜色和底栏分组；为明确标记的本地模拟项目补充阶段、任务及CI细节，仓库链接可打开，CI示例说明可查看，新增跨示例项目隔离断言。所有示例不得视为真实GitHub证据。视觉基准和1%门槛不变，修复效果待新CI验证，不进入V0.2。

## 第五次真实视觉测量与修订
证据 SHA 447f3e4a2ec1578369866c1b06b93ffe2dfed1c0；PR CI https://github.com/trade3403/browseai/actions/runs/45927408523，push CI https://github.com/trade3403/browseai/actions/runs/45927402733，均结束失败。backend/web/functional/compose/windows-foundation 五项通过。artifact 15167525318：外壳13.0587%（前13.3510%），整图12.5877%，header14.2916%、sidebar12.2230%、workspace_toolbar15.0006%、right_panel14.4690%、footer10.4948%，边界1.453125px。读取原图/实际图、JSON及像素颜色，发现底栏时长文本溢出与窗口字段重叠；新增底栏约束、缩短示例文字、真实 GitHub 图标组件和已完成状态图标，调整面板颜色和字体重量，新增底栏相邻元素不重叠断言。仍保留明确的模拟标记，不复制用户头像或伪造线上信息。新效果待 CI 测量，原图与1%门槛不变，不进入V0.2。

## 第六次真实视觉测量及修订
证据 SHA 4220e976a0164d12ac8ac7ef9595339dbab40fe3；PR CI https://github.com/trade3403/browseai/actions/runs/45928100111 和 push CI https://github.com/trade3403/browseai/actions/runs/45928093726 均完成失败。backend/web/functional/compose/windows-foundation 五项通过。artifact 15167232876：整图12.4919%，外壳12.8806%（前13.0587%），header14.3048%、sidebar11.7724%、workspace_toolbar14.9678%、right_panel14.4048%、footer10.1406%，边界1.453125px。已读取真实 reference/actual/diff 和几何报告；主要剩余差异集中在文字、图标、原图示例文案以及原图边缘模糊，布局边界已达标。修订6：矢量品牌标识、真实本地通知计数、导航GitHub标识、工具栏和卡片字体基线及进度位置微调，新增本地日志计数测试。保留模拟提示和禁用状态，不伪造真实服务在线、账户或CI。尚未达到1%验收，新效果待CI，不进入V0.2。

## 第七次真实视觉测量及定向修订
证据 SHA 5c8bfcff631cf9515a256f6c05c038b08a669745；PR CI https://github.com/trade3403/browseai/actions/runs/45928622226 与 push CI https://github.com/trade3403/browseai/actions/runs/45928616908 均已结束失败。backend/web/functional/compose/windows-foundation 五项通过，visual-desktop/gate 未通过。artifact 15167188945：整图12.1496%，外壳12.2439%（前12.8806%），header14.1710%、sidebar10.1835%、workspace_toolbar15.1629%、right_panel13.7085%、footer10.1875%，边界1.453125px。核查真实截图和JSON：工具栏标签实际y=65而设计约y=67，顶部品牌/标语位置偏移；发现第六轮CSS将首张卡片背景固定为蓝色而非按选中状态。修订7：修正顶部和标签位置、选中状态样式及ARIA状态，增加选中状态回归测试；报告附48px差异热点和字体属性，仅诊断，不更改计分、原图、排除范围或1%门槛。原图为压缩WebP，字体/图标、明确的模拟状态文案仍与参考不同，暂不能保证达到1%。新CI待验证，不进入V0.2。
