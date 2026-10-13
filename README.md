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

测试账号在 `deployments/seed.sql`：`alice` `bob` `bot1`~`bot4`，密码都是 `pet`（库里存的是 bcrypt 哈希）。

> `init.sql` / `seed.sql` 只在 MySQL 数据卷**第一次初始化**时执行。改了建表语句要 `docker compose down` 再 `up`（会清空数据）。

不用 Docker 跑服务：先 `docker compose up -d mysql redis`，再在仓库根目录 `go run ./server`。默认连 `127.0.0.1:3307` / `127.0.0.1:6379`，可用 `PET_MYSQL_DSN`、`PET_REDIS_ADDR` 覆盖。

| 环境变量 | 默认 | 作用 |
|---|---|---|
| `PET_MYSQL_DSN` | `root:pet@tcp(127.0.0.1:3307)/pet...` | MySQL 连接串 |
| `PET_REDIS_ADDR` | `127.0.0.1:6379` | Redis 地址（匹配队列 + 登录 token） |
| `PET_LOGIN_RATE` | `1` | `/login` 每个 IP 每秒几次（突发 10）；`0` 关闭，压测时用 |
| `PET_ALLOWED_ORIGINS` | 空 | 允许连 WebSocket 的浏览器来源，逗号分隔；空表示只允许同源 |

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
- **两把锁都不碰对局状态**：`room.Manager.mu` 保护 `roomID→*Room` 表，`auth.Server.mu` 保护 `user→conn` 表（token 在 Redis 里）。
- **readLoop** 跑在 HTTP handler 的 goroutine 里，**writeLoop** 是单独 `go` 出来的，负责写消息和 ping。
- **main 只负责接线**：把 `s.SendTo` 作为 `Out` 注入房间，把 `saveReport` 作为 `OnFinish` 注入房间。房间包不 import store。

### 背压：谁慢谁吃亏，不拖累房间和对手

| 位置 | 满了怎么办 | 计数 |
|---|---|---|
| 房间 `inbox`（64，两人共享） | `select + default` 非阻塞投递，满了丢弃，并回 `room_busy_or_closed` | `pet_inbox_dropped_total` |
| readLoop 限速（每个连接 10 条/秒，突发 10 条） | 超速的对局指令直接拒，回 `rate_limited`，不进 inbox，避免一个人刷满共享 inbox | `pet_rate_limited_total` |
| 每个连接的 `send`（256） | 非阻塞投递，满了丢这条并**踢掉这个连接**（CAS 保证只踢一次） | `pet_send_dropped_total` / `pet_slow_kicked_total` |

房间 goroutine 向连接发消息永远不阻塞，所以一个读得慢的客户端只会把自己踢下线。之后它重连、`join` 就能拿到完整快照。

### 安全

| 措施 | 做法 |
|---|---|
| 密码 | bcrypt（cost 10）。老的明文记录在登录成功时自动改写成哈希；用户不存在也跑一次 bcrypt，响应时间不暴露用户名是否存在 |
| bcrypt 并发上限 | 最多占一半 CPU 核，登录高峰排队，不拖慢对局 |
| token | 32 字节 `crypto/rand`，Redis 里存 `tok:<sha256(token)>`，24 小时过期；无效回 401，Redis 不可用回 503 |
| WebSocket Origin | 不带 Origin（bot / App）放行；否则只允许同源或 `PET_ALLOWED_ORIGINS` 里精确匹配的来源 |
| 登录限速 | 按 TCP 对端 IP 的令牌桶，在查库和 bcrypt 之前拒绝，回 429 + `Retry-After` |
| 请求体 | HTTP 64KB，WebSocket 32KB |

### 断线重连

1. 新连接 `/ws?token=...` 会顶掉旧连接。旧连接的 `removeConn` 比较指针，发现自己已不是当前连接，就**不会**给房间发 `leave`。
2. 客户端发 `join{room_id}`，房间检查成员身份后单推一份 `game_state` 快照。`seq` 不变，客户端靠 `seq` 跳号发现自己丢过消息。
3. 两人都掉线时开始断线宽限（3 分钟）。宽限到期房间关闭，战报记为 `abandoned`。

### 战报与关服顺序

- **战报 = seed + 有序动作日志 + 双方初始卡组 + 卡表 sha256**。battle 是确定性的，拿同一个 seed 重放动作就能还原整局，不用存每一步的快照。
- `end_reason` 有三种：`finished`（分出胜负）、`abandoned`（两人掉线超时）、`aborted`（关服或房间被关）。没开局就散了的房间不写战报。
- 落库用 `context.Background()` 派生的新 ctx。关服时房间 ctx 和 signal ctx 都已经取消，如果沿用它们，战报会写不进去。
- 关服顺序：`srv.Shutdown` → `rooms.Wait()`（等所有房间写完战报）→ `defer db.Close()`。

### 回放与确定性

服务端权威：客户端只发意图，规则全在服务端跑。规则是确定性的：同一个 seed、同一组卡组、同一串被接受的动作，结果一定相同。所以战报不存快照，回放就是把动作重新跑一遍。

