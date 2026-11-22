# 验证运行记录

这里保留经过筛选的历史结果、发布记录和恢复映射；其中原始成功、失败和阻塞状态均不变。被这些摘要引用的大文件已按原路径编入 `../archive-manifest.json` 和 `../archive-files.json.gz`。

需要查看历史日志、冻结源码或运行完整历史验证时，先从仓库根目录执行：

```sh
python3 tools/parity_archive.py restore --bundle /path/to/original.bundle
```

普通 clone 不再包含原始历史对象。需单独取得清单指定 SHA-256 的 bundle；恢复器只将它导入忽略的 `.local` 缓存，不会重新撑大主 `.git`。详细规则见 [`../README.md`](../README.md)。新运行产物默认忽略，只逐个提交经过复核的结果摘要。

归档校验通过只说明证据可完整恢复，不代表这些历史测试刚刚重跑，也不增加上游移植通过数。
