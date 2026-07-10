# AI 定时执行器 — 设计文档

> 工作名：`ai-sched-cli`
> 目标：先把最小可用版本做出来，再逐步补强。

---

## 一、定位

这是一个 **Linux-only、本地优先、单机单进程** 的 AI 定时执行器。

它不是通用任务管理器，也不是完整工作流平台。它只做一条核心链路：

1. 用户用自然语言描述一件未来要处理的事
2. AI 将这句话解释为结构化任务
3. 任务写入本地 SQLite
4. 后台调度器持续检查到期任务
5. 到点后，把 **当前时间 + 原始话术 + 任务上下文 + 当前目录** 交给 AI
6. AI 返回结构化执行结果
7. 本地程序执行允许的动作，并把结果发送到通知通道

一句话概括：

**这是一个“AI 驱动的定时执行壳”，不是“另一个大而全调度平台”。**

---

## 二、核心目标

### 必须解决的问题

1. **任务创建**
   - 把一句人话变成确定的调度描述
   - 需要时做一次确认，避免时间歧义

2. **到点执行**
   - 调度器可靠地发现到期任务
   - 只执行一次，不重复，不漏执行

3. **AI 执行**
   - 到点时把上下文交给 AI
   - AI 只返回结构化结果，不直接操作外部系统

4. **通知投递**
   - 执行结果统一由本地通道层发送
   - AI 失败时仍能降级投递，保证消息不丢

### 明确不做

- 不做 Web 管理台
- 不做多机调度
- 不做插件系统
- 不做任意 Shell 权限开放给 AI
- 不做复杂编排引擎
- 不做 Windows / macOS 支持

---

## 三、三层结构

按当前目标，系统分为三层：

1. **通道配置层**
2. **调度层**
3. **确认层**

### 1. 通道配置层

职责：

- 管理通知通道配置
- 提供统一发送接口
- 做初始化引导和连通性校验
- 提供通道模板，降低配置成本

当前推荐通道：

- `webhook`
- `wecom_robot`，本质上仍是 webhook 模板
- `dida`

设计原则：

- 通道能力统一抽象，不在业务逻辑里写分支
- 企业微信优先通过群机器人 webhook 接入
- 滴答清单作为强提醒和待办闭环通道
- 没必要一开始就做“很多通道”，先把 2 个做好

### 2. 调度层

职责：

- 保存任务
- 扫描到期任务
- 原子领取任务
- 调 AI 执行
- 调本地执行器
- 调通知层投递
- 记录运行结果

设计原则：

- 调度是确定性逻辑，不依赖 AI 解释时间
- AI 只参与“创建时解释”和“到点时生成执行结果”
- 任务状态机必须简单清晰
- 单机单进程先跑通，不提前做复杂分布式能力

### 3. 确认层

职责：

- 在任务创建时处理歧义
- 在初始化时做关键配置确认
- 在 AI 无法稳定解释时回退到用户确认

设计原则：

- 不是每次都确认，只在必要时确认
- 确认只发生在“不可确定”的点
- 一旦确认完成，后续调度全走结构化数据

确认层的价值不是“多一层交互”，而是：

**把模糊自然语言在入库前收敛成确定事实。**

---

## 四、系统总流程

### 1. 创建任务

```text
用户输入一句话
  -> AI 解析任务
  -> 如果无歧义，直接返回结构化任务
  -> 如果有歧义，进入确认层
  -> 用户确认
  -> 写入 SQLite
```

### 2. 到点执行

```text
后台调度器扫描到期任务
  -> 原子领取任务，状态改为 running
  -> 组装执行上下文
  -> 调用 AI，生成结构化执行结果
  -> 本地执行器按结果执行允许动作
  -> 通知层发送结果
  -> 写入 runs 和 delivery 结果
  -> 任务置为 done / failed
```

### 3. 重复任务

```text
任务执行完成
  -> 如果 repeat_rule 为空，结束
  -> 如果 repeat_rule 存在，按规则计算 next_run_at
  -> 更新任务状态为 pending
```

