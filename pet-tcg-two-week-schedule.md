# pet-tcg-server 两周日程

目标：边学 Go 边做出可演示的权威 1v1 宠物卡牌服（登录、组卡、匹配、打完一局、战报）。

每天建议 6～8 小时。先学当天那一小块，再只推进「项目」那一行。结束前必须有可运行提交。

核心文件（`match` / `room` / `battle`）自己敲。`main`、SQL、compose 可用 AI 对照着改，但进仓库的代码必须能讲清。

---

## 原则

- 语法只学当天项目用得到的。
- 报错先自己读，超过 20 分钟再问。
- 每天最后 20 分钟写 5 行：今天状态 / 数据怎么走。
- 落后时砍功能，不砍架构。

**不能砍：** 房间单 goroutine、`Apply` 校验、Redis 匹配、能打完一局。

**可以砍：** 技能种类 → 断线重连 → metrics → 战报详情。

---

## 第 1 周：能连上、能排队、能进空房间

### Day 1 — 卡组校验（已完成）

- **学：** `go mod`、包、`struct`、`slice` / `map`、`error`、`if` / `for`、指针与 `range` 拷贝
- **文档：** [Tour 目录](https://go.dev/tour/list)（Basics / Flow control / More types / errors 那一页）；`go mod` 在 [Create a Module](https://go.dev/doc/tutorial/create-module)，不在 Tour 里
- **项目：** `internal/deck` — `Catalog` + `ValidateDeck` + 测试
- **过关：** `go test ./internal/deck/` 全绿

### Day 2 — 并发最小集

- **学：** Tour「Concurrency」：`goroutine`、`chan`、`select`、`context` 取消；不深挖 GMP
- **文档：** [Concurrency](https://go.dev/tour/concurrency/1)
- **项目：** 练习：两个 goroutine 交替打印；带超时的 worker
- **过关：** 能默写交替打印；能说「棋盘不能多协程一起改」

### Day 3 — HTTP 骨架

- **学：** `net/http`、路由、`encoding/json`
- **项目：** `cmd/server/main.go` — `POST /login`（可先内存用户），返回 token
- **过关：** curl 能登录

### Day 4 — MySQL

- **学：** `database/sql`、占位符、`Begin` / `Commit` / `Rollback`
- **项目：** docker compose 起 MySQL；表 `users`、`decks`；登录查库；保存卡组并走 `ValidateDeck`
- **过关：** 重启进程后卡组还在

### Day 5 — WebSocket + 心跳

- **学：** 一个 WS 库、读循环 / 写循环、心跳
- **项目：** `GET /ws`（或等价路径）；连上推 `welcome`；超时踢人；维护 `playerID → conn`
- **过关：** 两个终端能连上并收到服务端消息

### Day 6 — Redis 匹配

- **学：** `go-redis`：`SET`（token）、`ZADD` / `ZRANGEBYSCORE` / `DEL`
- **项目：** `internal/match` — 入队、出队、分窗凑两人、等待超时放宽分差
- **过关：** 两个账号入队后打出 `match_found`

### Day 7 — 空房间循环

- **学：** 复习 `chan` + `select`；房间生命周期
- **项目：** `internal/room` — 每房间一个 goroutine + `inbox`；只广播「已进入 / Waiting」
- **过关：** 能画出「WS 读协程只投递命令，不改棋盘」

---

## 第 2 周：能打完一局并落库

### Day 8 — 卡表与对局模型

- **学：** yaml 或 JSON 配置、结构体 tag、`math/rand` + seed
- **项目：** `configs/cards.yaml`；`internal/battle` 数据模型；同一 seed 洗牌可复现
- **过关：** 洗牌测试稳定

### Day 9 — Apply 第一刀

- **学：** 表驱动测试、`t.Run`
- **项目：** `Apply` — 抽牌、回费、出牌召唤、费不足失败
- **过关：** 非法出牌有错误码；合法则手牌减少、场上多一只

### Day 10 — 攻击与胜负

- **学：** `time.Ticker`（回合超时）
- **项目：** 宠物攻击、死亡下场、打脸、HP ≤ 0 结束；超时自动 `end_turn`
- **过关：** 能打完一局并打印赢家

### Day 11 — 接到房间

- **学：** 消息 `seq`、视角过滤（对手手牌只给数量）
- **项目：** `Apply` 接到房间 `inbox`；广播过滤后的 `state`
- **过关：** A 出牌，B 立刻看到场上变化

### Day 12 — 断线与战报

- **学：** `context` 取消房间；战报 JSON 落库
- **项目：** 断线保留房间；token + roomID 重连拉快照；结束写入 MySQL
- **过关：** 杀掉一个客户端再连上棋盘还在；库里有战报行

### Day 13 — 工程包装

- **学：** Docker Compose 常规用法；结构化日志字段；最简单 `/metrics`
- **项目：** 一键启动；两个 bot 自动打若干局；README（架构图 + 协议 + 已知限制）
- **过关：** 按 README 能把一局跑通

### Day 14 — 打磨，不新开功能

- **学：** 不新学语言；Go 手撕交替打印 + 一道链表 / 数组
- **项目：** 默写房间循环和 `Apply` 校验顺序；简历改一句项目描述
- **过关：** 不看代码能讲完数据流

---

## 每天时间切分（8 小时为例）

| 时段 | 内容 |
|------|------|
| 1.5h | 当天语法 + 官方例子敲一遍 |
| 4h | 只写仓库里今天的目标 |
| 1h | 测试 / 修昨天的坑 |
| 0.5h | 5 行笔记 + git 提交 |
| 余量 | 用 Go 做 1 道 Easy/Medium，或复盘报错 |

前 3 天语法可以占一半。Day 4 起语法不超过 1.5 小时。

---

## 两周结束合格线

- `docker compose up` 后能登录、组卡、匹配、打完一局
- `battle` 包有测试，至少覆盖非法出牌和 seed 复现
- 不看代码能画出数据流
- 能解释：为什么棋盘只在一个 goroutine 里改

没有华丽客户端也算成功。没有匹配、或规则写在 HTTP handler 里，不算成功。

---

## 仓库目标结构（随日程长出来）

```text
pet-tcg-server/
  cmd/server/main.go
  internal/
    deck/          # Day 1
    match/         # Day 6
    room/          # Day 7
    battle/        # Day 8–10
    store/         # Day 4 / 12
  configs/cards.yaml
  deployments/docker-compose.yml
  scripts/bot/
```

---

## 简历可写的一句（Day 14 用）

用 Go 实现权威 1v1 宠物卡牌对局服：WebSocket 房间循环、Redis 分窗匹配、配置化卡表、seed 可复现洗牌、MySQL 战报。
