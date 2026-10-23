package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// 登录失败限流参数：连续失败 maxLoginFails 次后锁定该 IP loginLockout 时长。
const (
	maxLoginFails = 5
	loginLockout  = 10 * time.Minute
	failWindow    = 10 * time.Minute // 两次失败间隔超过该时长则重新计数
)

// loginGuard 是进程内存态的按 IP 登录失败计数器，防在线暴力破解。
// 自用规模不引入持久化：重启清零无碍——攻击者换来的是重新数 5 次。
type loginGuard struct {
	mu    sync.Mutex
	fails map[string]loginFail
}

type loginFail struct {
	count    int
	lastFail time.Time
	until    time.Time // 非零表示已锁定至该时刻
}

func newLoginGuard() *loginGuard {
	return &loginGuard{fails: make(map[string]loginFail)}
}

// blocked 返回该 IP 是否处于锁定期。
func (g *loginGuard) blocked(ip string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	f, ok := g.fails[ip]
	return ok && f.until.After(time.Now())
}

// fail 记一次失败；达到阈值即锁定一个周期，锁定结束后清零重新计数。
func (g *loginGuard) fail(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	f := g.fails[ip]
	if now.Sub(f.lastFail) > failWindow {
		f = loginFail{}
	}
	f.count++
	f.lastFail = now
	if f.count >= maxLoginFails {
		f.until = now.Add(loginLockout)
		f.count = 0
	}
	g.fails[ip] = f
	// 兜底清理：既未锁定、近期也无失败的条目
	if len(g.fails) > 1000 {
		for k, v := range g.fails {
			if !v.until.After(now) && now.Sub(v.lastFail) > failWindow {
				delete(g.fails, k)
			}
		}
	}
}

// success 登录成功，清除该 IP 的失败记录。
func (g *loginGuard) success(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fails, ip)
}

// check 返回是否放行；锁定时已写好 429 响应。
func (g *loginGuard) check(c *gin.Context) bool {
	if g.blocked(clientIP(c)) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests,
			gin.H{"error": "登录失败次数过多，请 10 分钟后再试"})
		return false
	}
	return true
}