---

## 五、任务模型

### 任务表 `tasks`

```sql
CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    raw_input       TEXT NOT NULL,
    summary         TEXT NOT NULL,
    schedule_type   TEXT NOT NULL,      -- once | recurring
    timezone        TEXT NOT NULL,
    run_at          INTEGER,            -- 一次性任务的执行时间
    repeat_rule     TEXT,               -- recurring 规则，如 daily / weekly|mon
    time_of_day     TEXT,               -- HH:MM:SS，重复任务用
    next_run_at     INTEGER NOT NULL,   -- 下一次要执行的时间戳
    cwd             TEXT NOT NULL,
    channel         TEXT NOT NULL,
    channel_ref     TEXT,               -- 通道附加信息，如 webhook key
    status          TEXT NOT NULL,      -- pending | running | done | failed | cancelled
    confirm_status  TEXT NOT NULL,      -- none | required | confirmed
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    last_error      TEXT
);

CREATE INDEX idx_tasks_next_run_status ON tasks(next_run_at, status);
```

### 运行记录表 `runs`

```sql
CREATE TABLE runs (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id),
    started_at      INTEGER NOT NULL,
    finished_at     INTEGER,
    status          TEXT NOT NULL,      -- running | success | failed
    ai_input        TEXT,
    ai_output       TEXT,
    exec_output     TEXT,
    notify_output   TEXT,
    error           TEXT
);

CREATE INDEX idx_runs_task_id ON runs(task_id);
```

### 可选投递记录表 `deliveries`

第一版可以合并进 `runs.notify_output`，如果后面需要更细的通知重试，再单独拆表。

---

## 六、时间与确认策略

### 基本原则

- `timezone` 是初始化配置，不是每个任务单独猜
- AI 创建任务时只做一次时间解释
- 调度器运行时不再理解自然语言
- 重复任务执行完成后，下一次时间由程序确定性计算

### AI 创建任务返回格式

```json
{
  "action": "create_task",
  "requires_confirmation": false,
  "task": {
    "raw_input": "每天下午六点提醒我看 CI",
    "summary": "看 CI",
    "schedule_type": "recurring",
    "timezone": "Asia/Shanghai",
    "repeat_rule": "daily",
    "time_of_day": "18:00:00",
    "next_run_at": "2026-07-12T18:00:00+08:00",
    "cwd": "/home/admin/code/project",
    "channel": "webhook"
  }
}
```

### 需要确认时的返回格式

```json
{
  "action": "create_task",
  "requires_confirmation": true,
  "question": "你说的“明早”我先按 09:00 理解，可以吗？",
  "draft_task": {
    "summary": "看 CI",
    "schedule_type": "once",
    "timezone": "Asia/Shanghai",
    "next_run_at": "2026-07-12T09:00:00+08:00",
    "cwd": "/home/admin/code/project",
    "channel": "webhook"
  }
}
```

### 什么时候必须确认

- 用户没有说清楚具体时刻
- 重复规则存在歧义
- 句子里同时包含一次性和重复性描述
- AI 置信度不足
- 用户显式要求确认

### 什么时候直接入库

- “明天 9 点提醒我发周报”
- “每天下午 6 点提醒我看 CI”
- “每周一上午 10 点检查构建状态”

---

## 七、调度器设计

### 进程模型

采用 **常驻后台进程**，而不是 cron + tick。

原因：

- 你的目标是“AI 调度器”，不是分钟级提醒器
- 需要每秒扫描或近实时执行
- 到点时要带执行上下文和目录，不适合每分钟拉起一次进程

建议：

- 前台命令：`ai-sched-cli daemon`
- 后台运行方式：`systemd --user`

### 扫描策略

第一版可以先用简单轮询：

```text
while true:
  查询 next_run_at <= now 且 status = pending 的任务
  逐个领取并执行
  sleep 1s
```

