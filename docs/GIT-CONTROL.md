# Git 状态控制协议 v1

## 1. 数据所有权
代码分支用于工作产物；独立 ai-control/<task-id> 状态分支保存 .ai-control/config.json、tasks.json、progress.json、result.json、handoff.md。状态分支不得触发代码 CI。用户授权执行者在正常开发过程中提交必要状态；调度器读取并验证，不读取 ChatGPT 内容。公开仓库仅存非敏感数据。DB 保存待发送指令、确认记录、会话映射、租约及 CI 观察，不把敏感会话地址放到 Git。

## 2. 配置示例
```json
{
  "protocol_version": "1.0",
  "task_id": "DEMO-001",
  "repository": "owner/demo",
  "code_branch": "feat/demo",
  "control_branch": "ai-control/DEMO-001",
  "required_workflows": ["CI"],
  "required_jobs": ["unit", "integration"],
  "stages": [{"id":"S1","weight":1,"acceptance":["unit","integration"]}],
  "max_repairs": 5,
  "poll_seconds": 30
}
```
config 由用户审批，执行者不能擅改验收标准。tasks 定义 stage ID、依赖、用户指令、验收清单。progress 为执行者声明；result 保存阶段产物链接（提交/PR/测试证据），handoff 保存已完成、未完成、阻塞、最新已验证代码 SHA 和下一步，不含聊天记录、token、私有凭证。

```json
{
  "protocol_version": "1.0",
  "task_id": "DEMO-001",
  "revision": 12,
  "stage_id": "S1",
  "status": "WAIT_CI",
  "code_sha": "0123456789abcdef0123456789abcdef01234567",
  "dispatch_id": "DEMO-001-S1-003",
  "completed_stage_ids": [],
  "handoff_ready": false,
  "updated_at": "2026-11-27T00:00:00Z"
}
```
上面为模拟数据。revision 单调递增，code_sha 必须是仓库可达提交，任务、分支、阶段、dispatch_id 必须匹配；状态分支自身 SHA 不作为代码 CI SHA。JSON Schema v2020-12 在 V0.2 落地，拒绝未知字段/非法枚举、超限文件、非法分支和 URL；控制分支设置权限。自由文本只供用户阅读，不当作系统命令执行。

## 3. 状态机
| 状态 | 触发及动作 |
|---|---|
| QUEUED | 依赖通过、资源可用、取得租约 -> READY |
| READY | 准备固定指令 -> AWAIT_APPROVAL |
| AWAIT_APPROVAL | 本人确认及网页手动发送，确认记录入库 -> DEVELOPING |
| DEVELOPING | 观察新的有效进度提交；未知/超时 -> NEED_HUMAN，禁止盲目重复发送 |
| WAIT_CI | 查询匹配代码 SHA 的 CI；运行中不发指令 |
| CI_FAILED | 生成修复指令，修复计数加一，等待确认；超过上限 NEED_HUMAN |
| STAGE_PASSED | 所有必需检查通过且验收证据齐全 -> 下一阶段或最终审核 |
| HANDOFF_REQUIRED | 保守轮次预算或人工触发，准备保存检查点指令 |
| HANDOFF_READY | 验证交接 revision、代码 SHA、文件及状态后建立新聊天；确认恢复后释放旧窗口 |
| PAUSED/OFFLINE | 停止派发，保存前状态；恢复先对账 |
| NEED_HUMAN | 登录/额度/权限/异常/证据冲突/不确定发送结果 |
| COMPLETED | 所有阶段通过、用户最终验收；终态 |
| STOPPED | 用户停止；不自动重启 |

控制状态由调度器计算，不能直接信任执行者填写的 COMPLETED。取消、跳过、超时、缺失检查均不是通过。明确取消/重跑 CI 需用户授权。

## 4. GitHub 与一致性
- Webhook push、workflow_run、check_run；验签原始请求体，限制大小，delivery ID 唯一；异步入队并及时响应。轮询补丢失事件、分页、限流、过期检查。
- 绑定 repository、branch/PR、head SHA、工作流 ID/名称、job/check 名称和最新有效 attempt。若 PR 使用合并提交，记录并验证其对应代码 head；有新提交即旧证据失效。缺失/歧义不得推进。
- 一个 repo+code_branch 仅一个写任务租约；租约续期、fencing token 防过期持有者；其他分支可并行。数据库唯一键 task_id+stage_id+revision+action，事务 outbox。Webhook 可乱序到达，读取 Git 当前事实后再转移。
- 确认前和发送后分别记账；遇到发送结果不明 -> NEED_HUMAN，不能宣称网页侧 exactly-once。服务重启先检查 Git 最新 revision、CI 及人工确认，再恢复。
- 状态分支更新采用 compare-and-swap 或非强制 fast-forward，冲突重读；无意义心跳仅写 DB，防止 CI 循环。禁止擅自 force push、删除分支、自动合并或生产部署。

## 5. 新聊天固定恢复指令模板
请在已有授权下读取指定仓库、代码分支及控制分支的 config.json、tasks.json、progress.json、result.json、handoff.md。检查最新代码提交、对应 CI 和待办。仅继续未完成任务，按用户验收条件提交代码及必要项目进度。若证据不一致，停止并报告给用户。不要读取或复制旧聊天正文。每一阶段保存检查点。全部完成后提交验收证据，等待用户确认。

## 6. 验收用例
同一事件重复 100 次只产生一条待确认指令；旧 SHA 成功不推进；缺少必需 job 不推进；两个调度实例不能同时获得同一分支租约；断网恢复后无重复派发；交接文件损坏不切换；最终状态持久且停止；所有测试使用模拟站点与测试仓库。

## 7. 多租户扩展（V1.0 必需）
所有 DB 状态、缓存、事件、指令与 GitHub 授权均以已认证 tenant_id 隔离；任务 ID 全局唯一。Webhook 使用安装 ID 和授权仓库映射，不信任请求提供的 tenant_id。仓库稳定 ID + 分支写租约为全局唯一，跨租户冲突不泄露其他用户信息。用户禁用时撤销派发及远程控制；详见 MULTI-USER.md。

## 8. V0.2 首批可执行协议
`schemas/{config,tasks,progress,result}.schema.json` 使用 Draft 2020-12，`schemas/examples/` 为非敏感模拟样本。tasks 的 stages 每项包含 id、dependencies、instruction、acceptance；result 包含 revision、stage_id、code_sha、dispatch_id、evidence（kind/url）及 UTC updated_at。离线校验 `tests/schema/validate.py` 限制单文件128 KiB、拒绝重复键、未知字段、非法 URL/分支、跨文档不一致及循环依赖。运行 `python -m pip install -r tests/schema/requirements.txt` 后 `python -m unittest discover -s tests/schema -v`。此为结构和本地一致性验证，不能证明 Git 可达性、CI 成功或人工授权；handoff.md 及生产服务端验证仍待实现。自由文本不执行。
