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

## 第八次真实视觉测量及修订
证据 SHA c11de5ca4d736f9bb8e9433765c13598818d8ea9；PR CI https://github.com/trade3403/browseai/actions/runs/45929271868，push CI https://github.com/trade3403/browseai/actions/runs/45929266519 均已结束失败。backend/web/functional/compose/windows-foundation 五项通过。artifact 15168080745：整图11.9517%，外壳11.8757%（前12.2439%），header12.6808%、sidebar10.1835%、workspace_toolbar13.6875%、right_panel13.7085%、footer10.1875%，边界1.453125px。实查 report.json 的48px热点和截图：右栏任务清单1296,678/1296,726、状态区1344,534及顶部文案为主要差异。修订8：调整右栏任务清单行高、状态字段字体与对齐、工具栏字号；为真实数据驱动的模拟进度条加入ARIA并测试数值与实际长度一致、示例任务非自动执行及切换后隔离。参考进度条视觉长度与其75%文字并非完全一致，保留准确比例，不伪造进度或用户在线信息。原图、阈值、排除范围均未改变。新CI待验证，未进入V0.2。

## 第九次视觉测量与回归修复
证据 SHA 38059991fd8ceec72795d614758b390147c69e82；PR CI https://github.com/trade3403/browseai/actions/runs/45929946046，push CI https://github.com/trade3403/browseai/actions/runs/45929940721 均结束失败。backend/web/functional/compose/windows-foundation 五项通过。artifact 15168700459：整图11.9868%，外壳11.9411%（上一轮11.8757%，回退0.0653个百分点），header12.6664%、sidebar10.1835%、workspace_toolbar14.0958%、right_panel13.7449%、footer10.1875%，边界1.453125px。实查 report.json/actual/reference/diff，任务状态卡的下一步文案换行导致交接文档越出卡片。修订9：恢复已实测较优的第七轮CSS，保留第八轮数据驱动进度条、ARIA及测试；缩短明确标记的示例任务文案（完整提示保留），使任务卡按内容伸展，调整行距、状态标签与工具栏，加入任务卡不溢出及项目切换断言。未改变计分、原图、排除范围或1%门槛。字体和安全标记与原图文案差异仍为主要阻塞；不能保证1%可达。新CI待验证，不进入V0.2。

## 第十次真实视觉测量与字体诊断改进
证据 SHA 0fc2f5fdd98a8c8a8969813b20ff30fe897f255a；PR CI https://github.com/trade3403/browseai/actions/runs/45930511504，push CI https://github.com/trade3403/browseai/actions/runs/45930505914，均结束失败。backend/web/functional/compose/windows-foundation 五项通过。外壳11.7695%（前11.9411%），header12.6808%、sidebar10.1835%、workspace_toolbar13.8126%、right_panel13.3654%、footer10.1875%。当前本地容器存在 Chromium 与中文字体，但无法解析 raw.githubusercontent.com，也无完整前端依赖，不能声称本地页面验收。为避免反复猜测字体，在 visual.spec.ts 增加独立诊断测试：同一次CI按相同阈值分别渲染当前、Noto优先、system-ui优先、Liberation优先、YaHei优先五种字体栈，输出原始截图、各区域差异、实际Chromium平台字体和边界误差；诊断不改变生产样式或正式验收的actual.png、原图、1%门槛及排除区域，不能使用候选评分冒充通过。先收集证据再决定是否修改字体。没有宣称本轮UI已改善，不进入V0.2。

## 第十一轮视觉测量与分区诊断
证据 SHA 02b25d3670848a581bb07e67b23d7b3e85641fa0；PR CI https://github.com/trade3403/browseai/actions/runs/45931058951，push CI https://github.com/trade3403/browseai/actions/runs/45931053054，均已完成失败。backend/web/functional/compose/windows-foundation 五项通过；visual-desktop 中原图验收失败、字体诊断通过。artifact 15168622393，实查 font-report.json、report.json 和 reference/actual：当前与 Liberation 优先均为11.7695%，system-ui 12.0749%，Noto 优先和 YaHei 优先均12.0827%，均边界1.453125px。实际当前渲染为 Liberation Sans 和 Noto Sans CJK SC；YaHei 候选使用回退字体而非已安装真实YaHei。正式外壳11.7695%，整图11.8945%，各区域与上一轮一致。保留当前字体栈。原图示例版本、在线状态、账户、通知数、任务文案、进度条像素和实际模拟状态不一致，不能通过伪造状态来消除。修订10：调整工具栏文字/按钮间距与任务卡行距；新增12种分区排版候选诊断、按区域排名和溢出标记，正式验收不使用候选结果，不改变原图、阈值、远程区域排除或1%门槛。新CI待验证，尚不能进入V0.2。若诊断仍无明显改善，需获得原始可编辑UI设计稿、确切字体及图标素材，或由用户明确批准其他视觉验收办法；不得自行放宽。

## 第十二轮视觉测量：采用实测最优候选并明确阻塞
证据 SHA 2f1ac17ad86464627fcab6c812a5bfe501e8c441。PR CI https://github.com/trade3403/browseai/actions/runs/45931831405 和 push CI https://github.com/trade3403/browseai/actions/runs/45931825577 均已结束失败。PR 的 backend/web/functional/compose/windows-foundation 五项通过，visual-desktop 中正式验收失败，字体与排版诊断均通过。正式外壳11.7591%（前11.7695%），header12.6808%、sidebar10.1835%、workspace_toolbar13.7341%、right_panel13.3654%、footer10.1875%；边界1.453125px。依据 visual-desktop job 140285978330 的 LAYOUT_DIAGNOSTICS 输出，12个候选中 toolbar-type-1 的工具栏差异13.5814%、外壳11.7390%，footer-type-2 的底栏差异10.1641%、外壳11.7566%；两个候选均无检测到的溢出，边界不变。仅采用这两个候选的样式，组合效果尚未验证；其他候选无收益，不采用。字体维持当前。原图、正式验收计分、1%门槛、排除范围及功能交互不变。阶段V0.1仍未通过，V0.2不得启动。多轮有证据的优化后仍距目标甚远，不能将布局边界达标等同于像素验收通过。若组合实测仍失败，应避免重复无依据微调；优先取得可编辑设计稿/准确字体与图标素材，或请求用户明确批准调整验收方式。未经批准不改变验收标准，不假报通过。