这已经够用，单机 SQLite 完全能承受。

后续可优化为：

- 计算最近任务的时间差
- 最长 sleep 1 秒
- 新任务写入时主动唤醒

### 领取任务

必须保证原子性，防止重复执行。

做法：

1. 开事务
2. 选出一条 `pending` 且到期的任务
3. 更新为 `running`
4. 提交事务

如果更新行数为 0，说明任务已被其他执行流领取。

### 状态流转

```text
pending -> running -> done
pending -> running -> failed
pending -> cancelled
failed  -> pending      # 手动重试或自动重试
done    -> pending      # 仅重复任务推进后出现
```

### 重启恢复

daemon 启动时：

- 查找长时间停留在 `running` 的任务
- 标记为 `failed`
- 记录错误为 `scheduler interrupted`

这样可以避免进程崩溃导致任务永远卡死。

---

## 八、AI 执行协议

### 原则

- AI 只返回结构化动作
- AI 不直接发通知
- AI 不直接拥有任意 shell 权限
- 本地代码负责执行动作和权限边界

### 到点执行输入

```json
{
  "task": {
    "id": "task_001",
    "raw_input": "今天下午如果 CI 还红着，就提醒我处理",
    "summary": "检查 CI",
    "cwd": "/home/admin/code/project",
    "channel": "webhook"
  },
  "runtime": {
    "now": "2026-07-11T18:00:00+08:00",
    "timezone": "Asia/Shanghai"
  }
}
```

### 到点执行输出

```json
{
  "action": "notify",
  "should_notify": true,
  "message": "CI 仍然失败，建议现在处理。",
  "priority": "normal"
}
```

或者：

```json
{
  "action": "run_skill",
  "skill": "check-ci",
  "args": {
    "repo": "current"
  },
  "should_notify": true,
  "message": "已触发 CI 检查。"
}
```

### 允许动作白名单

第一版只允许：

- `notify`
- `run_skill`
- `skip`

不要一开始开放：

- 任意 shell
- 任意文件写入
- 任意外部命令拼接执行

### AI 失败降级

如果 AI 超时、返回非法 JSON、执行结果为空：

- 直接发送兜底消息
- 默认消息内容为 `raw_input` 或 `summary`
- 记录 `ai_output` 和错误原因

核心原则：

**AI 死了，任务不能死；AI 乱了，调度不能乱。**

---

## 九、通道配置层设计

### 配置文件

路径建议：

- `~/.config/ai-sched-cli/config.json`

结构示例：

```json
{
  "timezone": "Asia/Shanghai",
  "database_path": "~/.local/share/ai-sched-cli/tasks.db",
  "ai": {
    "endpoint": "http://localhost:11434/api/generate",
    "model": "qwen2.5:7b",
    "timeout_seconds": 15,
    "max_retries": 1
  },
  "daemon": {
    "poll_interval_seconds": 1,
    "stuck_running_timeout_seconds": 300
  },
  "default_channel": "webhook",
  "channels": {
    "webhook": {
      "enabled": true,
      "url": "https://example.com/hook"
    },
    "wecom_robot": {
      "enabled": false,
      "webhook_url": ""
    },
    "dida": {
      "enabled": false,
      "cli_path": "/usr/local/bin/dida365-cli"
    }
  }
}
```

### 通道抽象接口

```go
type Sender interface {
    Name() string
    Check(ctx context.Context) error
    Send(ctx context.Context, msg Message) (*SendResult, error)
}
```

### 推荐顺序

初始化时推荐：

1. `webhook`
2. `wecom_robot`
3. `dida`

原因：

- `webhook` 最通用
- `wecom_robot` 非常适合工作场景
- `dida` 更适合个人强提醒

### init 向导

可以让 AI 参与问答，但写配置和校验必须由本地程序完成。

初始化建议提问：

1. 使用什么时区
2. 默认通道是什么
3. 是否启用 webhook
4. 是否使用企业微信机器人模板
5. 是否启用滴答清单
6. 是否发送测试消息

