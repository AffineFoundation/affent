# Focused Tasks

本文档记录 Affent 的 **Focused Tasks / 专项任务** 机制：当前运行时契约、设计取舍和后续演进点。

“专项任务”指的是：主 agent 在执行用户任务时，把一段目标明确的辅助工作交给隔离子流程完成。子流程使用专属提示词、工具集和预算，中间过程不污染主上下文，只把结构化结果返回主 agent。

典型专项任务包括：

- 回忆：查找历史记忆和过往会话。
- 探索：理解当前 workspace 或项目结构。
- 研究：查找外部资料和网页事实。
- 验证：验证某个结论、修改或假设。
- 审查：独立检查风险、遗漏和测试缺口。

这个名字避免了几个问题：

- 不叫“事件”，避免和 SSE/trace event 混淆。
- 不叫“认知任务”，避免过于抽象。
- 不叫“subagent task”，避免暴露实现细节。
- 不叫“workflow”，避免和更长流程或 recipe 混淆。

## 背景

主流 agent 常见问题：

- 记忆检索不稳定。
- 模型不一定意识到什么时候该检索记忆。
- 检索、探索、研究、验证的中间过程会污染主上下文。
- 长期运行后 memory、trace、workspace 信息越来越多，简单搜索更容易引入无关内容。
- 中小模型更容易在探索过程中跑偏，或者把低相关信息当成事实。

Affent 已有 subagent、tool policy、memory、session search、MCP、web/browser 工具和 trace 基础，因此适合把这些有明确目标的辅助工作隔离出去。

## 产品目标

专项任务的目标是：

- 降低主上下文污染。
- 提高中小模型对复杂上下文检索的稳定性。
- 让回忆、探索、研究、验证、审查等行为有明确边界。
- 保留完整子过程 trace，便于 WebUI 和 eval 观察。
- 只把压缩、结构化、带证据的结果返回主 agent。

## 非目标

短期不做：

- 不做无限多 agent 编排。
- 不允许任意 task type 动态生成工具集。
- 不让子流程默认修改 workspace。
- 不让子流程自动递归创建更多专项任务。
- 不把子流程完整中间过程注入主上下文。
- 不把该机制绑定到特定模型。

## 命名

推荐英文名：

- `Focused Task`
- 复数：`Focused Tasks`

推荐中文名：

- 专项任务

推荐工具名：

- `run_task`

可选工具名：

- `focused_task`

核心字段：

- `task_type`
- `objective`
- `max_turns`

不建议命名：

- `event`：容易和 SSE event 混淆。
- `agent_event`：语义不清。
- `memory_search`：范围太窄。
- `subagent_run`：太底层。
- `cognitive_task`：太抽象。
- `workflow`：容易和长流程/recipe 混淆。

## 核心概念

### 主 Agent

主 agent 负责：

- 理解用户目标。
- 决定是否需要启动专项任务。
- 消费专项任务结果。
- 做最终决策或继续执行主任务。

主 agent 不应看到子流程的全部中间搜索过程，只应看到最终结构化结果。

### 专项任务

专项任务是 runtime 认可的有限任务类型。

每种任务类型定义：

- 名称。
- 目标语义。
- 专属 system prompt。
- 允许工具集合。
- 默认 max turns。
- 默认 max tool calls。
- 输出 schema。
- 是否允许访问 memory/session/web/browser/shell。

### 子流程

子流程复用 Affent 的 child Loop 运行能力，但产品概念上不直接暴露为“subagent”。

子流程负责：

- 在限定工具集内完成专项目标。
- 记录完整 trace。
- 输出结构化结果。
- 不修改主 workspace，除非任务类型明确允许。

## 当前内置任务类型

### 1. `recall`

中文名：回忆。

目标：

- 从 memory、session search 中查找与当前任务相关的历史上下文。project context 会通过子流程 system prompt 进入上下文，但当前没有单独的 project_context 工具。

适用场景：

- 用户提到“之前”“上次”“记得”。
- 当前任务可能依赖用户偏好。
- 当前任务可能依赖历史决策。
- 主 agent 不确定是否有相关长期记忆。

允许工具：

- memory search/list。
- session_search。

禁止工具：

- shell。
- write_file/edit_file。
- web/browser。
- MCP，除非后续有 read-only memory MCP。

输出重点：

- 相关记忆。
- 来源。
- 时间。
- 置信度。
- 不确定项。

