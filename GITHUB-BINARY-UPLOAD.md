# GitHub 原始图片与二进制文件无损上传：通用 Prompt 与实测方案

本指南适用于任何获得写入授权的 GitHub 仓库，不限 BrowseAI。适用 PNG、JPEG、WebP、ZIP 等需保持字节不变的文件。2026-11-27 在 BrowseAI 上实测成功。Base64 仅用于传输，最终仓库必须保存真实二进制。不要上传秘密、个人隐私或未经许可的素材。

## 1. 直接复制给其他 AI 开发窗口的 Prompt

请完整阅读本指南并按照以下方法，将我提供的原始附件无损提交至我指定的 GitHub 仓库、分支和路径。

- 目标仓库：以用户本次指定为准。
- 目标分支：以用户本次指定为准。
- 保存路径：以用户本次指定为准。
- 保留原始文件全部字节，不重新生成、压缩、缩放、截图、转码或替换图片，不以外链、占位文件、Base64 文本文件冒充原图。
- 检查真实文件格式、字节数、SHA-256 和 Git Blob SHA；图片记录分辨率。禁止泄露敏感文件；未经授权不覆盖不同内容、不 force push、不合并。
- 首选原始字节 Base64 + GitHub create_blob + create_tree + create_commit + update_ref；最终 fetch_file(base64) 回读验证。
- 如果附件环境与 GitHub 工具环境无法直接共享字节，采用本指南的 Git 二进制补丁中转法，不要仅因文本接口不支持就要求用户自行上传。若存在直接授权 Git 客户端或其他可用二进制接口，也可以使用。
- 只有分支真实提交、回读内容校验一致才算完成。报告仓库、分支、路径、字节数、格式/分辨率、SHA-256、Git Blob SHA、Commit SHA 和文件链接。若工具权限或文件大小限制确实无法解决，明确报告阻塞。
- 若用于 UI 还原，必须使用原始设计图作为视觉基准，不得擅自降低验收标准。上传通过不等于 UI 像素验收通过。

## 2. 首选方法：直接上传二进制

1. 读取原始附件的真实字节，计算 SHA-256 和 Git Blob SHA。
2. Base64 编码（不含 data URI 前缀）。
3. 调用 GitHub create_blob，encoding=base64，content=Base64 字符串；校验返回 SHA 等于原始 Git Blob SHA。
4. 获取目标分支最新 HEAD 及其 Tree SHA。检查目标路径已有文件，未经授权不得覆盖不同内容。
5. create_tree：以现有 Tree 为 base_tree_sha，条目为 path=目标路径、mode=100644、type=blob、sha=已验证的 Blob SHA。
6. create_commit：以新 Tree 为 tree_sha，旧 HEAD 为 parent_sha。
7. update_ref：更新指定分支，force=false；支持时传 expected_sha=旧 HEAD。并发冲突重新读取并处理，不强推。
8. fetch_file：指定新 Commit 或目标分支，encoding=base64。解码回读并核对原始大小、SHA-256、Blob SHA，同时确认分支已经指向新提交。

常用连接器 create_file/update_file 是 UTF-8 文本接口，不能直接替代二进制上传。仅创建 Blob 不会使文件出现在分支，必须完成 Tree、Commit、Ref。

### Python 校验示例

```python
import hashlib, base64
from pathlib import Path
data = Path("original.webp").read_bytes()
print("bytes:", len(data))
print("sha256:", hashlib.sha256(data).hexdigest())
print("git_blob_sha1:", hashlib.sha1(
    f"blob {len(data)}\0".encode("ascii") + data
).hexdigest())
payload = base64.b64encode(data).decode("ascii")
# 将 payload 交给支持 base64 的 GitHub create_blob，不是写入 .webp 文本。
```

Git Blob SHA 与文件 SHA-256 不同：传统 SHA-1 Git 仓库计算的是 blob 头部加 NUL 加原始字节。也可运行 git hash-object 校验。

## 3. 备用方案：Git 二进制补丁跨环境中转（已实测成功）

### 3.1 生成完整补丁

在独立临时目录初始化 Git 仓库，把原始附件逐字节复制到目标相对路径后执行：

