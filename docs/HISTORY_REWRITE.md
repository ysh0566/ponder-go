# Git 历史精简

用户明确将目标设为缩小仓库和克隆体积，并授权在必要时重构历史。本次从所有相关 refs 的历史中移出未保留的 `parity/runs/` 文件，保留开发提交及父子关系。普通完整克隆不再包含旧对象；代码、原始测试、台账和已选摘要保留。

每个旧/新提交都核对所有保留路径的 Git blob 和 mode。提交消息、作者、提交者和原始时间不变，父提交使用对应的新 SHA。先前作者修复的 `parity/runs/author-repair.json` 原字节保留。

完整原历史和映射在 Git 仓库之外保存：

- `.local/history-rewrite-20261122/original.bundle`：操作前完整历史，SHA-256 记录在 `parity/archive-manifest.json`，供历史证据恢复。
- `.local/history-rewrite-20261122/original-refs.txt`、`remote-refs.txt`：原本地与远端 refs，用于审计和发布租约。
- `.local/history-rewrite-20261122/original.git`：安装精简 Git 元数据时保留的原始目录，含原 refs、reflog 和对象。
- `.local/history-rewrite-20261122/commit-map.tsv`：旧/新 SHA 映射；逐提交审计报告与其一并保存。

同一映射和审计结论也以压缩 JSON 保存为 `parity/history-commit-map.json.gz`，随精简克隆获取。表中的旧 SHA 是文本索引，不会让 Git 重新保留原对象。

这些备份不跟踪、不推送，须独立保管。不要在同一远端保留原始历史的备份分支或标签，否则仍会携带旧对象。历史证据恢复方式见 `parity/README.md`，缓存只写入忽略的 `.local`。

本轮结果见 `parity/runs/history-rewrite-20261122/result.json`，包括独立完整克隆的 Git 大小、旧大对象不可达、逐提交内容核对、外部 bundle 恢复验证。首次只减少工作树的 `archive-cleanup-20261122/result.json` 保留为历史记录，其中“保留原历史”的决定已由本轮目标取代。

发布需使用逐 ref 原 SHA 的租约，覆盖全部 10 个现有远端分支，不能覆盖并发新提交；核对时远端没有标签。远端释放不可达对象的时间不由本地操作控制，克隆体积以从新 refs 实测的结果为准。

本地验证完成后，用户已明确批准保持仓库公开并发布本次全部 10 个远端分支的重写历史，作为此前私有发布限制的单次例外。仍须验证 Git 推送身份确为 `ysh0566`；机器现有 `whyareulaughing` SSH 凭据不允许使用，也不创建新 token。用户选择自行配置现有 `ysh0566` Git 凭据后继续；目前远端尚未修改。

远端尚未替换时，本地 `origin` 的 fetch 暂时指向不存在的占位 ref，阻止自动 fetch 把旧历史重新导回。成功按租约发布后，再恢复 `+refs/heads/*:refs/remotes/origin/*` 并获取远端。原始 fetch/跟踪配置保存在独立原始 Git 元数据中。