## 用户批准开发阶段视觉门槛调整（2026-11-27）
证据 SHA df3cbf54f4b3a937fd7038854dc9fee3894d3d46；PR CI https://github.com/trade3403/browseai/actions/runs/45932386067 与 push CI https://github.com/trade3403/browseai/actions/runs/45932380662 均完成失败，按当时 1% 规则历史结果仍为失败。外壳 11.7364%，header12.6808%、sidebar10.1835%、workspace_toolbar13.5814%、right_panel13.3654%、footer10.1641%，边界1.453125px，五项非视觉检查及两项诊断通过。此前 NEED_HUMAN 记录：https://github.com/trade3403/browseai/pull/1#issuecomment-7208830351。用户明确批准“同意将开发阶段UI视觉误差标准调整为15%，保留最终视觉验收要求，重新运行CI，通过后继续下一阶段开发。” 现 V0.1–V0.9 采用 development 门槛 15%，V1.0 最终 release 门槛仍 1%，整体与各核心区域均适用；2px 边界、0.1 色差、原图 SHA、排除区域、所有功能门禁均不变。测试同时输出两个门槛独立判定，CI 手动支持 release 且 v1.* tag 强制 release；不进行实际发布。此提交仅更新验收政策、实际执行代码与文档，不改 UI 或伪造历史结果。新 CI 尚未实测；全部通过后才开始 V0.2，最终视觉未完成项必须保留至严格发布验收。

## V0.1 开发门禁通过，启动 V0.2（2026-11-27）
已验收 SHA 7c36fdcdd275d2765625731a01bc418823d7bbd6；PR CI https://github.com/trade3403/browseai/actions/runs/45937506693 和 push CI https://github.com/trade3403/browseai/actions/runs/45937499945 均 success，七项 job 全部成功。VISUAL_ACCEPTANCE: developmentPassed=true, releasePassed=false，外壳11.7364%、最大核心区域13.5814%、边界1.453125px。V0.1 开发验收完成，最终1%视觉债务保留。V0.1 PR #1 未合并。V0.2 叠加分支 feat/v0.2-task-control 从已验收 SHA 创建，独立草稿 PR 指向 V0.1 分支，未经授权不合并。首批实现 JSON Schema 2020-12 config/tasks/progress/result、模拟样本、离线跨文档校验、负样本测试及 schema 必需 CI job。tasks/result 字段为首次具体化约定；尚未实现服务端接入、真实 Git 可达性/CI 验证、handoff 验证、多租户认证、PostgreSQL、任务 CRUD、状态机及全局租约，V0.2 尚未完成，新 CI 待验证。

## V0.2 第二批：任务状态机核心
证据 SHA bd0379c60665c2fa67bbd5c913fac0c3f15ea852，PR CI https://github.com/trade3403/browseai/actions/runs/45939000172 与 push CI https://github.com/trade3403/browseai/actions/runs/45938986923 均 success，8 项必需检查通过（含 schema）。本批加入 Go 纯领域状态机 task_state.go 与单元测试：revision 乐观并发、人工发送确认、匹配 SHA 的 CI 证据、最终用户确认、修复次数上限、暂停/离线对账恢复、交接验证和不可恢复终态。内部 evidence 仅为已验证事实的输入契约，不能直接信任 HTTP 请求；没有自动发送网页操作。当前尚无数据库原子写入、真实证据适配器、认证或 API 接入，不视为生产完成。保留 V0.2 数据库迁移/认证/任务 CRUD/全局租约及持久化测试待办。新 CI 待验证。

## V0.2 第二批格式修复（2026-11-27）
证据 SHA 6efc061fdc57cf2c60fdf13c325c552e1bdf9e2e，PR CI https://github.com/trade3403/browseai/actions/runs/45940439291、push CI https://github.com/trade3403/browseai/actions/runs/45940434727 均失败。PR 中 schema/compose/functional/web/visual-desktop/windows-foundation 六项通过；backend 在 gofmt 格式检查失败（job 140313264639），gate 随之失败；Linux 后续 vet/race/coverage 尚未执行。读取源码发现 taskAction 常量组整体多一空格对齐，本次仅修正对齐，不改变状态机逻辑，并使 CI 格式门禁失败时打印文件列表和 gofmt diff，保留强制失败。容器无法解析 GitHub raw 域名，未运行完整本地仓库测试，修复待新 CI 验证。尚不进入数据库开发。

## V0.2 第三批 PostgreSQL 基础（2026-11-27）
证据 SHA 1363546ece837f7cb2665c7fd8f137dfcb27cd71，PR CI https://github.com/trade3403/browseai/actions/runs/45941890846、push CI https://github.com/trade3403/browseai/actions/runs/45941885624 均 success，8 项检查通过。任务状态机核心通过当前阶段 CI。新增 PostgreSQL 16 初始 up/down migration、身份/租户/会话/项目/任务/阶段/全局 repo ID+branch 租约表、复合外键、任务状态约束、RLS 默认拒绝及独立应用角色权限。新增真实 PostgreSQL service CI，测试双租户读写隔离、CAS revision、跨租户 FK、并发租约唯一性及 fencing token、迁移回滚/重建，纳入 gate。当前容器没有 psql/postgres/docker，未本地运行数据库测试；新 CI 待验证。尚未接入 Go 数据库驱动、认证、事务级任务 CRUD/租约续期及真实运行配置，现有 Compose 仍为 mock；不能视为 V0.2 已完成。下批依据 CI 结果推进。

### 数据库批次工作流配置修复
SHA fa04258af5d42a975f3f61c15cc6db49b1e54ce4 push CI https://github.com/trade3403/browseai/actions/runs/45943338934 已失败，API 返回 0 jobs，未执行数据库测试。check-suite 详细错误接口无法访问；静态检查发现 job.services.postgres.ports 动态端口引用错误地位于 job env，改至实际 psql 步骤 env（允许 job context），其余测试与门槛不变。此为工作流配置修复，待新 CI 证实，尚不开始下一批。

静态复查另发现回滚断言中的 SQL 双引号被多余转义，修正为独立 remaining 变量并显式 ON_ERROR_STOP；不修改测试预期，待新 CI。

## V0.2 第四批：会话认证及最小权限数据库接口
证据 SHA eba29ec693e9d057ea9424ec653ebd947a58d642；PR CI https://github.com/trade3403/browseai/actions/runs/45943420860 与 push CI https://github.com/trade3403/browseai/actions/runs/45943415212 均 success，九项必需检查全部通过，数据库迁移、隔离及全局租约约束通过真实 PostgreSQL 测试。新增 0002_auth up/down，独立 browseai_auth 角色只能调用受控会话函数，无直接身份或项目表权限；8 小时会话、32 字节随机令牌、SHA-256 摘要、会话撤销、禁用及过期检查。Go 增加 sessionService 和 database/sql 参数化 adapter，以及领域单测；真实 PostgreSQL 测试检查双租户身份、权限、过期、撤销和回滚。当前容器无法连接 Go proxy，未引入未验证依赖，尚未配置 PostgreSQL Go driver/连接池 bootstrap、Argon2id/OIDC 真实身份验证、登录 HTTP 接口、Cookie/CSRF、限流或项目任务 CRUD；issueVerified 仅允许认证成功后的可信调用，不对 HTTP 暴露；运行仍 mock。新 CI 待验证，不进入下一批。

