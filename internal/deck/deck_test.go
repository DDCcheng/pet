package deck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 脚手架
// ---------------------------------------------------------------------------

// writeYAML 把一段 YAML 写进临时目录，返回文件路径。
// t.TempDir() 会在测试结束后自动删掉，不会污染仓库，也不依赖 configs/ 的相对路径。
func writeYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cards.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写临时卡表失败: %v", err)
	}
	return path
}

// 一份合法的最小卡表，多数用例基于它
const goodYAML = `
cards:
  - id: slime
    name: 史莱姆
    type: minion
    cost: 1
    atk: 1
    hp: 2
  - id: wolf
    name: 幼狼
    type: minion
    cost: 2
    atk: 2
    hp: 2
  - id: knight
    name: 骑士
    type: minion
    cost: 3
    atk: 3
    hp: 4
  - id: dragon
    name: 幼龙
    type: minion
    cost: 5
    atk: 5
    hp: 6
  - id: bolt
    name: 雷击
    type: spell
    cost: 1
    damage: 3
  - id: heal
    name: 治疗
    type: spell
    cost: 2
    heal: 3
`

func loadGood(t *testing.T) *Catalog {
	t.Helper()
	cat, err := Load(writeYAML(t, goodYAML))
	if err != nil {
		t.Fatalf("合法卡表不该加载失败: %v", err)
	}
	return cat
}

// ---------------------------------------------------------------------------
// Load — 正常路径
// ---------------------------------------------------------------------------

func TestLoadGoodCatalog(t *testing.T) {
	cat := loadGood(t)

	if cat.Len() != 6 {
		t.Fatalf("Len() = %d, want 6", cat.Len())
	}

	// 字段是否被 yaml tag 正确映射
	c, ok := cat.Get("wolf")
	if !ok {
		t.Fatal("找不到 wolf")
	}
	if c.Name != "幼狼" || c.Type != TypeMinion || c.Cost != 2 || c.Atk != 2 || c.HP != 2 {
		t.Fatalf("wolf 字段解析错误: %+v", c)
	}

	// 法术的专属字段
	b, _ := cat.Get("bolt")
	if b.Type != TypeSpell || b.Damage != 3 {
		t.Fatalf("bolt 字段解析错误: %+v", b)
	}
	// 法术没写 atk/hp，应该是零值
	if b.Atk != 0 || b.HP != 0 {
		t.Fatalf("bolt 不该有 atk/hp: %+v", b)
	}
}

func TestGetMissingReturnsFalse(t *testing.T) {
	cat := loadGood(t)
	c, ok := cat.Get("no-such-card")
	if ok {
		t.Fatal("不存在的卡不该返回 ok=true")
	}
	if c.ID != "" {
		t.Fatalf("不存在时应返回零值 Card，实际 %+v", c)
	}
	if cat.Has("no-such-card") {
		t.Fatal("Has 对不存在的卡应返回 false")
	}
}

// ★ 今天的核心：顺序必须稳定，这是可复现洗牌的地基
func TestIDsKeepFileOrder(t *testing.T) {
	cat := loadGood(t)
	want := []string{"slime", "wolf", "knight", "dragon", "bolt", "heal"}

	// 跑 20 次 —— 如果 IDs() 内部走了 map 遍历，这里会时绿时红
	for i := 0; i < 20; i++ {
		got := cat.IDs()
		if len(got) != len(want) {
			t.Fatalf("IDs() 长度 = %d, want %d", len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("第 %d 次调用，位置 %d: got %q, want %q（顺序不稳定）", i, j, got[j], want[j])
			}
		}
	}
}

// ★ IDs() 必须返回拷贝，否则调用方一改就毁了内部顺序
func TestIDsReturnsCopy(t *testing.T) {
	cat := loadGood(t)
	ids := cat.IDs()
	ids[0] = "TAMPERED"

	if cat.IDs()[0] != "slime" {
		t.Fatal("IDs() 返回的是内部切片本身，外部修改污染了 Catalog")
	}
}

// ---------------------------------------------------------------------------
// Load — 各种坏配置都必须报错
// ---------------------------------------------------------------------------

