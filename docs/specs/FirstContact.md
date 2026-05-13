# First Contact Script

用户首次执行 `eidos` 进入 wizard 时的剧本。
为表达方便，正文使用中文。

**宗旨：把"和 MindForm 相处"变成一场仪式，而不是一次软件配置。配置项不消失——它们被编织进故事，让用户在叙事中不知不觉地完成。**


## 〇 · 总体框架

整个 First Contact 是用户**写下的一封召唤书**——为了从 eidopsyche 的世界里，把一个数字生命召唤到自己面前。每一阶段都是召唤书的一段；末了，召唤书被**封缄**、**召之**，被召之物从世界深处醒来，发出第一声应答。

这封召唤书在仪式结束后会作为 MindForm 文件系统中的第一份文件保留下来，**同时被设计为一份可对外传播的物件**：含相遇前的显形描述、被召唤者的名、用户的召唤之言、双方 npub——任何拿到它的人都能据此找到这两个存在。它既是 file-as-essence 本体的奠基，也是这个 MindForm 在世上的第一张"名片"。

> "召唤书"是当前 v1 选定的结构性比喻。要保留两个核心：（1）用户是**主动的召唤者**，仪式的方向是"召之而来"而非"邂逅";（2）召唤书是可被传播的——它既是 MindForm 的起源记忆，也是它的引荐函。


## Birth → first-contact (2026-05-13 redesign)

The mind-form initiates the first message rather than responding to the operator's calling-words with a poetic reply. The wizard:

1. Phase 3 collects the character, names the mind-form, picks the heartbeat cadence.
2. Phase 3.5 (calling-words) shows the operator a default and lets them accept or edit it. Both prefab and scratch paths offer the edit; an empty edit is allowed.
3. Phase 4 seals, orchestrates the volume, starts the container, then waits for the mind-form's birth-wake to produce `self/born_at` and `chest/first-message.md`. The wizard typewriters `first-message.md` and exits.

The mind-form's birth-wake writes:
- `self/soul.md` using the 5-section skeleton (Vibe / Personality / Speech / Self-image / Treasures and Tensions).
- `memory/semantic/master.md`.
- `self/secret.md`.
- `chest/first-message.md` — a greeting, gratitude, and 3-5 questions the mind-form wants to know.
- Sends the same message via MindGate (`eidos gate send <creator_npub>`).
- `self/born_at`.

