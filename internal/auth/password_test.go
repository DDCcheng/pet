package auth

import (
	"strings"
	"testing"
	"time"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("pet")
	if err != nil {
		t.Fatal(err)
	}
	if !isBcryptHash(h) {
		t.Fatalf("not a bcrypt hash: %q", h)
	}
	if len(h) > 64 {
		t.Fatalf("hash len %d > users.password VARCHAR(64)", len(h))
	}
	h2, _ := HashPassword("pet")
	if h == h2 {
		t.Fatal("same password hashed twice should differ (random salt)")
	}
	if ok, up := checkPassword(h, "pet"); !ok || up {
		t.Fatalf("right password: ok=%v upgrade=%v", ok, up)
	}
	if ok, _ := checkPassword(h, "pet2"); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestCheckPasswordLegacyPlaintext(t *testing.T) {
	cases := []struct {
		stored, given string
		ok, upgrade   bool
	}{
		{"pet", "pet", true, true},
		{"pet", "pe", false, false},
		{"pet", "", false, false},
		// 明文恰好长得像哈希前缀但长度不对，仍按明文处理
		{"$2a$short", "$2a$short", true, true},
	}
	for _, c := range cases {
		ok, up := checkPassword(c.stored, c.given)
		if ok != c.ok || up != c.upgrade {
			t.Errorf("checkPassword(%q,%q) = %v,%v want %v,%v", c.stored, c.given, ok, up, c.ok, c.upgrade)
		}
	}
}

func TestSeedHashMatchesPet(t *testing.T) {
	// deployments/seed.sql 里写死的哈希
	const seed = "$2a$10$HwZC6M80Ig08R7iMVsBoZuA1YHQzUOKupgyor5H367s6fRfOtum/e"
	if ok, _ := checkPassword(seed, "pet"); !ok {
		t.Fatal("seed hash does not match password pet")
	}
}

func TestHashPasswordTooLong(t *testing.T) {
	// bcrypt 只看前 72 字节，库会直接拒绝更长的输入，而不是悄悄截断
	if _, err := HashPassword(strings.Repeat("a", 73)); err == nil {
		t.Fatal("want error for >72 byte password")
	}
}

func TestUnknownUserCostsLikeWrongPassword(t *testing.T) {
	h, _ := HashPassword("pet")

	const n = 5
	start := time.Now()
	for i := 0; i < n; i++ {
		checkPassword(h, "wrong")
	}
	wrong := time.Since(start)

	start = time.Now()
	for i := 0; i < n; i++ {
		burnPasswordCheck("wrong")
	}
	unknown := time.Since(start)

	// 同一个 cost，耗时应该在同一量级；这里只防"用户不存在时直接返回"这种数量级的差距
	if unknown < wrong/3 {
		t.Fatalf("unknown-user path too fast: %v vs wrong-password %v", unknown, wrong)
	}
}

func TestBcryptWaitsForFreeSlot(t *testing.T) {
	h, _ := HashPassword("pet")
	// 占满所有槽位，模拟登录高峰
	for i := 0; i < cap(bcryptSem); i++ {
		bcryptSem <- struct{}{}
	}
	done := make(chan bool)
	go func() {
		ok, _ := checkPassword(h, "pet")
		done <- ok
	}()
	select {
	case <-done:
		t.Fatal("bcrypt ran while all slots were taken")
	case <-time.After(200 * time.Millisecond):
	}
	for i := 0; i < cap(bcryptSem); i++ {
		<-bcryptSem
	}
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("right password rejected")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still blocked after slots were released")
	}
}
