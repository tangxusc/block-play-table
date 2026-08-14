# Block Play Table A2A Execution Extension v1

扩展 URI：`https://tangxusc.github.io/block-play-table/a2a/extensions/execution/v1`

本目录包含 A2A execution v1 扩展的 JSON Schema：

- `request.schema.json`：Manager 发给 Worker 的执行请求 DataPart。
- `event.schema.json`：Worker 产生的可投影事件 DataPart。
- `artifact.schema.json`：Artifact metadata DataPart。

运行时不依赖网络获取这些文件。Agent Card 必须把本扩展声明为 required，breaking change 必须发布新的版本路径。

完整协议设计见 [`../../../../a2a-task-protocol.md`](../../../../a2a-task-protocol.md)。