## V0.2 第五批：登录 HTTP 层及依赖验证
证据 SHA 65f4eb0ea95103af309d2b13a0df32e4744fba85，PR CI https://github.com/trade3403/browseai/actions/runs/45944860120 与 push CI https://github.com/trade3403/browseai/actions/runs/45944855613 均 success，九项必需检查通过。新增独立 authHTTP handler，可信 credentialVerifier 注入，登录/退出/me、严格 HTTPS Origin、Secure HttpOnly SameSite Strict cookie、令牌轮换、请求大小限制、每来源及全局限流和并发 KDF 槽位；使用合成凭证的 httptest。尚未挂载至默认 mock routes。当前容器实查 Go 1.23.2、无缓存依赖，proxy.golang.org DNS 失败，无法直接获取可信 go.sum。新增独立 dependency-probe 模块及 CI job，使用 Go 官方代理和 sumdb 验证候选 lib/pq v1.10.9、x/crypto v0.36.0 及传递依赖，输出 go.sum 供下一批接入；候选版本需在生产发布前进行安全升级/审计。不是正式运行依赖。真实 Argon2id verifier、凭证查询、SQL driver、连接池、生产路由、任务 CRUD 和事务级租约尚待实现；本批不视为完整登录上线。限流为单进程基础，后续需多实例及可信代理策略。新 CI 待验证。

## V0.2 第六批：真实 Argon2id 与 PostgreSQL 登录接入
证据 SHA a7592f3b7c3f1c1c1b6633ae39fd3f5cca5e313f，PR CI https://github.com/trade3403/browseai/actions/runs/45946338279 与 push CI https://github.com/trade3403/browseai/actions/runs/45946333829 均 success，10 项检查全部通过。依赖校验 job 140331786350 通过官方 sumdb，读取真实 go.sum 日志并写入 controller，固定 lib/pq v1.10.9、x/crypto v0.36.0、x/sys v0.31.0；发布前仍需安全审计及升级。新增 0003 credential migration、Argon2id 固定参数及随机盐/恒时比较/未知账号 dummy KDF、真实 SQL verifier、认证独立运行模式与数据库权限/就绪检查。Docker 构建包含实际 Go 源码与锁定依赖。新增真实 PostgreSQL 两账号登录/me/撤销/禁用与错误密码测试；CI 使用一次性测试密码及非 TLS 测试 DSN，实际运行强制 verify-full 与 loopback/TLS 代理。当前仅认证 API 接入，默认 Compose 和视觉仍 mock；用户开通工具、登录页面、项目任务 CRUD、事务级租约、密码变更及重新启用时的彻底撤销、生产部署等未完成。本批新增代码未在本地执行完整测试，新 CI 待验证。V0.2 不视为完成。

### 第六批 CI 工作流修复（2026-11-27）
证据 SHA 40dc5611c0d33134844a52dfc28c8b5ac10a97db；push CI https://github.com/trade3403/browseai/actions/runs/45947817007 已结束失败，API 返回 0 jobs，未执行 Go/数据库/前端等测试。直接读取该 SHA 的 ci.yml 确认编辑时 JavaScript replace 的特殊替换符导致测试正则尾部美元符号与单引号被展开为原工作流后缀，造成命令截断及重复 job 块。恢复唯一完整 workflow，使用完整的 go test -mod=readonly -run '^TestPostgresAuthIntegration$' -count=1 -v 命令，保留所有 9 个必需子 job 及 gate、go mod verify 和只读 vet。提交前核对 job/trigger 名称唯一性及完整测试命令；未运行真实 Runner，待新 CI。尚不进入 CRUD 开发。

## V0.2 第七批：项目 CRUD 与独立应用连接池
证据 SHA ca8a8ed36ae9965cd652b4da37df9cb6ebeed980；PR CI https://github.com/trade3403/browseai/actions/runs/45949212776 与 push CI https://github.com/trade3403/browseai/actions/runs/45949206219 均 success，10 项通过，真实 TestPostgresAuthIntegration 通过，原单测覆盖率 77.0%。本批新增项目 GET/POST/PATCH/DELETE、事务内 RLS tenant context 与显式 tenant 条件、UUID、100 条游标分页、revision 冲突、存在任务时禁止删除、只允许更名（仓库/分支身份不可直接修改）、安全 Origin 与认证身份校验、独立 browseai_app 数据库连接池及真实双租户 API 测试。真实 GitHub 仓库 ID 暂为用户配置，不能当作授权或已验证身份，V0.3 才对账授权；不派发任务。整体后端覆盖率门槛仍 >=75%，从仅单测的 backend job 移至必需 database job 的完整单测+真实数据库集成+race 测试，backend 继续执行 gofmt/vet/race/build。新代码未本地跑完整依赖/数据库，待 CI 验证。默认 Compose 保留 mock；任务 CRUD、持久化状态机、租约操作与用户开通仍待完成。未进入 V0.3。

## V0.2 第八批：任务 CRUD 与状态机事务持久化
证据 SHA 2b2b53d5c2ddacfb82cbbbe75c7ada8b227990a6，PR CI https://github.com/trade3403/browseai/actions/runs/45950859034 与 push CI https://github.com/trade3403/browseai/actions/runs/45950852815 均 success，10 项检查通过。新增任务列表/详情/创建/更名/安全草稿删除 API，创建事务写入 stages；事务内 RLS 和显式租户过滤，行锁+revision 状态转换 CAS；公开 pause/stop，其他 evidence 只允许可信内部 service 提供，禁止 HTTP 伪造 CI 结果或自动恢复派发。仅未修改的 QUEUED 草稿可删除，已变更或停止任务保留历史。真实双租户 API 测试及两个同 revision 并发 stop 测试。未实现 HTTP resume/approve（需可信对账/确认适配器）、创建幂等键、stage 指令/依赖持久化、审计/outbox、租约续期/释放及 fencing 执行，待后续批次；默认 Compose 仍 mock。整体覆盖率及 CI 门禁不变，本次未在本地运行完整依赖/数据库测试，新 CI 待验收。

## V0.2 第九批：事务级全局分支租约
证据 SHA 3c9d98e7734a02ac2c7f86c65199043e263729a1；PR CI https://github.com/trade3403/browseai/actions/runs/45952447631 与 push CI https://github.com/trade3403/browseai/actions/runs/45952442058 均 success，10 项检查通过。新增内部 leaseStore：任务行锁及 revision 检查、repo stable ID+branch 全局抢占、DB 时钟 60 秒有效期、续期、过期禁止续期/派发、fencing token、仅非派发状态且可信对账后释放，过期不自动抢占；ready/prepare/confirm_sent/repair 持久化转换同事务检查有效 fence。真实 PostgreSQL 双租户竞争、不同分支并行、过期/旧 token 拒绝及释放后递增测试。verifiedBranch 只是未来可信 GitHub 适配器输入契约，V0.3 才完成真实授权，当前没有公开租约 API 或网页派发；外部 GitHub 写入 fencing、完整调度/恢复、幂等键/outbox、审计仍待后续实现。未本地执行完整数据库测试，待新 CI。V0.2 未宣称完成。

