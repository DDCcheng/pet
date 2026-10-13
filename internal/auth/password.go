package auth

import (
	"crypto/subtle"
	"runtime"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// passwordCost 是 bcrypt 的工作因子，每 +1 计算量翻倍。10 在普通机器上一次约几十毫秒。
const passwordCost = bcrypt.DefaultCost

// bcryptSem 限制同时进行的 bcrypt 计算数，最多占一半核。
// bcrypt 一次几十毫秒纯 CPU，登录高峰不限的话会和房间 goroutine 抢 CPU，把对局延迟拖到几百毫秒；
// 限了以后登录排队变慢，对局不受影响。
var bcryptSem = make(chan struct{}, max(1, runtime.NumCPU()/2))

func withBcryptSlot[T any](f func() T) T {
	bcryptSem <- struct{}{}
	defer func() { <-bcryptSem }()
	return f()
}

// HashPassword 生成存库用的 bcrypt 哈希（60 字节，自带随机盐和 cost）。
func HashPassword(plain string) (string, error) {
	var err error
	h := withBcryptSlot(func() []byte {
		var b []byte
		b, err = bcrypt.GenerateFromPassword([]byte(plain), passwordCost)
		return b
	})
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// isBcryptHash 判断库里存的是不是 bcrypt 哈希，不是就当老的明文密码。
func isBcryptHash(s string) bool {
	return len(s) == 60 && (strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$"))
}

// checkPassword 校验密码。needUpgrade 为 true 表示库里还是明文且校验通过，调用方应改写成哈希。
func checkPassword(stored, given string) (ok, needUpgrade bool) {
	if isBcryptHash(stored) {
		ok = withBcryptSlot(func() bool {
			return bcrypt.CompareHashAndPassword([]byte(stored), []byte(given)) == nil
		})
		return ok, false
	}
	// 老数据：明文比较。用常量时间比较，不让耗时泄露"前几位对了"。
	ok = subtle.ConstantTimeCompare([]byte(stored), []byte(given)) == 1
	if !ok {
		// 明文比较是微秒级，不补这一下，耗时就会暴露"这个账号还没迁移"
		burnPasswordCheck(given)
	}
	return ok, ok
}

// dummyHash 用于用户不存在时也跑一次 bcrypt，让"用户不存在"和"密码错"耗时一样。
// 启动时就生成：如果懒加载，第一个查不存在用户的请求会多花一次生成时间，反而又露出了差异。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), passwordCost)

func burnPasswordCheck(given string) {
	withBcryptSlot(func() error { return bcrypt.CompareHashAndPassword(dummyHash, []byte(given)) })
}
