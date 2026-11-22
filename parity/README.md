# 一致性验证资料

本目录保存 Go 移植版与固定上游 Ponder 0.18.2 的核对资料。`test-ledger.json`、`mappings.json`、`feature-ledger.json`、各项源码用例清单，以及 `source-tests/`、`source-support/` 都继续由 Git 跟踪。归档操作不会改变测试源码、原始状态、断言、映射或通过数量。

## 历史运行证据

`runs/` 只在当前工作树保留已选定的结果和发布/恢复记录。大日志、RPC tape、失败现场、冻结输入和重复源码快照由以下索引定位：

- `archive-manifest.json`：原提交和 tree、保留/归档数量、保留文件及其校验值。
- `archive-files.json.gz`：每个归档文件的原路径、Git blob、SHA-256、大小与可执行位；gzip 头固定，结果可重复生成。

原始证据的 Git 对象保留在 **未重写的祖先历史** 中。正常完整 clone 已包含它们，不依赖其他账号、数据库或外部归档服务。移出当前 tree 不等于从 Git 历史删除：本次减少 checkout 占用和后续产物增长，不承诺缩小现有 Git pack 或完整 clone 的下载量。

```sh
# 校验完整索引、原始 tree 和所有归档 Git blob 的 SHA-256；不恢复文件。
python3 tools/parity_archive.py verify

# 仅恢复一个文件或一个历史目录；可以重复指定 --prefix。
python3 tools/parity_archive.py restore --prefix parity/runs/batch-41-evidence

# 历史审计或完整验收前，恢复全部历史证据。
python3 tools/parity_archive.py restore

# 也可以恢复到单独目录，避免让历史 .go/.ts 快照参与本次测试扫描。
python3 tools/parity_archive.py restore --destination /tmp/ponder-parity-evidence
```

恢复器按原字节和原路径恢复普通文件，保留可执行位；校验失败、路径越界、符号链接或目标文件已有不同内容时拒绝覆盖。只读校验及恢复不访问网络，不执行归档中的代码。历史结果中的绝对路径、工具环境和二进制可能仍需重新准备；恢复历史文件不等于重新运行历史测试。

浅 clone 不一定包含归档源提交。可以执行 `git fetch --unshallow origin` 获取完整历史，再恢复；不要把缺失证据改成测试跳过。若以后需要过滤 Git 历史，必须先把本归档源提交及其所有对象独立保存到可取回的 Git bundle/归档，验证新 clone 能逐字节恢复，再调整存储位置。当前不需要历史重写或强推。

本次整理另将全部现有 refs 和完整历史备份为本地忽略文件 `.local/parity-archive/before-cleanup.bundle`；大小、SHA-256、`git bundle verify` 结果记在 `runs/archive-cleanup-20261122/result.json`。该备份不上传，也不代替祖先历史这一可持久取回的来源。需要用独立备份恢复对象时：

```sh
git bundle verify .local/parity-archive/before-cleanup.bundle
git fetch .local/parity-archive/before-cleanup.bundle refs/heads/main
python3 tools/parity_archive.py verify
```

## 新运行产物

`runs/` 中的新文件默认忽略。测试和验证工具仍将完整日志写到原有路径；验收后逐个提交经过核对的小型摘要，例如：

```sh
git add -f parity/runs/<run-name>/result.json
```

不要强制添加整个 run 目录。需要长期保留新原始证据时，先制作独立、可取回且已验证的归档，并在摘要中记录位置、SHA-256 和恢复方式，再移除本地原件。现有归档索引是固定源提交的完整快照，不自动包含未来运行。

需要再次精简已恢复的旧文件时，可运行 `python3 tools/parity_archive.py prune`。它先验证所有归档对象和全部待删除文件，遇到任何已改变的证据便拒绝删除；未列入归档的文件始终保留。不要用清空整个目录的方式代替它。