## V0.2 第十批：变更幂等与事务审计
证据 SHA 300bbee71407899c170774cddcdfa9c878b0a36e，PR CI https://github.com/trade3403/browseai/actions/runs/45954023205 与 push CI https://github.com/trade3403/browseai/actions/runs/45954016760 均 success，事务级租约批次已通过。新增 0004 migration：租户+用户范围幂等键、只追加 audit_logs 与 events、RLS 隔离；项目/任务全部现有变更接口要求 Idempotency-Key，作用域绑定认证用户，动作/对象/revision/规范化参数哈希，同键同内容重放已提交结果，同键异内容 409，数据库行、幂等结果、审计和事件同事务提交，失败回滚。内部任务状态转换亦记录审计与事件，不暴露 evidence。真实数据库并发重复创建、跨租户相同键、重放更新删除、回滚、审计不可删除测试；迁移回滚/重建加入既有必需 job。事件仅为持久记录基础，消费者、派发 outbox 重试/唯一 dispatch key、清理保留策略及账号生命周期审计尚未完成。项目/任务 API 的安全恢复/审批、完整阶段定义及账户开通仍待收尾；不进入 V0.3。当前提交未本地实际运行 PostgreSQL CI，待新 CI 验证。

## V0.2 第十一批：账号生命周期及永久会话撤销
证据 SHA b2c729ca0cfcfc54c33709bd1860dc04745e2bf2；PR CI https://github.com/trade3403/browseai/actions/runs/45954517830 与 push CI https://github.com/trade3403/browseai/actions/runs/45954510019 均 success，10 项检查通过。新增 0005 migration、独立 browseai_admin 运维角色及 account CLI（创建一用户一工作区、禁用、启用、密码重置），密码通过 stdin 而非命令行参数，生产 DSN 要求 verify-full，账号审计不记录密码。用户密码/启停及租户启停触发永久 session 撤销，真实 HTTP 登录的 Argon2id 验证结果在签发时再次锁定并校验密码哈希，拒绝密码重置期间的旧证明；签发锁定身份记录。测试真实 PostgreSQL 账号生命周期、重复创建回滚、角色隔离、旧密码与旧会话失效、审计。默认 Compose 仍 mock；管理员 UI/配额属 V0.7，浏览器控制租约即时撤销、GitHub 授权撤销与外部写入协调待相关阶段。此次未本地运行完整数据库测试，待 CI；后续补齐安全恢复/审批边界及阶段定义后综合验收。未进入 V0.3。

### 第十一批 CI 格式修复
证据 SHA 013c42c10551e98c9bf520553084a56d07fc7dd8；PR CI https://github.com/trade3403/browseai/actions/runs/45955088955 与 push CI https://github.com/trade3403/browseai/actions/runs/45955083980 均 failure。PR 实际 job 日志显示 database 成功，TestPostgresAccountLifecycle PASS，完整 race+PostgreSQL 测试覆盖率 80.6%；其他七个子 job 成功，backend 因 auth_password.go 的 crypto/sha256 与 crypto/subtle 导入排序违反 gofmt 失败，gate 因此失败。按 Runner 的 gofmt -d 结果修正唯一差异，未修改测试、权限或验收阈值。等待新 SHA 的双套 CI，尚不进入安全恢复/审批批次。

## V0.2 第十二批：本地安全恢复与审批意向
证据 SHA 9ff8796be1c81d32bb9fa73b72d720576c251429，PR CI https://github.com/trade3403/browseai/actions/runs/45955429197 与 push CI https://github.com/trade3403/browseai/actions/runs/45955425057 均 success，10 项通过。新增 0006 task_approvals RLS 只追加记录；POST resume 只恢复首阶段、未派发、无租约、无相关审计记录的 PAUSED/QUEUED 草稿，其他状态必须可信对账；POST approve 在 AWAIT_APPROVAL、当前 revision、有效 fence 下记录用户审批意向，不改变任务状态或发送消息；confirm_sent 内部转换额外要求同 revision/stage/fence/actor 审批且仍需可信人工发送证据。两接口沿用幂等键与原子审计。真实数据库测试跨租户、伪造 evidence、旧审批、恢复拒绝和人工确认边界。审批仅为任务级意向，不是精确指令授权，V0.5 必须绑定实际不可变 dispatch 内容并核验人工发送；V0.3/V0.6 的 Git/CI 对账及外部恢复未实现。本批未本地完整执行，待新 CI。下一批阶段配置及 V0.2 综合验收。

## V0.2 第十三批：阶段计划配置与持久化
证据 SHA 9f967e495efb33b4fc33fefb02d014cb9766bbb1，push CI https://github.com/trade3403/browseai/actions/runs/45957140199 success；PR CI https://github.com/trade3403/browseai/actions/runs/45957145910 第一次因微软 APT 源 HTTP403 安装失败，原 SHA 第 2 次重跑成功，10 项检查通过，未降低视觉门槛。新增 0007 stage_plan 迁移；创建任务必须提供每阶段 instruction/dependencies/acceptance 及原有 id/weight，校验文本大小、空值、唯一验收项、前置依赖与顺序，拒绝环、未知、自依赖及向后引用。阶段计划随任务创建同事务写入，不允许直接更改已创建计划；GET /api/tasks/{id}/stages 返回 tenant 隔离的有序计划与 configured 标记。ready/prepare/confirm_sent/repair 在行锁事务内验证完整计划和现有有效 fence；历史缺配置记录保留但禁止推进，旧草稿可删除重建。HTTP 创建请求上限128 KiB，其余仍8 KiB。真实 DB 测试持久化/跨租户/旧配置拒绝；单测非法依赖和验收。阶段验收项是用户配置，不是已验证 CI 证据，GitHub 工作流/检查映射及外部授权属于 V0.3；精确指令派发仍属 V0.5。未本地完整执行，待新 CI；下一步 V0.2 综合验收与遗留问题核对。