```bash
git init
mkdir -p design/screens
# 将原始图片复制为 design/screens/original.webp
git add -- design/screens/original.webp
git diff --cached --binary --full-index -- design/screens/original.webp > original.patch
git hash-object design/screens/original.webp
```

新增二进制文件会生成 GIT binary patch 和正向 literal N 段，不依赖旧文件。若 Git 误判为文本，可在临时仓库通过 .gitattributes 将文件标记为 binary。Windows 旧 PowerShell 重定向可能产生 UTF-16，建议用 Git Bash 或 Python subprocess 将原始命令 stdout 字节直接写入文件。不得用有损处理后的附件代替原图。

先在另一个空仓库验证 git apply --check original.patch 和 git apply original.patch，核验恢复文件大小及两个哈希。若跨环境只能传文本，就将补丁作为可读取文本提供给能调用 GitHub 的环境。

### 3.2 分段读取与解码

- 用文件读取工具按行分段读取完整补丁，保证行号连续、无漏行、重复或截断。不要把工具输出的预览当成完整文件。
- 定位指定文件的 GIT binary patch 后第一个正向 literal N 段；后续空行停止，不读取反向 literal 0。
- Git Base85 字符表：0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz!#$%&()*+-;<=>?@^_`{|}~
- 每行首字符 A–Z 对应 1–26 个有效字节，a–z 对应 27–52；后续每五字符为一组，按 85 进制解为 4 个大端字节，再依首字符长度去掉填充。严格验证字符及行长度。
- 拼接 zlib 流并 DEFLATE 解压；有库时使用标准解压库，无库时可使用纯 JavaScript 实现 RFC 1950/1951，支持 stored、fixed、dynamic Huffman block，验证 zlib 头、Adler-32、回引用和输出上限。不要误用 ASCII85。
- 校验恢复字节数与 literal N 一致，校验原始 SHA-256/Git Blob SHA，再 Base64 编码，按第 2 节完整提交和回读。哈希不符必须停止。

这正是 BrowseAI 成功采用的方案：本地生成补丁，通过文件工具读取文本，在调用 GitHub 的 JavaScript 环境中解码 Git Base85、解压 DEFLATE、转换 Base64，再调用 GitHub Git Blob API。无需把本地文件路径直接传给二进制接口。

## 4. 实际成功案例

- 日期：2026-11-27
- 仓库：trade3403/browseai
- 分支：feat/v0.1-foundation
- 原始附件名：1000005835.png，实际格式 WebP，1536×864
- 目标路径：design/screens/browseai-desktop.webp
- 文件字节数：103632
- SHA-256：cbbcf34029c6fff16e8ac2449da923a80e83eff8cb980bb531dcd32de3a59c05
- Git Blob SHA：cf3eca7dd025ca45b430980bdf203e3cdd8a8812
- Commit SHA：67c88a53b75c5460ed0ad5f0998c9800087bbd9e
- GitHub create_blob 返回 SHA 与原始 Git SHA 完全相同。新提交 fetch_file(base64) 回读 Blob SHA 一致、Base64 长度 138176 字符（去除空白）；今后验收还应对回读字节直接复算 SHA-256。

[查看成功上传的原图](https://github.com/trade3403/browseai/blob/67c88a53b75c5460ed0ad5f0998c9800087bbd9e/design/screens/browseai-desktop.webp)

## 5. 常见失败

| 失败 | 处理 |
|---|---|
| 文本接口不支持二进制 | create_blob(base64) |
| 文件路径不能传递给连接器 | Git binary patch 中转 |
| 补丁太长或截断 | 分段读取，校验连续性及哈希 |
| SHA 不同 | 停止并排查原始字节与解码 |
| Blob 已创建但分支不可见 | 完成 Tree、Commit、Ref |
| 分支已更新 | 重新读取最新 HEAD，不强制覆盖 |
| 缺权限或超出大小上限 | 使用有授权的适用接口或 Git 客户端，必要时经批准使用大文件机制；如不能解决，报告真实限制 |

本指南仅在有原始文件读取能力、GitHub 写权限和足够传输容量时可执行。禁止声称未完成的上传已成功。