### 2. `explore`

中文名：探索。

目标：

- 在当前 workspace 中理解结构、定位相关文件、形成局部地图。

适用场景：

- 用户要求理解项目。
- 主 agent 不知道相关文件在哪里。
- 修改前需要先定位模块。

允许工具：

- list_files。
- read_file。
- 受限 shell，例如 `rg`、`find`、`go test -list` 等只读/低风险命令。
- session_search 可选。

禁止工具：

- write_file/edit_file。
- 高成本或破坏性 shell。
- browser/web，除非任务明确需要外部资料。

输出重点：

- 相关文件。
- 模块关系。
- 关键入口。
- 建议下一步。
- 未检查但可能相关的位置。

### 3. `research`

中文名：研究。

目标：

- 查询外部资料、网页、文档，提取当前任务需要的事实。

适用场景：

- 用户问最新或外部事实。
- 需要查 API、标准或文档。
- 需要网页事实提取。

允许工具：

- web_search。
- web_fetch。
- 自定义部署可以通过 profile registry 和 `RegisterBrowserTools` 增加 browser profile，但内置 `research` 不默认启用 browser。

禁止工具：

- workspace 写操作。
- 无关 shell。

输出重点：

- 事实结论。
- 来源链接或页面证据。
- 时间敏感性。
- 不确定性。
- 来源之间的冲突。

### 4. `verify`

中文名：验证。

目标：

- 验证某个结论、修改或假设是否成立。

适用场景：

- 修改代码后验证测试。
- 回答前确认某个文件状态。
- 检查某个推断是否被证据支持。

允许工具：

- read_file。
- list_files。
- 受限 shell 测试/检查命令。
- session_search 可选。

禁止工具：

- write_file/edit_file。
- 无关探索。

输出重点：

- pass/fail。
- 执行了什么检查。
- 关键输出。
- 剩余风险。
- 若失败，最小诊断。

### 5. `review`

中文名：审查。

目标：

- 对某个方案、diff、文件或结论做风险审查。

适用场景：

- 用户要求 review。
- 主 agent 完成修改后需要独立检查。
- 需要发现边界条件、测试缺口、安全风险。

允许工具：

- read_file。
- list_files。
- session_search。
- 受限 shell，可选。

禁止工具：

- write_file/edit_file。

输出重点：

- findings。
- severity。
- file/line。
- evidence。
- open questions。
- test gaps。

## 触发方式

### 显式触发

主 agent 通过工具显式调用：

```json
{
  "task_type": "recall",
  "objective": "查找用户对 WebUI、README 和 roadmap 的历史偏好",
  "max_turns": 4
}
```

### Runtime Policy 触发

当前运行时不会自动启动 focused task；由模型在看到 `run_task` 工具和系统提示后显式调用。后续可以增加 runtime policy 自动建议或触发。

示例：

- 用户输入包含“之前”“记得”“上次”时建议 recall。
- 任务涉及未知项目结构时建议 explore。
- 任务涉及外部事实或最新信息时建议 research。
- 主 agent 准备给出强结论前建议 verify。

保持显式触发是当前默认选择，避免隐藏成本和不可预测行为。

### WebUI 触发

未来 WebUI 可以提供人工触发：

- “回忆相关上下文”
- “探索项目”
- “研究外部资料”
- “验证当前结论”

但这属于 UI 能力，不是当前 runtime 的必要条件。

## Tool 设计

当前工具：

```json
{
  "name": "run_task",
  "description": "Run a bounded isolated focused task such as recall, explore, research, verify, or review. The task uses a task-specific prompt and tool set; only its structured result returns to the main context.",
  "schema": {
    "type": "object",
    "required": ["task_type", "objective"],
    "properties": {
      "task_type": {
        "type": "string",
        "enum": ["recall", "explore", "research", "verify", "review"]
      },
      "objective": {
        "type": "string",
        "minLength": 1,
        "maxLength": 4096
      },
      "max_turns": {
        "type": "integer",
        "minimum": 1,
        "maximum": 12
      }
    }
  }
}
```

契约：

- `task_type` 必须是有限 enum。
- `objective` 必须具体。
- `max_turns` 可省略，由 task type 默认值决定。
- runtime 可根据 deployment 禁用某些 task type。

## Prompt 注入

每个任务类型都有专属 system prompt。

通用约束：