## V0.2 代码阶段综合验收（2026-11-27）
证据 SHA 2f80cf2af22483c78e61e4018cea3a6e66b8d85c；PR CI https://github.com/trade3403/browseai/actions/runs/45960638615 与 push CI https://github.com/trade3403/browseai/actions/runs/45960630219 均 completed/success，逐项检查 10+10 jobs。实际 database job 140377364013 七项 TestPostgres* 集成均 PASS，完整语句覆盖率 81.3%；schema 6 tests OK；functional Linux 10 passed、Windows 10 passed；visual-desktop 3 passed，shell 11.7364%、边界 1.453125 px、核心区域均 <=15%，releasePassed=false。详见 docs/V0.2-ACCEPTANCE.md（含遗留边界和技术选型差异）。按路线图 V0.2 代码范围通过；文档提交待自己的双套 CI，不替代证据 SHA。V0.3 尚未开发，下一步从验收通过的分支建立独立草稿 PR；不得未经批准合并/部署。

## V0.3 第一批：GitHub App 授权基础及租户绑定
V0.2 最终文档证据 SHA a1aea68e21deee039b6d1ab06dc77eb8fa2ecbe2，PR CI https://github.com/trade3403/browseai/actions/runs/45962133276 与 push CI https://github.com/trade3403/browseai/actions/runs/45962126324 均 completed/success。基于该 SHA 建立独立分支 feat/v0.3-github-integration，不合并 PR #1/#2。新增 0008 迁移：安装 ID 全局独占租户、仓库 stable ID 白名单、RLS 强制隔离、应用角色只读绑定，拒绝客户端自行绑定；项目必须匹配已授权 repo ID 和名称。GitHub App RS256 JWT 五分钟有效期，安装 token 请求固定 HTTPS API、禁止重定向、只读 actions/checks/contents/pull_requests/metadata、单仓库 scope，验证响应权限、repo ID 和期限；token 只在内存，不记录响应内容。mock transport 合约测试签名、scope、异常与重定向；真实 PG 测试跨租户、禁用、未授权和 runtime 自绑定拒绝。当前仅内部代码及运维预置映射，无生产私钥加载、OAuth/安装归属验证流程、外部 token 使用、缓存、Webhook/CI 轮询或自动派发；真实安装尚未授权。新 CI 待验收。下一批：可信安装验证、授权配置和受控 API 客户端。

## V0.3 第二批：GitHub 只读仓库与分支观察
证据 SHA af1cd53da3311cd83dce4aac428e1e5c6c476d86，PR CI https://github.com/trade3403/browseai/actions/runs/45963975601 与 push CI https://github.com/trade3403/browseai/actions/runs/45963967551 均 completed/success，PR 10 jobs 成功。新增固定 HTTPS 只读 API 客户端，禁止重定向/外部 URL，限制响应 2 MiB，错误不泄露凭据；每次观察获取单仓库短期 token，远程 repo stable ID/名称/禁用/归档校验、准确 branch 和 40 位 head SHA 校验；内部 project observer 从认证 principal 和项目/绑定查询获取目标，网络读取后再次检查本地授权。GET 429、明确限流 403、5xx 产生 Retry-After/限额 reset 的下一次可重试时间，不在请求内循环重试。mock 测试仓库/分支不符、过期 token、非法 ref、响应超限、HTTP 错误和限流。仅内部观察接口，尚未接入真实私钥加载、安装归属验证/绑定操作、轮询调度、分页和 Webhook；token 签发接口限流尚需完善。未修改状态机或启用自动派发。待当前 CI 验证。

## V0.3 第二批验收与窗口交接
证据 SHA 7aa1bb593231125d4000b881bc70f7698489dac3，PR CI https://github.com/trade3403/browseai/actions/runs/45965865818 和 push CI https://github.com/trade3403/browseai/actions/runs/45965857888 均 success，PR 10 项成功，实际 database job 覆盖率 80.9%，四项 GitHub 相关测试 PASS；visual developmentPassed=true，releasePassed=false。交接详见 docs/HANDOFF-V0.3.md。本文档提交待自身 CI，暂停新增功能，下一窗口从第三批继续。

## V0.3 third batch — initial security primitives
Previous HEAD 26c0915b186f3ce4b99d0cce824786497c8c5ed4 verified: push https://github.com/trade3403/browseai/actions/runs/45967086759 and PR https://github.com/trade3403/browseai/actions/runs/45967093399 both success. Added internal protected PEM loader and remote App/installation/account/repository verification with synthetic contract tests. New commit CI pending; third batch not yet accepted. Remaining: privileged provisioning, key loader tests, token response compatibility and retry, integration tests. No live App credentials.

Batch 3a CI for 765c15d5867ffca6abbc18d67d64017283b5c8ab: push https://github.com/trade3403/browseai/actions/runs/45971034894 and PR https://github.com/trade3403/browseai/actions/runs/45971041816 failed backend gofmt (github_setup_test.go field alignment); other eight jobs passed; gate failed. Applied exact gofmt log correction. New CI pending; no acceptance claimed.

Batch 3a formatting fix af47a00345f289cfa67f2b50c09d4a5851664270: push https://github.com/trade3403/browseai/actions/runs/45973113150 and PR https://github.com/trade3403/browseai/actions/runs/45973120119 both success (10/10 each). Batch 3b adds protected key tests, explicit Windows fail-closed behavior and token issuance retry classification. New CI pending. Next: privileged binding persistence and cross-tenant integration tests.

Batch 3b 239dcd1db2bff0c750d02daadb9bf8f77e6f9bc5 verified push https://github.com/trade3403/browseai/actions/runs/45975203963 and PR https://github.com/trade3403/browseai/actions/runs/45975215028 both 10/10 success. Batch 3c adds internal trusted provisioning transaction and PostgreSQL negative tests; CI pending. Next: separate least-privilege operator role and entry point, approval audit, response compatibility review. No live App credentials.

Batch 3c 1631dc2b184a564c9e934005d1f44275ca63fe80 verified push https://github.com/trade3403/browseai/actions/runs/45977278136 and PR https://github.com/trade3403/browseai/actions/runs/45977284732 both 10/10 success. Batch 3d adds 0009 dedicated operator function, atomic audit and isolated role integration tests; CI pending. Next: separate CLI, protected operator input and approval workflow. No live credentials.

Batch 3d a045a370d85989965244d86efc7ffc6b90e3a05b verified push https://github.com/trade3403/browseai/actions/runs/45979374753 and PR https://github.com/trade3403/browseai/actions/runs/45979385539 both 10/10 success. Batch 3e adds offline GitHub binding CLI, explicit exact approval, TLS/role checks and negative tests; CI pending. Remaining live token contract compatibility review and webhook development. No real credentials.

Batch 3e 3e6abcdde29b959b0dba0a38061fbd53a4b1d900 verified push https://github.com/trade3403/browseai/actions/runs/45981529982 and PR https://github.com/trade3403/browseai/actions/runs/45981537281 both 10/10 success. Batch 4a adds webhook verification primitives and synthetic negative tests. CI pending. Next persistent deduplication and authorized routing. Live App compatibility remains NEED_HUMAN without credentials.