- **逐步校验和**：每条动作日志带一个 `h`，是这一步之后完整局面的 fnv64a（包括双方手牌、牌库顺序、下一个随从编号）。重放时逐步比对，第一个对不上的动作就是分叉点。只核对终局不够：改了 seed 但胜负和回合数碰巧一样的战报，只有逐步校验才能发现。这个字段之前的老战报没有 `h`，退回只核对终局。
- **上帝视角帧**：`GET /reports/{room_id}/replay` 在服务端重放，返回每一步之后的双方完整局面（含两边手牌），前端按 `at_ms` 播放即可，不用自己实现规则。
- **谁能看**：只有对局双方能拿回放。其他人和不存在的房间一样返回 404，不暴露房间是否存在。
- **卡表变了**：战报里存了打这局时卡表的 sha256。和当前卡表不一致时返回 409 `cards_changed`，因为用新数值重放出来的不是当时那局。重放和战报对不上时返回 409 `replay_mismatch`。
- **Fuzz**：`FuzzReplayDeterministic` 用随机卡组和随机动作（合法、非法混着）打一局，每一步检查规则不变量（血量、法力、场上数量、随从编号唯一、牌守恒、胜负一致），被拒的动作不能改任何状态，最后重放两次，逐步校验和都要和线上一致。
- **批量校验**：`go run ./scripts/replay -all` 把库里每一局都重放一遍，`-room <id>` 打印一局的逐帧时间轴。

## 目录

```
server/            main：接线、战报落库、路由
internal/auth      登录、保存卡组、WebSocket（readLoop/writeLoop）、限速、/metrics
internal/match     Redis 匹配
internal/room      房间 actor、Manager、动作日志与战报
internal/battle    纯规则：NewState / Apply / ViewFor / Replay，无 IO
internal/replay    战报 → 逐帧回放，回放接口和校验工具共用
internal/deck      卡表加载、卡组校验
internal/store     MySQL 读写
configs/cards.yaml 卡表（改数值不用重新编译）
deployments/       docker-compose、建表、测试账号
scripts/bot        对打机器人
scripts/replay     战报重放与批量校验
```

## 协议

### HTTP

| 方法 | 路径 | 请求 | 响应 |
|---|---|---|---|
| POST | `/login` | `{"username","password"}` | `{"token","player_id"}`；401 账号或密码错，429 登录太频繁 |
| POST/PUT | `/decks` | 头 `Authorization: Bearer <token>`，体 `{"cards":[...]}`，8–12 张，单卡 ≤2 | `{"ok":"true","owner"}` |
| GET | `/ws?token=` | 升级为 WebSocket | 403 来源不允许，401 token 无效，503 token 存储不可用 |
| GET | `/reports?limit=20` | 头 `Authorization: Bearer <token>` | 自己最近的对局：`room_id`、`opponent`、`result`（win/lose/draw/unfinished）、`end_reason`、`rounds`、`first_hand`、`started_at`、`duration_ms`；limit 最大 100 |
| GET | `/reports/{room_id}/replay` | 同上 | `players`（[0] 先手）、`winner`、`seed`、`frames`：每帧 `index`、`at_ms`、`player`、`action`、`state`（双方完整局面）；404 不存在或不是参与者，409 `cards_changed` / `replay_mismatch` |
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

- `internal/match` 和 `internal/auth` 的 Redis 用例需要本地 Redis（`127.0.0.1:6379`，可用 `REDIS_ADDR` 覆盖，分别用 DB 15 和 DB 14）。连不上时这些用例会跳过，不会失败。
- `scripts/bot` 里有进程内的端到端测试：真 WebSocket 加真房间，两个 bot 打完一整局，核对战报，再用战报重放，终局必须和线上完全一样。
- Fuzz（确定性 + 规则不变量）：
  ```bash
  go test ./internal/battle -run '^$' -fuzz=FuzzReplayDeterministic -fuzztime=5m
  ```
- 重放库里所有战报：`go run ./scripts/replay -all`（默认连 `127.0.0.1:3307`，`-dsn` 可改）。
- 端到端（真库）：按"快速开始"起服务后跑 bot，再查战报：
  ```sql
  SELECT room_id, winner, end_reason, rounds, JSON_LENGTH(actions) FROM battle_reports;
  ```
- 压测：服务端要关掉登录限速（所有 bot 来自同一个 IP），再跑 bot 的 `-load` 模式。先登录全部玩家，再按 `-ramp` 陆续开局，登录和对局分开统计：
  ```bash
  PET_LOGIN_RATE=0 docker compose up -d          # 在 deployments/ 下
  go run ./scripts/bot -load -players 2000 -games 1 -ramp 10s
  ```

## 已知限制

- **单实例**：房间在进程内存里，不能水平扩展，重启后进行中的对局会丢失（但会写一份 `aborted` 战报）。token 已经在 Redis 里，重启后不用重新登录。
- **账号安全**：没有登出接口；登录限速只按 IP，没有按账号统计失败次数锁定；部署在反向代理后面时要改成信任代理加的 `X-Forwarded-For`。
- **先手固定**：匹配时排队最早的一方是 `Players[0]`，永远先手（`State.Turn` 从 0 开始），后手也没有补偿。bot 对局里先手赢了 29 局中的 25 局（86%），玩家还能靠抢先排队拿先手。改法：用 seed 随机决定先手，后手多发一张牌。
- **法术卡**（bolt、heal）在卡表里，但服务端没实现，出了会回 `not_implemented`。
- **回放依赖当时的卡表**：改了 `cards.yaml` 之后，旧战报只能返回 `cards_changed`，不能回放。要支持的话得按 `cards_hash` 保存历史卡表。
- **回放是整包返回**：一局几十到一百多帧，一次性返回 JSON。对局很长时可以改成只返回动作，让前端用同一套规则（比如编译成 WASM）自己推演。
- **inbox 两人共享**：限速挡住了单人刷指令，但 inbox 满时丢的可能是任何一方的指令。客户端要靠 `seq` 跳号发现丢失，再重新 `join`。
- **客户端丢了 room_id**：服务端不会主动推 `resume`，需要客户端自己保存 `room_id`。
- **日志**：用的是 `log.Printf`，没有结构化日志（slog）。
- **token 在 URL 里**：浏览器 WebSocket 不能自定义请求头，token 只能放在 `?token=`，可能出现在代理的访问日志里。