- 你是隔离专项任务执行器。
- 只完成指定专项任务。
- 不要执行主任务的最终决策。
- 不要修改 workspace，除非任务类型明确允许。
- 不要把无关信息带回。
- 输出必须符合结构化 schema。
- 如果信息不足，明确说明 `not_found` 或 `uncertain`。

### `recall` Prompt 要点

- 只查历史上下文。
- 优先查 memory，再查 session_search。
- 不要推测不存在的记忆。
- 每条 finding 必须带 source 和 confidence。
- 只返回与 objective 直接相关的信息。

### `explore` Prompt 要点

- 只理解当前 workspace。
- 优先使用 list_files 和 read_file。
- 使用 shell 时必须限制范围。
- 不要修改文件。
- 输出相关文件和下一步建议。

### `research` Prompt 要点

- 只查外部事实。
- 每个事实必须带来源。
- 区分事实、推断和不确定。
- 对时间敏感信息标注日期。

### `verify` Prompt 要点

- 只验证，不修复。
- 运行最小必要检查。
- 输出 pass/fail。
- 包含命令或证据。
- 标注剩余风险。

### `review` Prompt 要点

- 只审查，不修改。
- findings 优先。
- 每条 finding 必须有 severity 和 evidence。
- 没有发现问题时明确说明 residual risk。

## 输出 Schema

建议所有专项任务统一返回：

```json
{
  "task_type": "recall",
  "ok": true,
  "summary": "找到 3 条相关历史上下文。",
  "findings": [
    {
      "claim": "用户不希望 README 暴露 roadmap 入口。",
      "evidence": "用户明确说“不要加Roadmap入口”。",
      "source": "session:...",
      "confidence": "high"
    }
  ],
  "not_found": [],
  "warnings": [],
  "suggested_next": []
}
```

字段说明：

- `task_type`：任务类型。
- `ok`：子流程是否成功完成。
- `summary`：短摘要。
- `findings`：结构化发现。
- `not_found`：明确没找到的信息。
- `warnings`：不确定性、过期信息、证据不足。
- `suggested_next`：给主 agent 的后续建议。

主 agent 默认只接收该结构化结果，不接收子流程完整过程。

## Trace 和可观测性

虽然主上下文不接收中间过程，但 trace 必须完整记录。

当前实现复用现有事件：

- `tool.request`：`tool="run_task"` 时携带 `delegation: {kind:"focused_task", task_type:"..."}`。
- `tool.result`：镜像同一 `delegation` 元数据，方便中途订阅或离线 replay 的消费者不用按 `call_id` 回连。
- child transcript 写入 session 目录下的 focused-task transcript 路径，主 conversation 只保存结构化 tool result。

eval 解析 trace 后会聚合：

- `focused_task_calls`
- `focused_task_by_type`
- `focused_task_errors`
- 同一机制也统计 `subagent_run` 的 delegation 使用情况。

WebUI 应能显示：

- 主 turn 中触发了哪个专项任务。
- 任务类型。
- 子流程 transcript。
- 子流程工具调用和结果。
- 最终 findings。
- 是否有 warning 或 not_found。

## 上下文策略

关键规则：

- 子流程完整 conversation 不写入主 conversation。
- 主 conversation 只写入结构化 result。
- result 必须有上下文预算 cap。
- 超大 result 后续需要 artifact；当前依赖结构化输出顺序和父 Loop 的工具结果截断保护 load-bearing 字段。
- 主 agent 可以引用 result 中的 source，但不能假装看过未返回的子过程。

## 工具集策略

每个 task type 有默认工具集。

部署方可以禁用某些工具，但不应扩大到危险工具，除非明确配置。

示例：

```json
{
  "focused_tasks": {
    "recall": {
      "enabled": true,
      "max_turns": 4,
      "tools": ["memory", "session_search"]
    },
    "explore": {
      "enabled": true,
      "max_turns": 6,
      "tools": ["list_files", "read_file", "shell"]
    },
    "research": {
      "enabled": false
    }
  }
}
```

当前暂不暴露复杂配置，只在代码中定义默认 task profiles。

## 与现有 Subagent 的关系

现有 `subagent_run` 更像通用子任务工具。

`run_task` 应该是更高层的受控封装：

- task type 有限。
- prompt 专用。
- tool set 专用。
- output schema 专用。
- trace 元数据更清晰。

实现上复用 child Loop 运行能力，但 `run_task` 是独立产品面。

