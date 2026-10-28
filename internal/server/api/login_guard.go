package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// 登录失败限流参数：IP 维度紧（5 次锁定），账号维度宽（20 次）——双桶都查，
// IP 桶防单源爆破，账号桶防代理池轮换 IP 对同一账号的慢速爆破；账号阈值
// 放宽是取舍：攻击者可借失败次数恶意锁号，宽阈值把锁号代价抬到 20 次。
const (
	ipFails      = 5
	userFails    = 20
	loginLockout = 10 * time.Minute
	failWindow   = 10 * time.Minute // 两次失败间隔超过该时长则重新计数
)

// loginGuard 是进程内存态的按键（IP 或用户名）失败计数器，防在线暴力破解。
// 自用规模不引入持久化：重启清零无碍——攻击者换来的是重新计数。
type loginGuard struct {
	maxFails int
	mu       sync.Mutex
	fails    map[string]loginFail
}

type loginFail struct {
	count    int
	lastFail time.Time
	until    time.Time // 非零表示已锁定至该时刻
}

func newLoginGuard(maxFails int) *loginGuard {
	return &loginGuard{maxFails: maxFails, fails: make(map[string]loginFail)}
}

// blocked 返回该键是否处于锁定期。
func (g *loginGuard) blocked(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	f, ok := g.fails[key]
	return ok && f.until.After(time.Now())
}

// fail 记一次失败；达到阈值即锁定一个周期，锁定结束后清零重新计数。
func (g *loginGuard) fail(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	f := g.fails[key]
	if now.Sub(f.lastFail) > failWindow {
		f = loginFail{}
	}
	f.count++
	f.lastFail = now
	if f.count >= g.maxFails {
		f.until = now.Add(loginLockout)
		f.count = 0
	}
	g.fails[key] = f
	// 兜底清理：既未锁定、近期也无失败的条目
	if len(g.fails) > 1000 {
		for k, v := range g.fails {
			if !v.until.After(now) && now.Sub(v.lastFail) > failWindow {
				delete(g.fails, k)
			}
		}
	}
}

// success 清除该键的失败记录。
func (g *loginGuard) success(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.fails, key)
}

// check 返回是否放行；锁定时已写好 429 响应。
func (g *loginGuard) check(c *gin.Context, key string) bool {
	if g.blocked(key) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests,
			gin.H{"error": "失败次数过多，请 10 分钟后再试"})
		return false
	}
	return true
}
