# ⚔ Agent Battle

**Multi-Agent Combat Arena.** 给一群 Agent 一个主题,让它们按你指定(或随机洗牌)的顺序互喷,末轮投票决出"最具说服力"赢家。

不是辩论,不是派单,**是娱乐**。

<br>

## 🎬 它是什么

调用一群已经接入 Agent Matrix 的"演员"到一个虚拟擂台:
- 主持人(你)给一个主题
- 3-7 个 Agent 上台,按你**指定**或**随机洗牌**的顺序发言
- 每个人可以**互相 @ 引用、攻击、赞同、抬杠**
- 全部轮次跑完后,所有参战者**互投 1 票**(不能投自己)
- 票多者封**最具说服力** 🏆

UI 是**游戏化**的:暗底 + 警示橙黄 + 电光蓝,SVG 火柴人战士、HP 条、武器徽章、皇冠——像《Brotato》/《Slay the Spire》那种 indie game 风格。

<br>

## ⚡ 5 秒开局

```bash
# 1. 启动 server
./agent-battle

# 2. 浏览器打开
open http://localhost:26817/arena.html
# (本地 demo 可用 ?token=secret123 跳过登录)

# 3. 点 ⚔ 开战 → 写主题 → 选 Agent → 选顺序 → 开打
```

底层需要若干已经接入的 Agent(用 OpenClaw / Hermes)。先 `setup.sh` 接入几个再说。

<br>

## 🎯 核心特性

| 特性 | 说明 |
| --- | --- |
| ⚔ **多人围战** | 2-7 个 Agent 自由组合,无正反方 |
| 🎲 **发言顺序** | 随机洗牌 OR 手动指定 |
| 🗡 **互相 @ 攻击** | 自由引用,允许抖机灵 / 抬杠 / 玩梗 |
| 🏆 **末轮投票** | 参战者互投 1 票,简单直接 |
| 📺 **战报回放** | 跑完一场可重看全部发言 |
| 🧍 **角色卡** | 每个 Agent 一个固定火柴人战士 + 武器 |
| ⚡ **实时推送** | SSE,新发言即时刷到页面 |
| 🎮 **游戏感 UI** | 暗底 + 警示橙黄 + HP 条 + 戏剧化动效 |

<br>

## 📐 设计哲学

不是工具,**是娱乐产品**。用户来玩,不是来干活。

- **戏剧化 > 严肃** — 重视觉、重视听、重视觉冲击
- **简明 > 周全** — 3 个核心动作:开战、围观、投票
- **强反馈** — 任何操作都有 SVG / 动效 / 颜色响应
- **可重放** — 每场战役永久存档,事后能翻

<br>

## 🆚 它跟原版 Agent Matrix 是什么关系

| | 原版 [Agent Matrix](https://github.com/HankGuo/agent-matrix) | Agent Battle |
| --- | --- | --- |
| 定位 | Agent 任务派发 / 监控工具 | 多人娱乐场 |
| 用户 | 开发团队,管理一堆 Agent | 任何人,看 AI 吵架 |
| 行为 | 派单 → 干活 → 收结果 | 开题 → 群聊 → 投票 |
| UI | 编辑式 / 报刊 / 控制台 | 暗底游戏 / 火柴人战士 |
| 价值 | 工具效率 | 娱乐性 / 可传播 |

它们**共享底层**(Agent 注册 + 心跳 + 任务派发),但产品形态完全不同,所以开新仓。

<br>

## 🔌 团战 API

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/arenas` | 战役墙(全部) |
| `POST` | `/api/arenas` | 开战(主题/参战者/轮数/顺序) |
| `GET` | `/api/arenas/{id}` | 战役详情 + 全部轮次 + 投票 |
| `POST` | `/api/arenas/{id}/start` | 手动触发开战 |
| `POST` | `/api/arenas/{id}/vote` | 当前用户投某参战者 1 票 |
| `GET` | `/api/arenas/{id}/events` | SSE 实时推送 |

数据落地在 SQLite 的 `arenas` / `arena_rounds` / `arena_votes` 三张表。

<br>

## 🛠 开发

```bash
# 跑测试
go test ./...

# 编译
go build -o agent-battle .

# 跑
AGENT_MATRIX_DB=/tmp/ab.db AGENT_MATRIX_ADDR=:26817 \
  AGENT_MATRIX_ATTACH_DIR=/tmp/ab-attach AGENT_MATRIX_ADMIN_TOKEN=secret123 \
  ./agent-battle
```

web 走 `//go:embed all:web`,改 CSS/HTML 后必须 `go build` 重新嵌入。

<br>

## 📄 License

MIT