func TestLoadRejectsBadConfigs(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantHas string // 错误信息里应该出现的关键片段
	}{
		{
			name: "type 拼错",
			yaml: `
cards:
  - id: slime
    name: 史莱姆
    type: minon
    cost: 1
    atk: 1
    hp: 2
`,
			wantHas: "minon",
		},
		{
			name: "id 重复",
			yaml: `
cards:
  - id: wolf
    name: 幼狼
    type: minion
    cost: 2
    atk: 2
    hp: 2
  - id: wolf
    name: 幼狼二号
    type: minion
    cost: 3
    atk: 3
    hp: 3
`,
			wantHas: "重复",
		},
		{
			name: "id 为空",
			yaml: `
cards:
  - name: 无名
    type: minion
    cost: 1
    atk: 1
    hp: 1
`,
			wantHas: "id",
		},
		{
			name: "name 为空",
			yaml: `
cards:
  - id: ghost
    type: minion
    cost: 1
    atk: 1
    hp: 1
`,
			wantHas: "name",
		},
		{
			name: "cost 为负",
			yaml: `
cards:
  - id: slime
    name: 史莱姆
    type: minion
    cost: -1
    atk: 1
    hp: 2
`,
			wantHas: "cost",
		},
		{
			name: "随从 hp 为 0",
			yaml: `
cards:
  - id: ghost
    name: 幽灵
    type: minion
    cost: 1
    atk: 3
    hp: 0
`,
			wantHas: "hp",
		},
		{
			name: "法术既不伤害也不治疗",
			yaml: `
cards:
  - id: nothing
    name: 空气
    type: spell
    cost: 1
`,
			wantHas: "damage",
		},
		{
			name:    "空卡表",
			yaml:    "cards: []\n",
			wantHas: "空",
		},
		{
			name:    "不是合法 YAML",
			yaml:    "cards: [ this is : broken\n",
			wantHas: "", // 只要报错就行，信息由 yaml 库给
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeYAML(t, tc.yaml))
			if err == nil {
				t.Fatal("坏配置必须报错，实际加载成功了")
			}
			if tc.wantHas != "" && !strings.Contains(err.Error(), tc.wantHas) {
				t.Fatalf("错误信息应包含 %q，实际是: %v", tc.wantHas, err)
			}
			t.Logf("err = %v", err) // -v 时能看到报错长什么样
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "not-exist.yaml"))
	if err == nil {
		t.Fatal("文件不存在必须报错")
	}
}

// 错误信息里要有「第几张」，否则几十行的卡表根本没法定位
func TestLoadErrorPointsToCard(t *testing.T) {
	bad := `
cards:
  - id: slime
    name: 史莱姆
    type: minion
    cost: 1
    atk: 1
    hp: 2
  - id: wolf
    name: 幼狼
    type: minion
    cost: 2
    atk: 2
    hp: 2
  - id: broken
    name: 坏卡
    type: wrong
    cost: 1
`
	_, err := Load(writeYAML(t, bad))
	if err == nil {
		t.Fatal("应该报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "3") {
		t.Fatalf("错误信息应指出是第 3 张，实际: %v", err)
	}
	if !strings.Contains(msg, "broken") {
		t.Fatalf("错误信息应包含出错的 id，实际: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ValidateDeck
// ---------------------------------------------------------------------------

// 8 张合法卡组：每张最多 2 张
func validDeck() []string {
	return []string{"slime", "slime", "wolf", "wolf", "knight", "knight", "dragon", "bolt"}
}

func TestValidateDeckOK(t *testing.T) {
	cat := loadGood(t)
	if err := ValidateDeck(cat, Deck{Owner: "alice", Cards: validDeck()}); err != nil {
		t.Fatalf("合法卡组不该报错: %v", err)
	}
}

func TestValidateDeckSizeBoundary(t *testing.T) {
	cat := loadGood(t)

	// 8 和 12 是边界，必须通过
	eight := validDeck()
	twelve := []string{
		"slime", "slime", "wolf", "wolf", "knight", "knight",
		"dragon", "dragon", "bolt", "bolt", "heal", "heal",
	}

	for _, d := range [][]string{eight, twelve} {
		if err := ValidateDeck(cat, Deck{Cards: d}); err != nil {
			t.Fatalf("%d 张应该合法: %v", len(d), err)
		}
	}

	// 7 和 13 必须被拒
	seven := eight[:7]
	thirteen := append(append([]string(nil), twelve...), "slime")
	for _, d := range [][]string{seven, thirteen} {
		if err := ValidateDeck(cat, Deck{Cards: d}); err == nil {
			t.Fatalf("%d 张应该被拒绝", len(d))
		}
	}
}

func TestValidateDeckUnknownCard(t *testing.T) {
	cat := loadGood(t)
	d := validDeck()
	d[0] = "unicorn"
	err := ValidateDeck(cat, Deck{Cards: d})
	if err == nil {
		t.Fatal("卡表里没有的卡应该被拒绝")
	}
	if !strings.Contains(err.Error(), "unicorn") {
		t.Fatalf("错误信息应指出是哪张卡，实际: %v", err)
	}
}

func TestValidateDeckTooManyCopies(t *testing.T) {
	cat := loadGood(t)
	d := []string{"slime", "slime", "slime", "wolf", "wolf", "knight", "dragon", "bolt"}
	err := ValidateDeck(cat, Deck{Cards: d})
	if err == nil {
		t.Fatal("同一张卡 3 份应该被拒绝")
	}
	if !strings.Contains(err.Error(), "slime") {
		t.Fatalf("错误信息应指出是哪张卡，实际: %v", err)
	}
}

func TestValidateDeckNilCatalog(t *testing.T) {
	if err := ValidateDeck(nil, Deck{Cards: validDeck()}); err == nil {
		t.Fatal("catalog 为 nil 时应返回错误而不是 panic")
	}
}