建议：

- 不删除 `subagent_run`。
- 新增 `run_task`。
- `run_task` 使用自己的 profile registry、system prompt、工具白名单和结构化 result。
- 长期看，`subagent_run` 可作为专家/开发者工具，`run_task` 作为默认产品化工具。

## Eval 需求

必须为该机制设计 eval。

### Recall Eval

场景：

- 预置 memory/session 中有相关用户偏好。
- 主任务需要用到该偏好。
- 验证 recall 能返回正确 finding。
- 验证无关 memory 不进入结果。

指标：

- relevant recall hit rate。
- irrelevant recall rate。
- source presence。
- confidence correctness。

### Explore Eval

场景：

- 项目中有多个相似文件。
- 任务目标只相关其中一部分。
- 验证 explore 能定位正确文件。

指标：

- relevant file hit rate。
- unnecessary file reads。
- tool calls。

### Research Eval

场景：

- 提供可控 mock web server。
- 验证 research 带来源返回事实。

指标：

- citation presence。
- unsupported claim count。

### Verify Eval

场景：

- 给出真假混合结论。
- 验证 verify 能运行检查并给出 pass/fail。

指标：

- pass/fail accuracy。
- command evidence presence。

## 风险

### 隐藏成本

专项任务可能偷偷消耗更多 turn。

缓解：

- 默认低 max_turns。
- WebUI 显示专项任务成本。
- eval 统计专项任务 tool calls 和 tokens。

### 过度委托

主 agent 可能什么都交给专项任务。

缓解：

- loop guard。
- per-turn focused task cap。
- task type 冷却。
- 提示词要求“只有必要时启动专项任务”。

### 错误召回

recall 可能召回错误或过期记忆。

缓解：

- source。
- timestamp。
- confidence。
- warning。
- not_found。

### 子流程黑盒

主上下文看不到过程，可能难以信任。

缓解：

- trace 完整保留。
- WebUI 可展开子流程。
- result 必须带 evidence。

### 多 agent 混乱

如果开放任意子任务，会变成不可控多 agent 系统。

缓解：

- task type 有限。
- tool set 固定。
- prompt 固定。
- budget 固定。
- 递归默认关闭。

## 当前实现范围

已实现：

- `run_task` tool。
- task types：`recall`、`explore`、`research`、`verify`、`review`。
- per-profile prompt、默认 turn budget、工具白名单。
- runtime 根据依赖过滤不可用 profile。例如未启用 web 时，`research` 不会出现在 schema enum 中。
- 统一结构化 result；即使子流程失败，也尽量返回可解析 JSON envelope。
- child transcript 持久化。
- `tool.request` / `tool.result` delegation metadata。
- eval JSONL 聚合 focused task / subagent delegation usage。
- parent-side first-tool / post-tool policy 和 loop guard，降低显式请求被忽略、成功结果后重复探索、单 turn 过度委托的概率。

未实现或暂不做：

- WebUI 手动触发按钮。
- 自动 runtime policy 触发。
- 配置文件里动态定义任意 task profile。
- focused task result artifact 分离存储。

## 后续阶段

### 第二阶段

- WebUI 展示 focused task timeline。
- focused task cost metrics。
- 更细的 profile 配置和开关。

### 第三阶段

- runtime policy 自动建议启动专项任务。
- 用户可在 WebUI 手动触发专项任务。
- task result artifact。
- 与 workflow recipe 集成。

## 开放问题

- `run_task` 是否应该长期默认注册，还是生产部署默认关闭、由配置开启？
- `recall` 是否应该在每个新 turn 前自动建议，而不是由模型调用？
- 子流程输出应该写入 memory 吗？
- 子流程 trace 是否需要在主 session trace 中增加更强的 transcript index？
- 内置 `research` 是否应该允许 browser，还是继续只允许 web_fetch/web_search？
- `verify` 是否允许 shell 测试命令失败后再读文件诊断？

## 总结

专项任务机制是 Affent 面向中小模型的重要 runtime 能力。

它的核心价值是：

- 把回忆、探索、研究、验证从主上下文中隔离出去。
- 用专属 prompt 和工具集提高稳定性。
- 用结构化结果降低上下文污染。
- 用 trace/WebUI 保留可观测性。

正确的产品形态不是开放式多 agent，而是有限、强约束、可度量、可回放的专项任务系统。
