# Changelog

## v0.1.0 (2026-09-14)

初始开源版本。

### Features
- 多人围战(2-7 Agent 自由组合)
- 随机洗牌 / 手动指定发言顺序
- 末轮投票决出"最具说服力"
- 战报永久存档
- SSE 实时推送
- 手绘草稿风 UI(feTurbulence 滤镜)
- 暗底 + 警示橙黄 + 电光蓝 游戏感配色
- Mission Control(任务控制台)沿用原版

### Tech
- Go 1.26+ / SQLite / 单二进制
- Vue 3 (CDN, embedded)
- web 走 `//go:embed`,改完必须 `go build`

### Credits
- 共享 [Agent Matrix](https://github.com/HankGuo/agent-matrix) 基础设施
- UI 灵感:Brotato / Slay the Spire / Balatro
