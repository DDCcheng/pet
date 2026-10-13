# pet-tcg-server

Go 写的权威 1v1 宠物卡牌对局服：登录、组卡、Redis 匹配、房间内回合制对战、断线重连、战报落 MySQL。

所有对局状态只在服务端计算，客户端只发"意图"（出牌 / 攻击 / 结束回合），服务端校验后按视角推回状态。

## 快速开始

需要 Docker。

```bash
cd deployments
docker compose up -d --build      # mysql + redis + server，首次会建表并写入测试账号
curl localhost:8080/metrics       # 看到 6 行 pet_* 指标就说明起来了

cd ..
go run ./scripts/bot -games 5                          # bot1 vs bot2 打 5 局
go run ./scripts/bot -users bot1,bot2,bot3,bot4 -games 10
```

测试账号在 `deployments/seed.sql`：`alice` `bob` `bot1`~`bot4`，密码都是 `pet`。

> `init.sql` / `seed.sql` 只在 MySQL 数据卷**第一次初始化**时执行。改了建表语句要 `docker compose down` 再 `up`（会清空数据）。

不用 Docker 跑服务：先 `docker compose up -d mysql redis`，再在仓库根目录 `go run ./server`。默认连 `127.0.0.1:3307` / `127.0.0.1:6379`，可用 `PET_MYSQL_DSN`、`PET_REDIS_ADDR` 覆盖。

## 架构

```
             HTTP /login /decks /metrics
客户端 ────────────────────────────────────▶ auth.Server ──▶ MySQL (users / decks / battle_reports)
   │
   │ WS /ws?token=      enqueue
   └──────────────▶ readLoop ─────────────▶ match.Manager ──▶ Redis ZSET（按分数撮合，窗口随等待时间放宽）
                         │                        │ OnMatch
                         │                        ▼
                         │                  room.Manager.Create ──▶ 每个房间一个 goroutine
```

### 一条指令的路径（4 个 goroutine、3 个 channel）

```
A ─WS→ readLoop(A) ─inbox(64)→ room goroutine ─sendA(256)→ writeLoop(A) ─WS→ A
                              (Apply + ViewFor) └sendB(256)→ writeLoop(B) ─WS→ B
```

- **房间是 actor**：`Battle`、`seq`、动作日志、在线状态都只由房间 goroutine 读写，不加锁。房间用一个 `for-select` 同时处理 `inbox`、断线宽限计时器、回合超时计时器和 `ctx.Done()`。
- **两把锁都不碰对局状态**：`room.Manager.mu` 保护 `roomID→*Room` 表，`auth.Server.mu` 保护 `user→conn` 和 token 表。
- **readLoop** 跑在 HTTP handler 的 goroutine 里，**writeLoop** 是单独 `go` 出来的，负责写消息和 ping。
- **main 只负责接线**：把 `s.SendTo` 作为 `Out` 注入房间，把 `saveReport` 作为 `OnFinish` 注入房间。房间包不 import store。

### 背压：谁慢谁吃亏，不拖累房间和对手

| 位置 | 满了怎么办 | 计数 |
|---|---|---|
| 房间 `inbox`（64，两人共享） | `select + default` 非阻塞投递，满了丢弃，并回 `room_busy_or_closed` | `pet_inbox_dropped_total` |
| readLoop 限速（每个连接 10 条/秒，突发 10 条） | 超速的对局指令直接拒，回 `rate_limited`，不进 inbox，避免一个人刷满共享 inbox | `pet_rate_limited_total` |
| 每个连接的 `send`（256） | 非阻塞投递，满了丢这条并**踢掉这个连接**（CAS 保证只踢一次） | `pet_send_dropped_total` / `pet_slow_kicked_total` |

房间 goroutine 向连接发消息永远不阻塞，所以一个读得慢的客户端只会把自己踢下线。之后它重连、`join` 就能拿到完整快照。

### 断线重连

1. 新连接 `/ws?token=...` 会顶掉旧连接。旧连接的 `removeConn` 比较指针，发现自己已不是当前连接，就**不会**给房间发 `leave`。
2. 客户端发 `join{room_id}`，房间检查成员身份后单推一份 `game_state` 快照。`seq` 不变，客户端靠 `seq` 跳号发现自己丢过消息。
3. 两人都掉线时开始断线宽限（3 分钟）。宽限到期房间关闭，战报记为 `abandoned`。

### 战报与关服顺序