Batch 4a e419b8709416b69c9cdb5b288de877a4ffe818d1 verified push https://github.com/trade3403/browseai/actions/runs/45983620662 and PR https://github.com/trade3403/browseai/actions/runs/45983628394 both 10/10 success. Batch 4b adds 0010 durable delivery ledger, audited tenant routing, replay deduplication and PostgreSQL tests. New CI pending. Next: protected webhook secret, isolated HTTP receiver and polling hint consumption. Live GitHub App still unconfigured.

Batch 4b 41f2ad9cd6d5fa950fca44360a77d15cda8bf6cc verified push https://github.com/trade3403/browseai/actions/runs/45985717390 and PR https://github.com/trade3403/browseai/actions/runs/45985724418 both 10/10 success. Batch 4c adds isolated loopback webhook receiver, protected secret loading, role checks, concurrency/time limits and HTTP-to-PostgreSQL tests. New CI pending. Next: receiver hardening tests and polling reconciliation. No live webhook configured.

Batch 4c 74728a7d4ac14c8edd5b25a4fb7809be30fbc861 failed push https://github.com/trade3403/browseai/actions/runs/45987808304 and PR https://github.com/trade3403/browseai/actions/runs/45987819231. Backend vet, database build and Compose logs confirm int vs int64 mismatch in readProtectedGitHubFile io.LimitReader. Corrected the conversion without changing the size limit or CI gates. New CI pending; no phase acceptance claimed.

Batch 4c fix 2e1ab33e4d836639e82fce9174cfe0c3d9100ba7 verified PR https://github.com/trade3403/browseai/actions/runs/45989642074 and push attempt 2 https://github.com/trade3403/browseai/actions/runs/45989634636 both 10/10 success. Initial push functional failed on external Microsoft apt 403; rerun succeeded without weakening gates. Batch 4d adds receiver hardening and concurrent ledger tests; CI pending. Next: V0.3 batch 5 CI read/polling. Live GitHub credentials and proxy deployment remain pending.

Batch 4d e2420bfec443a889327d1491c690be37cfdd7c9b verified push https://github.com/trade3403/browseai/actions/runs/45993297217 and PR https://github.com/trade3403/browseai/actions/runs/45993303509 both 10/10 success. Batch 5a adds bounded Actions workflow/run listings and synthetic pagination/identity tests. CI pending. Next: attempt-specific jobs and observation classification, followed by polling persistence. No live App integration claimed.

Batch 5a 0a3b6472e0e4998c6946caa2706f18dae017e8d8 verified push https://github.com/trade3403/browseai/actions/runs/45995088062 and PR https://github.com/trade3403/browseai/actions/runs/45995101308 both 10/10 success. Batch 5b adds attempt-specific jobs and conservative candidate classification. Previous submission failed with transport disconnect; retry now. New CI pending. Next polling/backoff and persistence.

Batch 5b e645826e615a2e9c473fc3896ca08d87dce5fbfb verified push https://github.com/trade3403/browseai/actions/runs/45998758264 and PR https://github.com/trade3403/browseai/actions/runs/45998764179 both 10/10 success. Batch 5c adds bounded cancellable polling worker, rate-limit deadlines, failure budget, conservative candidate selection and deterministic tests. CI pending. Next durable monitoring persistence and webhook reconciliation. No live App or autonomous runtime wiring yet.

Batch 5c bb51a440966cb4afbe0cfcb2d36c2d0888d3de84 verified push https://github.com/trade3403/browseai/actions/runs/46000394969 and PR https://github.com/trade3403/browseai/actions/runs/46000404527 both 10/10 success. Batch 5d-1 adds durable monitor migration 0011, lease recovery and fenced publication SQL tests. New CI pending. Next Go adapter, trusted policy and webhook reconciliation; no live worker enabled.

Batch 5d-1 3e1d4abcdb42dbe95b6a764dfb9201f8351430a7 verified push https://github.com/trade3403/browseai/actions/runs/46002507043 and PR https://github.com/trade3403/browseai/actions/runs/46002514113 both 10/10 success. Batch 5d-2 adds migration 0012 and durable Go single-step worker with concurrent PostgreSQL tests. CI pending. Next installation throttling, webhook compensation and trusted policy. No live process started.

Batch 5d-2 d33dee47ae45e3fe842b0449bb207f03ac9ac40e verified push https://github.com/trade3403/browseai/actions/runs/46004406298 and PR https://github.com/trade3403/browseai/actions/runs/46004413326 both 10/10 success. Batch 5d-3a adds migration 0013 installation coordination and webhook hints with SQL tests. CI pending. Next trusted policy, worker startup, multi-target concurrency tests and evidence verification.

Batch 5d-3a 502a5e856660072e61d56be12074bff395867a95 verified push attempt 2 https://github.com/trade3403/browseai/actions/runs/46006345177 and PR https://github.com/trade3403/browseai/actions/runs/46006354504 both 10/10 success; initial visual setup failed on external Microsoft apt 403. Batch 5d-3b adds opt-in monitor command, trusted policy and lifecycle tests. CI pending. Next multi-target concurrency and evidence gate. No live worker started.

Batch 5d-3b c9296f14a1d8800acbbcffc5b55559efe4e4b9ff verified push https://github.com/trade3403/browseai/actions/runs/46010160794 and PR https://github.com/trade3403/browseai/actions/runs/46010173238 both 10/10 success. Batch 5d-3c adds twelve-session multi-target PostgreSQL coordination, cooldown and revocation tests. CI pending. Next V0.3 batch 6 authoritative evidence verification. Live credentials and deployment remain pending.

Batch 5d-3c e1e2bb9af584468afcce50843ca124eef87efd4a verified push https://github.com/trade3403/browseai/actions/runs/46012272250 and PR https://github.com/trade3403/browseai/actions/runs/46012282719 both 10/10 success. Batch 6a adds strict push evidence snapshot and mismatch tests. New CI pending. Next PR evidence and remote observation tests; no task transition or live deployment.

Batch 6a initial push 46014573683 and PR 46014574910 failed backend gofmt (github_evidence_test.go alignment) and dependent gate. Apply exact gofmt correction without changing test semantics or CI rules; full backend tests still pending.

Batch 6a formatting fix 4e11d0be40e9c36d18128f1d6417b5f0fb0a308b verified push https://github.com/trade3403/browseai/actions/runs/46015960074 and PR https://github.com/trade3403/browseai/actions/runs/46015959128 both 10/10 success. Batch 6b-1 adds PR relationship and ordered merge-parent verification with negative tests. New CI pending. Next PR Actions linkage and workflow source trust; no task transition.

Batch 6b-1 803e2c7894d2020aacd012fae5216d1cf6905d9b verified push https://github.com/trade3403/browseai/actions/runs/46016843179 and PR https://github.com/trade3403/browseai/actions/runs/46016850805 both 10/10 success. Batch 6b-2a adds PR Actions association candidate and mismatch tests; actual repository metadata confirms head_sha is PR head, not merge commit. New CI pending. Next trusted workflow source and merge execution proof. No task transition.

