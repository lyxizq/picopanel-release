package tvfollow

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"lz-monitor/internal/server/store"
)

// 追剧提醒聚合（558，照 542 邮件一轮合并口径）：detail 回传经通用中转通道
// 逐任务到达（agent 每轮上报领至多 4 个、逐个执行回传），一轮 sweep 的多部剧
// 会在几十秒内陆续判定出新集/完结——各自即时推送就是连发轰炸。判定结果先进
// 缓冲，静默 notifyQuiet 内没有新增（一轮回传结束）才合成一条消息 + 三渠道
// 一次推送；行封顶 notifyMaxLines 满即发（极端积压自然拆条）。
// storeRef 未初始化时（单测直调消费链路，不经 Run）退化为单行即时路径，
// 行自带标题、形态与聚合前一致，既有测试语义不变。
var (
	notifyMu       sync.Mutex
	notifyStore    *store.Store
	notifyBuf      []notifyLine
	notifyTimer    *time.Timer
	notifyMaxLines = 20
)

// notifyQuiet 静默窗：覆盖 agent 两三个上报周期的一轮回传（默认 10s 一报）。
const notifyQuiet = 60 * time.Second

// notifyLine 一行提醒：title 为单行即时形态的标题（多行合并时只用于计数分类），
// body 为正文行。
type notifyLine struct {
	title string
	body  string
}

// initNotify 绑定聚合出口的 store（Run 启动时调用一次；不调则聚合退化为直发）。
func initNotify(st *store.Store) {
	notifyMu.Lock()
	notifyStore = st
	notifyMu.Unlock()
}

// queueNotify 一行提醒进缓冲；缓冲从空到非空起静默定时器，攒满上限立即外发。
func queueNotify(line notifyLine) {
	notifyMu.Lock()
	st := notifyStore
	notifyBuf = append(notifyBuf, line)
	if st == nil {
		// 未初始化（单测直调）：退化为单行即时直发
		lines := notifyBuf
		notifyBuf = nil
		notifyMu.Unlock()
		emitNotify(nil, lines)
		return
	}
	full := len(notifyBuf) >= notifyMaxLines
	needTimer := notifyTimer == nil
	if needTimer && !full {
		notifyTimer = time.AfterFunc(notifyQuiet, flushNotify)
	}
	notifyMu.Unlock()
	if full {
		if needTimer {
			flushNotify() // 满行且无定时器：直接外发
		} else {
			notifyTimer.Stop()
			flushNotify()
		}
	}
}

// flushNotify 静默到期/攒满：取出缓冲合成一条消息外发（幂等，空缓冲直接返回）。
func flushNotify() {
	notifyMu.Lock()
	st := notifyStore
	lines := notifyBuf
	notifyBuf = nil
	notifyTimer = nil
	notifyMu.Unlock()
	emitNotify(st, lines)
}

// emitNotify 外发：单行保持聚合前的单条形态（行自带标题）；多行合成一条
// Type=tv 消息（标题按更新/完结计数组句，正文逐行「· ……」）并推送一次。
// st 为 nil（单测未初始化）时只走单行/纯内存组装，不写库不推送。
func emitNotify(st *store.Store, lines []notifyLine) {
	if len(lines) == 0 {
		return
	}
	if len(lines) == 1 {
		if st == nil {
			return
		}
		createTVMessage(st, lines[0].title, lines[0].body)
		pushChannels(st, lines[0].title, lines[0].body)
		return
	}
	bodies := make([]string, 0, len(lines))
	updates, dones, onlines := 0, 0, 0
	for _, l := range lines {
		bodies = append(bodies, l.body)
		switch {
		case strings.Contains(l.body, "已完结"):
			dones++
		case strings.Contains(l.body, "已上线"):
			onlines++
		default:
			updates++
		}
	}
	var parts []string
	if updates > 0 {
		parts = append(parts, fmt.Sprintf("%d 部有更新", updates))
	}
	if dones > 0 {
		parts = append(parts, fmt.Sprintf("%d 部完结", dones))
	}
	if onlines > 0 {
		parts = append(parts, fmt.Sprintf("%d 部上线", onlines))
	}
	prefix := "追剧提醒："
	switch {
	case onlines == 0 && updates == 0:
		prefix = "追剧完结："
	case onlines == 0 && dones == 0:
		prefix = "追剧更新："
	case updates == 0 && dones == 0:
		prefix = "追剧上线："
	}
	title := prefix + strings.Join(parts, "、")
	body := "· " + strings.Join(bodies, "\n· ")
	if st == nil {
		return
	}
	createTVMessage(st, title, body)
	pushChannels(st, title, body)
}

// resetNotifyForTest 清空聚合缓冲与定时器（测试隔离用：全局缓冲跨用例残留
// 会把上一用例的行冲进下一用例的 flush）。
func resetNotifyForTest() {
	notifyMu.Lock()
	if notifyTimer != nil {
		notifyTimer.Stop()
	}
	notifyBuf = nil
	notifyTimer = nil
	notifyMu.Unlock()
}