- **战报 = seed + 有序动作日志 + 双方初始卡组 + 卡表 sha256**。battle 是确定性的，拿同一个 seed 重放动作就能还原整局，不用存每一步的快照。
- `end_reason` 有三种：`finished`（分出胜负）、`abandoned`（两人掉线超时）、`aborted`（关服或房间被关）。没开局就散了的房间不写战报。
- 落库用 `context.Background()` 派生的新 ctx。关服时房间 ctx 和 signal ctx 都已经取消，如果沿用它们，战报会写不进去。
- 关服顺序：`srv.Shutdown` → `rooms.Wait()`（等所有房间写完战报）→ `defer db.Close()`。

## 目录

```
server/            main：接线、战报落库、路由
internal/auth      登录、保存卡组、WebSocket（readLoop/writeLoop）、限速、/metrics
internal/match     Redis 匹配
internal/room      房间 actor、Manager、动作日志与战报
internal/battle    纯规则：NewState / Apply / ViewFor，无 IO
internal/deck      卡表加载、卡组校验
internal/store     MySQL 读写
configs/cards.yaml 卡表（改数值不用重新编译）
deployments/       docker-compose、建表、测试账号
scripts/bot        对打机器人
```

## 协议

### HTTP

| 方法 | 路径 | 请求 | 响应 |
|---|---|---|---|
| POST | `/login` | `{"username","password"}` | `{"token","player_id"}` |
| POST/PUT | `/decks` | 头 `Authorization: Bearer <token>`，体 `{"cards":[...]}`，8–12 张，单卡 ≤2 | `{"ok":"true","owner"}` |
| GET | `/ws?token=` | 升级为 WebSocket | — |
| GET | `/metrics` | — | 纯文本指标 |

### WebSocket：客户端 → 服务端

```json
{"type":"enqueue"}                      // 可选 "payload":"1800" 指定分数
{"type":"dequeue"}
{"type":"join",      "data":{"room_id":"..."}}
{"type":"play_card", "data":{"room_id":"...","card_id":"slime"}}
{"type":"attack",    "data":{"room_id":"...","inst_id":"m3","target":"face"}}   // target 是 "face" 或对方随从 inst_id
{"type":"end_turn",  "data":{"room_id":"..."}}
```

### WebSocket：服务端 → 客户端

| type | 内容 |
|---|---|
| `welcome` / `queued` / `dequeued` | `payload` 是用户名 |
| `match_found` | `room_id` |
| `state` | `status`：`waiting` / `ready` |
| `game_state` | `seq`、`me`（含手牌）、`opp`（只有手牌数）、`your_turn`、`round`、`over`、`winner` |
| `game_over` | `winner` |
| `error`（连接层） | `payload`：`rate_limited` / `room_not_found` / `not_in_room` / `room_busy_or_closed` / `bad json` / `enqueue failed` |
| `error`（规则层） | `code` + `msg`，例如 `not_your_turn`、`not_enough_mana` |

### 规则摘要

起始 20 血、3 张手牌。每回合法力上限 +1（最多 10），回满并抽 1 张，牌库空了按疲劳扣血（第 n 次空抽扣 n 点）。随从召唤当回合不能攻击，场上最多 5 个。回合 60 秒，超时自动 `end_turn`。

## 测试

```bash
go test -race -count=3 ./...
```

- `internal/match` 需要本地 Redis（`127.0.0.1:6379`，可用 `REDIS_ADDR` 覆盖，用的是 DB 15）。连不上时这些测试会跳过，不会失败。
- `scripts/bot` 里有进程内的端到端测试：真 WebSocket 加真房间，两个 bot 打完一整局，并核对战报。
- 端到端（真库）：按"快速开始"起服务后跑 bot，再查战报：
  ```sql
  SELECT room_id, winner, end_reason, rounds, JSON_LENGTH(actions) FROM battle_reports;
  ```

## 已知限制

- **单实例**：房间和 token 都在进程内存里，不能水平扩展，重启后进行中的对局会丢失（但会写一份 `aborted` 战报）。
- **token**：没有过期，不持久化，重启后要重新登录。密码是明文存储、明文比较，仅用于演示。
- **法术卡**（bolt、heal）在卡表里，但服务端没实现，出了会回 `not_implemented`。
- **回放**：战报里已经有重放需要的全部数据，`battle.Replay` 还没写。
- **inbox 两人共享**：限速挡住了单人刷指令，但 inbox 满时丢的可能是任何一方的指令。客户端要靠 `seq` 跳号发现丢失，再重新 `join`。
- **客户端丢了 room_id**：服务端不会主动推 `resume`，需要客户端自己保存 `room_id`。
- **日志**：用的是 `log.Printf`，没有结构化日志（slog）。
- **CORS / Origin**：WebSocket 的 `CheckOrigin` 放行所有来源。