Batch 6b-2a 31dfd8107f7e83b31cec691fd4a8360437a966af push 46019164151 and PR 46019172830 failed database coverage gate at 72.8% (<75%); Go tests passed. Added synthetic HTTP end-to-end tests for push and PR evidence snapshots, re-read changes, missing PR, ambiguous runs, foreign links and read failures. No threshold change. New CI pending.

Batch 6b-2a remote-test fix bb40ae569abbf92b8573287be480bcc549a009f8 verified push https://github.com/trade3403/browseai/actions/runs/46020858069 and PR https://github.com/trade3403/browseai/actions/runs/46020867566 both 10/10 success. Batch 6b-2b adds independently pinned workflow SHA-256 verification at immutable revisions and synthetic remote tests. New CI pending. Next source policy wiring and explicit execution proof; no task transition.

Batch 6b-2b initial push 46021436379 and PR 46021445784 failed backend gofmt (github_workflow_source_test.go multiline function literal) and dependent gate; other eight jobs each passed, including database. Applied exact gofmt correction without changing test semantics or CI rules. New CI pending.

Batch 6b-2b formatting fix 675385a03a8585d4d3c657a53d30c2869ceff6db verified push https://github.com/trade3403/browseai/actions/runs/46022356142 and PR https://github.com/trade3403/browseai/actions/runs/46022362232 both 10/10 success. Batch 6b-2c adds composite verification and independent signed execution statement contract, synthetic tests. New CI pending. External trusted verifier/signing infrastructure not implemented; no autonomous advancement. Next durable evidence policy/consumption.


### V0.3 6b-2c CI 已验收；6c-1a 开发提交待验收
实际 HEAD e41b3b83b42fd13312dce6cabff385059c0b5617，Push 46027678234、PR 46027689998 均 completed/success，20/20 jobs。新增 0014 可信公钥注册/轮换/吊销、独立最小权限角色、授权作用域及 SQL 测试，当前提交 CI 待查询。Go 调用集成及真实签名服务尚未实现，不允许推进任务。


### V0.3 6c-1a 已通过；6c-1b 待 CI
实际提交 dda23ee291d5aebe2b7f90b51510a658ff39925d，Push 46028420091、PR 46028429227 均 completed/success，20/20。新增注册公钥查询与复合验签入口，真实 PostgreSQL 独立角色及 mock GitHub 测试；当前提交尚未验收。独立签名服务、证据持久化及原子消费未完成。


### V0.3 6c-1b 已验收；6c-2a 待 CI
实际提交 ebf354c897a1d97a89e96c7786d146a2a05aba90，Push 46032121952、PR 46032129357 均 completed/success，20/20。新增 0015 封闭证据表、身份/有效期/去重约束与插入保护、迁移重建测试。普通运行角色全部无权访问。待实现任务绑定签名、可信 Go 写入、原子消费；没有生产验证器或任务推进。新提交 CI 待验收。


### V0.3 6c-2a 已验收；6c-2b-1 待 CI
实际提交 39b2375b31b4103b32014dc7fad481540620e681，Push 46035200936、PR 46035208978 均 completed/success，20/20。新增 v2 任务与签名者绑定证据、Push/PR 复合校验、摘要及时间边界、真实 PostgreSQL/mock GitHub 测试。证据表保持封闭；可信写入角色、原子消费、独立真实执行验证器仍待开发。新提交 CI 待验收。


### V0.3 6c-2b-1 已验收；6c-2b-2 待 CI
实际提交 175f98bd9e61d5e0cd5004a777b0530075716130，Push 46038151238、PR 46038158344 均 completed/success，20/20。新增 0016 独立写入角色、Go 复合验签写入与真实数据库测试。SQL 不独立验 Ed25519，专用写入凭据属于可信边界，不开放公共接口；消费、生产签名与策略接入待开发。新 CI 待验收。


### V0.3 6c-2b-2 已验收；6c-2c 待 CI
实际提交 0d09d52173638ce533ac38547625eb5025877ff2，Push 46041191693、PR 46041203393 均 completed/success，20/20。新增 0017 封闭证据原子消费及并发、回滚、权限/有效期测试。无运行时消费授权，无任务状态转换。可信执行验证器与第七批状态机事务接入待完成。新 CI 待验收。


### V0.3 6c-2c 已验收；6c-3a 待 CI
实际提交 1af081207099d694c396cbd9b7e244429553e900，Push 46044080958、PR 46044087576 均 completed/success，20/20。新增双重信任执行回执与独立签名决策原语及测试。真实隔离执行环境、监督器、策略分发、密钥部署和回执传输仍未完成；不允许任务自动推进。新 CI 待验收。


### V0.3 6c-3a 已验收；6c-3b-1 待 CI
实际提交 d86e9df01b9f40e99003ab402fcce38f4211e21e，Push 46046700691、PR 46046709390 均 completed/success，20/20。新增可信监督器编排、执行前后身份核对及模拟适配器测试。真实隔离执行适配器、独立服务与传输未实现，禁止自动推进。新 CI 待验收。


### V0.3 6c-3b-1 格式修复
提交 5bffa96d0e2ec4afa9f948789ca417ae34295054 Push 46049490013、PR 46049497112 各 8/10；backend 在 gofmt 检查发现 github_execution_supervisor_test.go 两处字段对齐错误，gate 随之失败。按两套实际日志修正格式，不修改测试语义或验收门槛。新 CI 待验收。


### V0.3 6c-3b-1 已验收；6c-3b-2a 待 CI
实际提交 0d057f1fdf2b51a95710c37687b94a9cd12c28f4，Push 46052133637、PR 46052140191 均 completed/success，20/20。新增远程执行客户端 TLS 1.3、证书固定、随机请求及节点签名报告核验。真实节点服务、隔离运行及生产凭据仍未完成，禁止自动推进。新 CI 待验收。


### V0.3 6c-3b-2a 格式修复
提交 d26a9d75f5feda0dc6305ca5feaa740aaa755600 Push 46054798856、PR 46054811307 各 8/10；backend 在 gofmt 检查发现 github_remote_executor.go 三处 HTTP Transport 字段对齐错误，gate 随之失败。按实际两套日志修正格式，不修改功能、测试或验收门槛。新 CI 待验收。


### V0.3 6c-3b-2a 已验收；6c-3b-2b 待 CI
实际提交 75a6df426ea728852cf9e2fa4563d80133fb8b47，Push 46057353386、PR 46057361671 均 completed/success，20/20。新增独立节点单次授权 HTTP handler、TLS 客户端验证、并发防重与签名报告测试。授权仅进程内；持久授权、独立服务启动及实际沙箱待完成，禁止生产自动推进。新 CI 待验收。