---

## 十、Go 模块分层

建议目录结构：

```text
cmd/ai-sched-cli/
internal/app/
internal/config/
internal/db/
internal/task/
internal/scheduler/
internal/confirm/
internal/ai/
internal/channel/
internal/channel/webhook/
internal/channel/wecom/
internal/channel/dida/
internal/executor/
internal/runtime/
internal/logx/
```

### 各层职责

#### `internal/config`

- 配置加载
- 默认配置生成
- 初始化写入

#### `internal/db`

- SQLite 打开
- 迁移
- 事务封装

#### `internal/task`

- 任务模型
- 任务仓储
- 任务状态流转
- 重复任务推进

#### `internal/scheduler`

- 轮询循环
- 到期任务领取
- 执行编排
- 异常恢复

#### `internal/confirm`

- AI 确认结果处理
- 创建任务确认流程
- 初始化关键问题确认

#### `internal/ai`

- 封装 AI 请求
- 校验 AI 返回 JSON
- 区分“创建任务协议”和“执行任务协议”

#### `internal/channel`

- 通道接口
- 通道注册
- 路由发送

#### `internal/executor`

- AI 动作执行器
- 白名单动作映射
- 技能调用封装

#### `internal/runtime`

- 执行上下文组装
- 当前时间、目录、环境快照

#### `internal/app`

- CLI 子命令装配
- 跨层协作入口

---

## 十一、命令设计

第一版建议保留这些命令：

```text
ai-sched-cli init
ai-sched-cli add "<自然语言>"
ai-sched-cli list
ai-sched-cli remove <id>
ai-sched-cli run <id>         # 手动执行一条任务
ai-sched-cli daemon
ai-sched-cli status
```

### 说明

- `init`
  - 初始化配置
  - 引导通道接入
  - 测试通道连通性

- `add`
  - 调 AI 解析
  - 必要时进入确认
  - 落库

- `list`
  - 展示任务状态和下一次执行时间

- `run`
  - 便于调试一条任务，不用等调度器

- `daemon`
  - 持续运行调度循环

- `status`
  - 显示配置概览、通道健康、任务数量、daemon 状态

---

## 十二、最小实现顺序

按实际依赖关系，建议这样做：

1. `config`
   - 配置结构
   - 默认配置
   - 初始化读写

2. `db`
   - SQLite 初始化
   - 建表迁移

3. `task`
   - 任务模型
   - CRUD
   - 状态流转

4. `channel/webhook`
   - 最先打通一个通道

5. `ai`
   - 创建任务协议
   - 执行任务协议

6. `confirm`
   - 任务创建确认

7. `scheduler`
   - 轮询
   - 原子领取
   - 执行结果落库

8. `executor`
   - `notify`
   - `skip`
   - `run_skill`

9. `channel/wecom`
   - 企业微信机器人模板

10. `channel/dida`
   - 滴答通道

11. 重复任务推进

12. `status` 和 `run`

---

## 十三、第一版验收标准

第一版完成后，应满足：

1. 可以初始化配置并保存到本地
2. 可以用自然语言创建一条一次性任务
3. 遇到模糊时间时可以要求确认
4. daemon 可以每秒扫描到期任务
5. 到点时可以把上下文交给 AI
6. AI 可以返回结构化执行结果
7. webhook 通道可以成功发送消息
8. AI 失败时可以降级发送兜底消息
9. 重启后不会让 `running` 任务永久卡死

如果这些都满足，这个项目就已经有真正可用的第一版。

---

## 十四、结论

当前最合适的工程策略不是“继续扩展需求”，而是：

**把 AI 调度这条主链做得尽量短、尽量稳、尽量可回放。**

所以实现重点只有三件事：

1. 创建时把模糊输入收敛成确定任务
2. 到点时可靠地领取并执行一次
3. 执行结果稳定地走通道发出去

围绕这三件事写代码，这个项目就不会发散。
