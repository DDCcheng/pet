package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

// checkOrigin 决定浏览器发来的 WebSocket 握手能不能升级。
//   - 没有 Origin 头：放行。浏览器发起的 WS 握手一定带 Origin，不带的是 bot、压测、App 这类非浏览器客户端，
//     它们本来就能伪造任意头，拦了也没有安全收益，只会误伤。
//   - AllowedOrigins 为空：只允许同源（Origin 的 host 和请求的 Host 相同），和 gorilla 的默认行为一致。
//   - 否则：Origin 必须精确等于白名单里的某一项（scheme://host[:port]，不区分大小写）。
func (s *Server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if len(s.AllowedOrigins) == 0 {
		u, err := url.Parse(origin)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	for _, o := range s.AllowedOrigins {
		if strings.EqualFold(strings.TrimRight(o, "/"), origin) {
			return true
		}
	}
	return false
}

func (s *Server) upgrader() *websocket.Upgrader {
	return &websocket.Upgrader{CheckOrigin: s.checkOrigin}
}

// ParseOrigins 把 "https://a.com, http://localhost:3000" 拆成列表，去掉空项和末尾的 /。
func ParseOrigins(v string) []string {
	var out []string
	for _, o := range strings.Split(v, ",") {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}