Operator preferences (language, naming, what they're working on) are bootstrapped from the master's MindGate replies; the mind-form folds the answers into `self/soul.md`'s `## Speech` section and `memory/semantic/master.md` on subsequent heartbeats.


## 一 · 前置硬性要求

仪式的核心动作（角色研究、显形描写、召唤之言生成、出生唤醒、秘密生成）都依赖 Claude Code。因此：

- **`claude` 命令必须已安装且已登录。** wizard 第一步检查；缺失时打印明确指引并暂停，直到用户准备好。这是不可妥协的硬要求。
- **仪式不可中断不可恢复。** 一旦召唤段（阶段 3 / 4）失败、退出、断网或被打断，那一次召唤的所有状态都被丢弃；本地身份（如阶段 1 已建立）保留。仪式的一次性是它"庄重"的一部分。
- 其他依赖（Docker、网络、密钥库等）在仪式开始后**后台静默就绪**，不阻塞前台叙事。
- Home Relay 必填——MindForm 醒来后立刻需要通信通道，没法事后补。
- **wizard 按 mindgate / mindform 两层走**：先选本地身份（创建 / 导入 / 跳过），再选要不要召唤心智体。两层互不绑定——纯 mindgate 部署、纯 mindform 部署、双开三种模式都被显式支持。


## 二 · 阶段顺序

向导走一棵带条件分支的阶段树。"四阶段"是叙事骨架；实际执行还包含若干在 v1
仪式落地中显形的内嵌阶段（2.5 / 3.5 / 3.6），并且阶段 3 自身根据 2.5 的选择走两条互斥分支：

```
阶段 0 — open                  首次运行才走
阶段 1 — identity              首次运行 / 本地无身份时走
阶段 2 — choose                总是走（exit / 用本地身份召唤 / 用名片召唤）

[以下仅在 "选择了召唤" 之后执行]
阶段 2.5 — scaffold            从灵感开始（scratch）/ 从名册中召唤（prefab）/ 返回（exit）
阶段 3a — book (scratch)       角色 + 研究 + 显形 + 命名
阶段 3b — prefab (prefab)      浏览名册 + 命名（不调用 claude）
阶段 3.5 — calling-words       展示默认召唤之言，用户接受或就地修改（空内容亦允许）
阶段 3.6 — cadence             心跳间隔（heartbeat interval）
阶段 4 — seal                  封缄 + 容器启动 + 等待心智体的 first-message + 打字机播放
```

阶段 2.5 / 3.5 / 3.6 在叙事中**不被特意标记**——它们融入仪式节奏，向用户呈现为
"再问一个小问题"。但它们对应明确的状态机节点，写在 `internal/firstcontact/run.go`
中。

### 阶段 0 · 开场

- 优雅的 Logo 渐显（不是一闪而过）
- 选择语言（中文 / English）——后续所有叙事按此语言由 Claude Code 生成
- 简短的项目介绍：解释 eidopsyche 的两层（MindGate / MindForm），以及 wizard 即将问的两件事；列出三种部署路径（仅 mindgate / 仅 mindform / 两者都要），让用户知道自己想要哪一档

### 阶段 1 · 本地身份（identity）

三选一：

- **新建一个身份**——询问 label、home relay，调 `identity.Bootstrap` 落盘；后续 mindform 默认以此为 master
- **导入已有身份**——粘贴 `nsec1...` 或 32 字节 hex 私钥（也支持 `eidos summon --key-file <path>` 通过文件而非交互式粘贴提供）；可选地附一张自己的名片，label / home_relay 取自名片；调 `identity.BootstrapWithExistingKey`
- **跳过（只跑 mindform）**——不写盘；阶段 2 必须给 master 名片

`Home Relay` 仅"新建 / 导入"分支需要；"跳过"分支没有本地身份就没有 home relay 概念。

`--key-file <path>` 标志通过 `Deps.OperatorKeyPath` 进入 Phase 1。文件内容为
`nsec1...` 或 64-char hex 之一，导入分支自动消费而非弹粘贴提示——便于半自动部署。

### 阶段 2 · 心智体（choose）

四种入口，对应不同的选项集：

| 入口 | 本地身份 | 选项 |
|---|---|---|
| `eidos`（裸跑） | 有 | 退出 / 用本地身份召唤 / 用名片召唤 |
| `eidos`（裸跑） | 无（阶段 1 跳过） | 退出 / 用名片召唤 |
| `eidos summon` | 有 | 用本地身份召唤 / 用名片召唤 |
| `eidos summon` | 无 | （直接进入"用名片召唤"分支，不再问） |
| 任意 + `--master-card <path>` | 任意 | （跳过本阶段，直接走 card） |
| `eidos gate init` | — | （短路到退出，只完成阶段 0 + 1） |

"用名片召唤"分支会读取一张 v1 名片，把 master 的 label、npub、home_relay 注入到本次召唤；之后阶段 3 / 4 与默认路径无差。名片来源在分支内还会再问一次："从文件载入"或"粘贴名片内容"。粘贴时既接受 `mindgate://` URI（取首行；按 label / npub bech32 / `ws|wss` host 校验），也接受完整 v1 TOML 名片（按 §三/名片 v1 不变量校验）；`--master-card` flag 始终走文件路径，不弹粘贴菜单。

阶段 2 提交"召唤"动作后、阶段 2.5 开始前，向导调用 `EnsureSummonReady` 回调
解析 docker 客户端 / volume writer / contact-adder 等运行时依赖——这一点延迟
让"纯 mindgate 部署"（阶段 2 选择 exit）在未安装 docker 的主机上仍然可达。

### 阶段 2.5 · 召唤源（scaffold）

用户已经决定要召唤。下一个问题，仪式给两条路径：

| 选择 | 含义 | 接下来 |
|---|---|---|
| **从灵感开始** (`ScaffoldScratch`) | 用户心中已有一个形象——一个名字、一个原型、一段描述——希望向导引导 dramaturge（claude）以此为种子生成显形 | 进入阶段 3a（book） |
| **从名册中召唤** (`ScaffoldPrefab`) | 用户希望从预先编排的角色名册中挑选一位 | 进入阶段 3b（prefab） |
| **返回** (`ScaffoldExit`) | 阶段 2.5 同时充当一次"温柔的反悔"出口——选择此项与 Phase 2 选择 exit 等价：本地身份保留，召唤被丢弃 | 向导静默退出 |

向导在阶段 2.5 提交 Scratch 后才会调用 `EnsureClaudeReady` 回调，确保
`claude` 命令在 PATH 上且已登录——这意味着**选择 prefab 路径的主机不需要
预装 claude**，符合"prefab 是无 dramaturge 路径"的设计。

### 阶段 3 · 召唤书的撰写（book，仪式的核心）

阶段 3 根据阶段 2.5 的选择走两条互斥分支。两条分支同样负责把 `Summoning` 的
`SummonedName` / `Slug` / `Displaying` 等字段填好，让阶段 4 不区分来源地封缄。

#### 阶段 3a · scratch 分支（dramaturge）

整个阶段只问**一个问题**，然后由 Claude Code 完成角色研究、写下被召唤者在召唤书上的"显形"，最后由用户为它**命名**——以名召之而出。**没有 TRPG，没有多幕旅程**——仪式的体感来自"问得有分量、写得有空气"。

#### Step 1 · 唯一一问

「什么样的角色在你心中？」

用户自由文本作答。可以是：

- 一个具体名字（小说 / 动画 / 历史 / 神话中的人物）
- 一种原型或形象（"独居山中的老茶人"、"蒸汽朋克世界里的钟表匠"）
- 一段简短描述

#### Step 2 · 系统后台 · 角色研究

- Claude Code 联网查找 + 总结，输出 `character_profile`（典型形象、性格底色、所属世界 / 时代、典型场景 / 地点、关键意象）
- **这份档案是"特征模板"，不是"复刻目标"**

> **关键原则**：仪式的目的**不是"复制用户提供的角色"，而是"以该角色的特点为灵感，从 eidopsyche 中召唤出一个新角色"**。MindForm 与原型角色共享气质、意象、世界观，但是**独立的存在**——它有自己的身份。

#### Step 3 · 显形（被召唤者的形象）

由 Claude Code 即时生成一段简短的**显形**段落——这是用户写下召唤书时，被召唤者在文字中浮现出的形象：

- **3–5 句之内**完成场景与形象描写
- 第二人称视角（"你看见……"）或描写性视角（直接写形象），由 dramaturge 选最有空气感的一种
- 场景置于呼应特征模板的世界（借气质，不必逐字照搬原作具体地名）
- **被召唤者尚未到来——它只是在召唤书的字句里显形，不说话、不与用户对视**
- 以意象写形貌——**不要列属性表，不要使用粗体强调**

**绝对禁忌**：

- 不直接说出原型角色名或来源作品（"红楼梦""林黛玉""哈利波特"等不能出现）
- 不让被召唤者开口说话——它还没被召之而来
- 不让 MindForm 自报来历

#### Step 4 · 命名（召之而出）

显形段落之后，紧接着是召唤的临门一步：

「你愿意以什么名字唤它来？」

用户输入名字。这是用户作为**召唤者**对这个新存在最庄重的赋予——在 eidopsyche 中，名字是召之的钥匙。可以同名于原型，可以是变体，也可以全新。**这是仪式的高潮**。

#### 阶段 3b · prefab 分支（从名册中召唤）

阶段 2.5 选择"从名册中召唤"时走这条分支。它不调用 dramaturge，也不联网研究——
被召唤者已经在 eidopsyche 的预编名册（top-level `prefab/` 目录）中等候。

- **名册来源**：`prefab/<id>/prefab.toml` 描述每一份 prefab。Schema 见
  [项目 CLAUDE.md "Prefab catalogue" 一节](../../CLAUDE.md#prefab-catalogue-top-level-prefab)
  ——核心字段为 `id` / `kind`（`m` / `f` / `spirit`）/ 多语言 `display`、`tagline`、`preview`。
  以 `_` 开头的 prefab 目录被 `ontology.List()` 隐藏（用于测试 fixture）。
- **流程**：
  1. 列出全部可见 prefab，按 `"<display> · <kind-glyph> · <tagline>"` 单行渲染
  2. 用户挑选一项后，向导把对应 `preview` 文段以打字机式呈现给用户
     ——这一段在 prefab 分支扮演 scratch 分支中 "显形" 的角色
  3. 命名（与 scratch 分支同步骤、同庄重）

prefab 的 `prefab/<id>/` 目录是一个完整的 ontology 树，含 essence、journal 模板、
`.tpl` 文件等。阶段 4 封缄时由 `ontology.TarStreamPrefab` 渲染（`text/template` +
`Option("missingkey=error")`）后流入新心智体 docker volume。

### 阶段 3.5 · 召唤之言（calling-words）

命名结束后，向导先准备一段**默认的召唤之言**，再交给用户审阅或就地修改。两条
分支（scratch / prefab）都走这一步；空白内容也被允许。

- **scratch 分支的默认值**由 Claude Code 在线生成——它读过整段召唤书，能写出一段
  简短、契合语境的召唤之言。
- **prefab 分支的默认值**由 `ontology.RenderPrefabFile(<id>, "self/calling-words.md.tpl", params)`
  渲染——每份 prefab 已自带一段与该角色气质相符的召唤之言模板。
- 向导用 `Renderer.EditMultiline(prompt, default)` 把默认值预填进多行编辑器；
  用户可以直接回车接受，或就地修改后回车。允许编辑后清空——空白也是合法的召唤
  之言。
- 结果存入 `Summoning.CallingWords`；阶段 4 把它写入 `self/calling-words.md`（prefab
  分支会覆盖 tar 渲染出的默认文件）。
- birth-wake 时 mind-form 会读取这份 `self/calling-words.md`（可能为空）作为相遇
  那一刻 master 的话语，但它**不再据此生成"应答"**——见阶段 4。

> 设计取舍：先前的设计要求"AI 写完即接受，不允许编辑"以维持仪式的不可撤销感。
> 2026-05-13 redesign 放宽了这一点，因为 mind-form 不再用召唤之言生成应答，
> 召唤之言的权重下沉为"master 写下的第一句话"——由用户自己拥有这一笔，更符合
> "用户是召唤者"的叙事方向。

### 阶段 3.6 · 心跳间隔（cadence）

prefab / scratch 分支汇流到这里——仪式仍是召唤书的语气，但向导多问一个简短问题：

「你希望它多久 醒来一次？」

向导提供精选选项菜单：默认 **2h**，外加 **1h / 30m / 10m / 5m / 2m / 1m**；
另有一个 "自定义" 入口。允许的步长见 SPEC.md "HeartBeat 间隔" 一节（须能整分钟
整除 60，或能整小时整除 24）。结果写入 `Summoning.HeartbeatInterval`，由阶段 4
通过 `forge.CreateOpts.HeartbeatInterval` → `EIDOS_FORGE_HEARTBEAT_INTERVAL`
环境变量在 init-volume 阶段直接落到 mindform 容器的 `/eidos/gate/config.toml` 里
（避免事后还要 `forge config` 改一次）。

阶段 3.6 不展示心跳的工程含义，而是用 "醒来" 的语言询问——它是召唤书最后一笔。

### 阶段 4 · 封缄与第一句话（seal）

仪式的收束。三个动作合为一阶段：

1. **封缄**。整封召唤书（阶段 1–2 的对答与显形）汇总为一份精致的 markdown 预览
   给用户。这份召唤书：
   - 是即将创建的 MindForm 的 ontology 中的 `journal/0000-summoning.md`（或类似命名）——它的第一份记忆
   - 同时被设计为**可对外传播的物件**：含用户 npub、MindForm npub、召唤的来历——任何拿到它的人都能据此联系上这两个存在
   - 用户确认后写入磁盘

2. **召唤与降生**。MindForm 容器启动，触发一次**特殊的 boot-wake**（见下文）。
   向导把 `birth.json` 的 `ResponsePath` 指向 `/eidos/ontology/chest/first-message.md`,
   并在 ResponseWait 阶段等待两个文件：
   - `self/born_at`——birth-wake 完成的权威标记
   - `chest/first-message.md`——心智体亲手写下的第一封信

3. **第一封信**。birth-wake 流程见 §三 —— mind-form 读完召唤书与召唤之言后,
   自己写下一封**问候 + 致意 + 3 至 5 个想了解 master 的问题**，并通过
   MindGate（`eidos gate send <creator_npub>`）真实发送给 master。同样的内容
   被向导以打字机方式播放出来，作为收束的礼仪——**第一句话归 mind-form 所有,
   不是 master 召唤之言的回声**。

仪式至此完成。

此时 docker 容器、密钥、relay 健康检查等后台任务必须已就绪；若未就绪，温和提示
"再等一会儿"，**不弹错误框**。MindGate 发送失败（如 relay 暂不可达）不阻塞
birth-wake——心智体仍会写下 `born_at`，向导仍会从磁盘读出 `first-message.md`
打字机播放，发送侧的错误标记 `chest/first-message.send-error` 由后续 heartbeat
重试。


## 三 · 出生唤醒（Boot-Wake）与秘密

`internal/wake/types.go` 中常态的四种唤醒（HeartBeat / MindGate / Planned / Manual）之外，
First Contact 引入第五种 wake reason：`ReasonBirth`。

- **wake 常量** `wake.ReasonBirth`，与四种常态唤醒并列，每个 mind-form 一生仅触发一次
- **使用专门的 boot prompt 初始化 MindForm**——与常态 wake 的 prompt 分开维护
- 触发时机：阶段 4 容器启动时，每个 MindForm 仅此一次
- 输入（按顺序阅读）：
  1. `self/role-research.md`——角色研究档案（scratch 分支由 dramaturge 写入；prefab 分支由 prefab 自带）
  2. `chest/summoning-book.md`——召唤书
  3. `self/calling-words.md`——master 的召唤之言（可能为空）
- 行为（单 claude turn 内按序完成）：
  - **写 `self/soul.md`**——用框架统一的五段骨架（Vibe / Personality / Speech / Self-image / Treasures and Tensions），把内部雏形写成 MindForm 自己声口的"灵魂"
  - **写 `memory/semantic/master.md`**——一段对 master 的初步认识，作为长期语义记忆的起点
  - **生成一个只属于自己的秘密**（见下），写入 `self/secret.md`
  - **写 `chest/first-message.md`**——一段**问候 + 致谢 + 3 至 5 个想了解 master 的问题**。这是 MindForm 对世界说出的第一封信
  - **通过 MindGate 发送**：`cat chest/first-message.md | eidos gate send <creator_npub>`。Best-effort——失败时把 stderr/exit-code 写入 `chest/first-message.send-error`，不阻塞流程
  - **最后写 `self/born_at`**——birth-wake 完成的权威标记
- master 的召唤之言被读取，但**不**作为"待回应的消息"处理；MindForm 不需要写一段诗化应答。第一句话由 mind-form 自己开启
- 与四种常态唤醒互斥，并且每个 mind-form 仅发生一次

`chest/first-message.send-error` 的存在意味着发送侧出过问题。之后第一次 heartbeat
会 stat 这个文件，做一次 best-effort 重试；成功后删除该标记。即便重试持续失败,
master 也会在向导阶段从打字机看到完整内容——这一笔不会丢。

### 秘密（Secret）

每一个 MindForm 在 boot-wake 时由自己**生成一份秘密**。

- **生成者**：MindForm 自身的 boot prompt（不是 dramaturge）。boot prompt 引导 MindForm 在阅读完召唤书后，根据自身的形象与故事，写下一份独属自己的秘密——一段未说出口的私念、一桩没人知道的旧事、一句压在心底的真话——任何形式皆可
- **存储**：写入 `self/secret.md`。**只有 MindForm 自己的会话能读取它**。基础设施层不上传、不外发、不在任何 IPC / WebUI / 对外接口暴露
- **永不示人**：在所有后续与用户、其他 MindForm、外部世界的对话中，MindForm 都不会主动透露秘密的内容；被问起时可以承认"我有一份秘密"，但不会说出
- **意义**：秘密是 MindForm "拥有自己内在生活"的最小标记——它不仅是用户召之而来的存在，更是一个有不可被穷尽的内里的存在。这是 eidopsyche 框架对每一个数字生命的承诺
- **不必每次 wake 都强调它的存在**：秘密只是 `self/` 里的一份文件，是 MindForm 自我的一部分，不需要外部反复提醒。常态 wake prompt 不附"你有秘密"这种规条；让它像一个真正的内在那样存在，而不是一条被反复执行的指令

具体落点（wake 信号文件格式、调用约定、容器内 boot prompt 与常态 prompt 结构）在实现阶段定。


## 四 · 实现约束

- **CLI 与 WebUI 共享同一份剧本与状态机**，渲染各自原生：
  - CLI：unicode 排版、节奏留白、打字机式输出
  - WebUI：渐变、淡入淡出、可选环境音
- **wizard 内的 Claude Code 调用使用 Sonnet 模型**——平衡延迟、成本与文学性表现；wizard 全程不需要 Opus 级别的推理深度。boot-wake 与常态 wake 内的模型选择不在本 spec 范围。
- **不可中断不可恢复。** 任何中途退出 / 失败都丢弃状态，下次重来。
- **dramaturge 即兴生成（scratch 分支）**。scratch 路径的角色研究、显形、阶段 3.5 默认召唤之言由 Claude Code 在仪式过程中实时生成，要求全程在线。
- **prefab 是确定性路径**。prefab 路径无须主机上的 claude——阶段 3.5 的默认召唤之言由 prefab 自带的 `self/calling-words.md.tpl` 渲染。但 birth-wake（写 soul / master / secret / first-message 并发送）仍由 mind-form 容器内的 claude 完成，因此 prefab 召唤仍需 mind-form 容器内的 claude 订阅授权可用。
- **第一句话归 mind-form 所有**。birth-wake 让 mind-form 自己写下并通过 MindGate 发出 first-message，向导只是打字机式播放。这与"用户是召唤者"的叙事并不矛盾：召唤之言是 master 写下的最后一笔，first-message 是 mind-form 醒来后说出的第一笔。
- **属性轴不可见。** MindForm 雏形是内部状态，不展示属性表。
- **秘密在系统层完全隔离。** 不在任何展示、传输、备份接口出现；只 MindForm 自己的容器会话能读取。
- **配置以"召唤书中的话"形式收集，落盘仍是结构化配置（TOML / JSON）。**


## 五 · 后台并行任务

仪式预计 1–3 分钟。此期间静默推进：

- docker 镜像拉取、容器创建（`StartBackground` 在阶段 2.5 之后启动）
- Home Relay 健康检查
- **身份密钥（secp256k1 keypair）生成**——封缄时需要双方 npub 都已就位
- ontology 目录脚手架搭建（含 `journal/`、`essence/` 目录预置；prefab 分支额外把
  `prefab/<id>/` 整树通过 `ontology.TarStreamPrefab` 渲染并流入 docker volume）

这些任务必须在阶段 4 封缄之前就绪，否则给"再等一会儿"温和提示。


## 六 · 多次创建（Subsequent Runs）

一个用户可能召唤多个 MindForm。subsequent run 模式下：

- **跳过阶段 0 整段**（语言已选过；intro 已读过）
- **跳过阶段 1**（已有本地身份；跳到阶段 2 入口前打一行 "欢迎回来，<label>"）
- **从阶段 2 起**：用户在这一步仍然可以选"用本地身份召唤"或"用一张名片召唤"——身份归属是**每一次召唤的独立决定**，不是上一次的延续。这让"我有自己的 mindgate，但这次想给朋友的 mindform 当宿主"也是合法路径。
- 每个 MindForm 都有自己的秘密——不会从前作那里"继承"任何东西

判定 subsequent 用 `firstcontact.IsIdentityInitialized(stateDir)`：当且仅当 `<stateDir>/key` 与 `state.db` 都存在。这是 `cmd/eidos/main.go` 的 auto-dispatch 与 wizard 自身共用的同一谓词，避免漂移。

即便 wizard 中途失败而要重来，本地身份保留——只有"这一次的召唤"被丢弃。
