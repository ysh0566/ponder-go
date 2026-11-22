# 一致性验证资料

`test-ledger.json`、`mappings.json`、`feature-ledger.json`、源码用例清单、`source-tests/` 和 `source-support/` 继续由 Git 跟踪。归档和历史过滤不改变原始测试、状态、断言、映射或通过数。

## 普通克隆与历史归档

历史大日志、RPC tape、失败现场、冻结输入和重复快照已从所有相关 Git 历史移出。普通 clone 只携带代码、测试、台账和已选摘要。`runs/` 中原始成功、失败和阻塞状态保持原样。

`archive-manifest.json` 保存原始提交/tree、文件校验值和外部 bundle 的身份；`archive-files.json.gz` 保存每个归档文件的原路径、Git blob、SHA-256、大小和可执行位。

完整原历史需独立保管和传递，不随普通 clone 获取。当前本地文件是忽略的 `.local/history-rewrite-20261122/original.bundle`，SHA-256 为 `69754ef0d06241ba2d1c739d821c9fb8e0e72ba1de65f6ba7f9f091d5580ec37`。此外保留首次整理前的 `.local/parity-archive/before-cleanup.bundle`、重写前的 Git 元数据及旧/新 SHA 映射；详见 [历史重写记录](../docs/HISTORY_REWRITE.md)。

## 按需恢复

取得上述独立 bundle 后，从仓库根目录执行：

```sh
python3 tools/parity_archive.py verify --bundle /path/to/original.bundle

# --prefix 可指定一个文件或目录，也可重复指定。
python3 tools/parity_archive.py restore --bundle /path/to/original.bundle \
  --prefix parity/runs/batch-41-evidence

# 完整历史审计/验收前恢复被摘要引用的旧证据。
python3 tools/parity_archive.py restore --bundle /path/to/original.bundle

# 单独目录可避免旧源码快照参与本次测试扫描。
python3 tools/parity_archive.py restore --bundle /path/to/original.bundle \
  --destination /tmp/ponder-parity-evidence
```

也可设置 `PONDER_PARITY_BUNDLE`，或将 bundle 放到清单的默认忽略路径。首次使用会校验大小、SHA-256 和 Git 完整性，再导入 `.local/parity-archive/objects-<hash>.git` 独立缓存，后续从缓存离线恢复。旧对象不会进入主 `.git`；不要手动把旧 bundle fetch 回主仓库，也不要推送原始历史备份分支或标签。

恢复器只写入 SHA-256 校验通过的普通文件并保留可执行位，拒绝越界路径、符号链接、错误 bundle 和已有不同内容。它不访问网络、不执行归档代码。缺失归档会明确报错，不能当作测试跳过；恢复文件也不等于重跑历史测试。旧绝对路径、工具环境和二进制仍可能需要重新准备。

## 新运行产物

新 `runs/` 文件默认忽略，日志继续写入原路径。验收后逐个提交已核对的小型摘要，例如 `git add -f parity/runs/<run-name>/result.json`，不要强制添加整个 run 目录。

长期保存新原始证据前，先制作独立、可取回且已验证的归档，在摘要中记录位置、SHA-256 和恢复方式。现有索引只覆盖固定原提交，不包含未来运行。

`python3 tools/parity_archive.py prune --bundle /path/to/original.bundle` 可精简已恢复的旧文件：先验证全部归档对象和待删除文件，遇到任何改动便拒绝删除，未列入归档的文件保留。归档校验不增加产品测试或上游移植通过数。