### V0.3 6c-3b-2b 已验收；6c-3b-2c-1 待 CI
实际提交 98ede011c9b96dd84337e048cda0333402bce2e0，Push 46059974959、PR 46059981244 均 completed/success，20/20。新增 Linux 持久化单次准入日志，真实文件系统并发/重建测试。实际沙箱、独立进程启动及部署尚未完成。新 CI 待验收。


### V0.3 6c-3b-2c-1 已验收；6c-3b-2c-2a 待 CI
实际提交 f5f1ac57eda572641c064459814061073bafd169，Push 46062483942、PR 46062489537 均 completed/success，20/20。新增离线固定镜像沙箱启动适配器与模拟命令测试。实际可信镜像构建、证明、专用隔离环境和独立服务尚未完成，禁止自动推进。新 CI 待验收。


### V0.3 6c-3b-2c-2a 已验收；6c-3b-2c-2b-1 待 CI
实际提交 d628da2f00dd72257a28cb3c3746447d925629b9，Push 46064998412、PR 46065004804 均 completed/success，20/20。新增强制可信构建签名及启动前复核、镜像健康检查禁用、篡改及过期测试。实际可信构建流水线、镜像检查及独立部署未完成，禁止自动推进。新 CI 待验收。


### V0.3 6c-3b-2c-2b-1 已验收；6c-3b-2c-2b-2a 待 CI
实际提交 3ec898284963f5ac766ed65b633a32c87b46d86a，Push 46067395279、PR 46067402580 均 completed/success，20/20。新增 Docker 本地运行时及镜像配置预检、严格资源能力与配置白名单、拒绝危险镜像测试。真实构建流水线及隔离实机测试仍未完成。新 CI 待验收。


### V0.3 6c-3b-2c-2b-2a 人工复核通过；6c-3b-2c-2b-2b-1 待 CI
实际提交 73c6c26ff10ec9193c9dc124cbad80963f403d7a，Push 46069854806、PR 46069863091 总结论均成功，20 项 conclusion success；PR schema job 140743949400 status 异常 in_progress，但 completed_at 2026-11-27T21:22:33Z，全部五步 completed/success、6 个测试 OK，最终 gate 成功。依据实际日志及完成时间人工复核，不改变程序自动证据严格校验。新增固定离线构建上下文及材料摘要验证；真实构建、签名和隔离环境仍待完成。新 CI 待验收。


### V0.3 离线构建材料已验收；镜像构建及签发待 CI
实际提交 f158f6440edce76d9763ed20a2f300e1f5ffc42a，Push 46074590629、PR 46074596980 均 completed/success，20/20。新增固定构建命令、基础镜像预检、实际产物 ID 验证及构建签名编排；CI mock，不代表真实 Docker 构建、隔离或部署验收。新 CI 待验收。


### V0.3 镜像构建及签发已验收；镜像归档交接待 CI
实际提交 163d4a4261b1ae8a1b0eb6842b6ed7ee8259a85a，Push 46076848408、PR 46076856478 均 completed/success，20/20。新增镜像归档签名、安全暂存、导入前完整性及导入后配置复核。实际独立主机部署和传输仍待验证。新 CI 待验收。


### V0.3 镜像归档交接已验收；独立执行节点预览启动待 CI
实际提交 58a3e3799f6015bd49ae18afb113974a4359feac，Push 46079030331、PR 46079034720 均 completed/success，20/20。新增独立节点本地预览启动命令、受保护配置与证书、镜像导入和 TLS 生命周期。实际专用虚拟机部署与隔离验收仍待完成。新 CI 待验收。


### V0.3 独立节点预览启动覆盖率修复待 CI
实际提交 ce60dcd3faf8c3b13c56168bc4eb77532ccb438d，Push 46081201330 与 PR 46081206490 均 completed/failure，各 8/10。两套 database 日志均显示 Go 测试成功但总语句覆盖率 74.9%，未达 75% 硬门槛；gate 随之失败。新增独立节点启动准备的签名构建、归档完整性、受保护目录、TLS 配置及拒绝路径测试，使用临时文件和模拟导入/运行后端；不修改生产代码或验收阈值。新的 CI 尚待验证，真实独立主机联调、独立签名服务和自动推进仍未验收。


### V0.3 节点启动准备已验收；回环端到端联调待 CI
实际提交 c5fd3c4a70f08b8383810cc5727ae89ddd1dbad6，Push 46082261519、PR 46082269564 均 completed/success，20/20，Push database 总覆盖率 76.1%。本批复用启动夹具，新增真实回环 HTTPS/TLS 1.3 双向认证、服务端固定证书、CA 合法但未授权客户端拒绝、任务版本拒绝、实际签名回执验签及重建后持久防重放测试。CI 使用临时证书、模拟镜像导入和执行，不代表真实 Docker/独立主机验收。新 CI 待验证；禁止自动推进。下一步独立签名服务及回执传输。


### V0.3 回环联调已验收；独立签名 HTTP 边界待 CI
实际提交 b619217292918b69b61a89f9c9020a617da7201d，Push 46085279970、PR 46085284537 均 completed/success，20/20。新增单策略签名 handler、TLS 1.3 已验证客户端固定身份、严格 canonical JSON、有限请求体、监督器回执预验签及可信观察回调，返回独立签名 proof。测试包含拒绝路径和控制器验签；仅模拟观察与 TLS handler 状态，未启动独立签名进程或真实传输。下一步独立签名客户端、真实 TLS 联调及可信 GitHub 观察接线。新 CI 待验收，禁止自动推进。


### V0.3 签名 HTTP 边界已验收；签名客户端与回执传输待 CI
实际提交 4ab3381559109da81198748d4690446362ffb218，Push 46087787543、PR 46087792675 均 completed/success，20/20。新增独立签名客户端，TLS 1.3 双向认证、服务端 SPKI 固定、禁重定向及自动重试、限制请求/响应、回执摘要与固定任务证明验签；真实回环 TLS 测试包含错误身份、签名、响应及过期拒绝。仍用模拟 GitHub 观察；未部署独立签名进程。新 CI 待验收。下一步可信 GitHub 观察接线及独立签名进程配置启动；不允许自动推进。


### V0.3 签名客户端已验收；可信 GitHub 观察接线待 CI
实际提交 e879db332367df35b6afc648d39b9283caab0244，Push 46090273167、PR 46090277895 均 completed/success，20/20。新增固定策略可信观察器，调用已有只读 GitHub App Push/PR CI 及不可变工作流源码验证，统一 30 秒新鲜度边界、取消、策略隔离和失败拒绝；测试使用模拟 GitHub HTTP transport。尚未实际部署签名进程或配置真实 App 凭据。新 CI 待验收，下一步受保护签名进程启动及独立服务联调，禁止自动推进。
