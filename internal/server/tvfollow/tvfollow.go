package tvfollow

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"lz-monitor/internal/server/integrations"
	"lz-monitor/internal/server/store"
)

// 轮询节奏：30s 一 tick（入队判定 + 租约回收），终态历史任务每小时清一次。
// 检查时刻固定：本地 0 点起每 6 小时（00/06/12/18 点，563 用户指定；原滚动
// ≥6h 窗口改对齐固定时刻——错过的条目在下一 tick 立即补查，重启不丢节奏）；
// 配手动「立即检查」。
const (
	tickInterval    = 30 * time.Second
	checkSlotHours  = 6 // 检查时刻间隔：0 点起每 6 小时（563）
	taskTimeoutSec  = 20
	historyKeepDays = 7 * 24 * time.Hour
)

// checkWindowStart 最近一个已过去的检查时刻（本地 0/6/12/18 点整）。条目
// last_queued_at 早于它即待查——固定时刻节奏，错过自动补查（PT 抓取日同款）。
func checkWindowStart(now time.Time) int64 {
	slotHour := now.Hour() / checkSlotHours * checkSlotHours
	w := time.Date(now.Year(), now.Month(), now.Day(), slotHour, 0, 0, 0, now.Location())
	return w.Unix()
}

// Run 阻塞循环：启动先跑一轮，之后每 tick 扫待查条目入队 + 回收超时任务。
func Run(ctx context.Context, st *store.Store) {
	initNotify(st) // 558：提醒聚合出口绑定（不绑定则退化为单条即时直发）
	sweep(st)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	cleanups := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if dead := st.ReclaimExpiredNetTasks(); dead > 0 {
				log.Printf("追剧中转任务租约超时转终态 %d 个", dead)
			}
			sweep(st)
			cleanups++
			if cleanups >= 120 { // 30s × 120 ≈ 每小时
				cleanups = 0
				st.CleanupNetTasks(historyKeepDays)
			}
		}
	}
}

// sweep 把到期待查条目入队（detail 任务，出口=全局设置）。入队即打标
// last_queued_at，回传消费时回写 last_checked_at；查询失败不动进度基线。
// 真正入队了待查条目的轮次打 tv_last_sweep_at 时间戳（63，前端「定时抓取」展示）。
func sweep(st *store.Store) {
	if !Ready(st) {
		return
	}
	egress, key := EgressServerID(st), TmdbKey(st)
	due, err := st.DueTVFollows(checkWindowStart(time.Now()))
	if err != nil {
		log.Printf("追剧待查扫描失败: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}
	setLastSweepAt(st, time.Now().Unix())
	for i := range due {
		f := &due[i]
		taskURL := DetailURL(f.Type, f.TmdbID, key)
		if _, err := st.CreateNetTask(PurposeDetail, strconv.FormatInt(f.ID, 10), egress, taskURL, "GET", nil, "", taskTimeoutSec, false); err != nil {
			if !errors.Is(err, store.ErrNetTaskActive) {
				log.Printf("追剧中转任务入队失败 (follow=%d %s): %v", f.ID, f.Name, err)
				continue
			}
		}
		st.MarkTVFollowQueued(f.ID)
		if f.Type == store.TVFollowTV {
			sweepPT(st, f, egress) // 566：同轮对 CHD/天空/憨憨入队进度搜索任务（电影无集数进度，不扫）
		}
	}
}

// castCandidates 参与主演补拉的候选条数上限（5 条足够辨认同名剧，控制中转
// 任务批量：agent 每轮上报至多领 4 个，搜索 1 + 卡司 5 = 6 个两轮领完）。
const castCandidates = 5

// AttachCast 给搜索候选拉主要演员（550）：前 5 条各入队一个 credits 中转任务，
// 同步等回传（预算 ~25s，覆盖两轮上报领取）；超时/失败的候选不强求，cast
// 留空前端不渲染该行。
func AttachCast(st *store.Store, typ string, candidates []Candidate) {
	if !Ready(st) || len(candidates) == 0 {
		return
	}
	egress, key := EgressServerID(st), TmdbKey(st)
	type pending struct {
		cand   *Candidate
		taskID int64
	}
	var pend []pending
	for i := range candidates {
		if i >= castCandidates {
			break
		}
		c := &candidates[i]
		taskID, err := st.CreateNetTask(PurposeAPI, "cast:"+SearchRefKey(typ, strconv.FormatInt(c.ID, 10)),
			egress, CreditsURL(typ, c.ID, key), "GET", nil, "", 15, false)
		if errors.Is(err, store.ErrNetTaskActive) {
			if active, aerr := st.ActiveNetTask(PurposeAPI, "cast:"+SearchRefKey(typ, strconv.FormatInt(c.ID, 10))); aerr == nil && active != nil {
				taskID = active.ID
			}
		}
		if taskID > 0 {
			pend = append(pend, pending{cand: c, taskID: taskID})
		}
	}
	deadline := time.Now().Add(25 * time.Second)
	for len(pend) > 0 && time.Now().Before(deadline) {
		time.Sleep(waitStep)
		var rest []pending
		for _, p := range pend {
			t, err := st.GetNetTask(p.taskID)
			if err != nil {
				continue
			}
			if t.State == "done" && t.StatusCode == 200 && t.Result != "" {
				p.cand.Cast = ParseCredits(t.Result)
				continue
			}
			if t.State == "done" || t.State == "failed" || t.State == "expired" {
				continue // 终态但无结果：留空
			}
			rest = append(rest, p)
		}
		pend = rest
	}
}

// AttachEmbyStatus 给候选拉 Emby 在库状态（551，添加弹层标题旁小标）：逐条
// 走 549 的名称搜索+TMDB ID 比对（server 直连 Emby，不经中转）。未配 Emby
// 直接返回；首条查询失败视为 Emby 不可达（停机/凭据失效），跳过其余防拖死
// 搜索响应。单条失败只影响该条（留未检查态）。
func AttachEmbyStatus(st *store.Store, candidates []Candidate) {
	// 单行取启用中的 emby 集成（含解密凭据），替代全量集成清单解密后线性找
	emby, err := st.GetEnabledIntegrationByType(store.IntegrationEmby)
	if err != nil || emby == nil {
		return
	}
	for i := range candidates {
		c := &candidates[i]
		names := []string{c.Name}
		if c.OriginalName != "" && c.OriginalName != c.Name {
			names = append(names, c.OriginalName)
		}
		match, err := integrations.FindEmbyByTMDB(*emby, c.Type, c.ID, names)
		if err != nil {
			if i == 0 {
				log.Printf("追剧候选 Emby 在库预检跳过（Emby 不可达）: %v", err)
				return
			}
			continue
		}
		c.EmbyChecked, c.InEmby, c.EmbyItemName = true, match.Found, match.ItemName
	}
}

// ConsumeDetailResult 消费 detail 回传（幂等，api 回传端点与手动检查共用）：
// 首次消费才解析——进度回写 + Emby 在库刷新 + 剧集完结/电影上线推送。查询失败
// 只记 last_error，绝不动进度基线（防查询抖动误报）。
func ConsumeDetailResult(st *store.Store, task *store.NetTask) error {
	first, err := st.ConsumeNetTask(task.ID)
	if err != nil || !first {
		return err // 已被消费（重复回传/手动检查先到）
	}
	id, err := strconv.ParseInt(task.RefKey, 10, 64)
	if err != nil {
		return nil // ref_key 异常，任务作废即可
	}
	follow, err := st.GetTVFollow(id)
	if err != nil {
		return err // 条目已删除等场景，无处回写
	}
	if task.Error != "" || task.StatusCode != 200 || task.Result == "" {
		msg := task.Error
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", task.StatusCode)
		}
		st.SetTVFollowError(id, "查询失败："+msg)
		return nil
	}

	var d *store.TVFollowDetailUpdate
	if follow.Type == store.TVFollowMovie {
		d, err = ParseMovieDetail(task.Result)
	} else {
		d, err = ParseTVDetail(task.Result)
	}
	if err != nil {
		st.SetTVFollowError(id, "响应解析失败："+err.Error())
		return nil
	}

	// Emby 在库检查（549）：server 直连 Emby，不走过转通道；未配 Emby 沿用旧值
	if in, name, ok := CheckEmbyFor(st, follow); ok {
		d.InEmbyChecked, d.InEmby, d.EmbyItemName = true, in, name
		d.EmbyCheckedAt = time.Now().Unix()
	} else {
		d.InEmbyChecked, d.InEmby, d.EmbyItemName = false, follow.InEmby, follow.EmbyItemName
		d.EmbyCheckedAt = follow.EmbyCheckedAt
	}

	// 进度水位保护（566）：PT 通道可能已把进度推到 TMDB 之前——TMDB 回传不回退，
	// 否则两源会在同一集上来回互踩反复提醒
	if d.LastSeason < follow.LastSeason || (d.LastSeason == follow.LastSeason && d.LastEpisode < follow.LastEpisode) {
		d.LastSeason, d.LastEpisode, d.LastAirDate = follow.LastSeason, follow.LastEpisode, follow.LastAirDate
		d.NotifyEpisodeKey = episodeKey(d.LastSeason, d.LastEpisode)
	}

	d.LastCheckedAt = time.Now().Unix()
	if follow.Type == store.TVFollowTV {
		notifyTVProgress(st, follow, d)
	} else {
		notifyMovieProgress(st, follow, d)
	}
	return st.UpdateTVFollowDetail(id, d)
}

// notifyTVProgress 完结判定与聚合入队（558：一轮多部剧合并成一条消息，静默
// 60s 内的回传共享一条；done 打标与进度回写不受影响）。首次查询（添加后建
// 基线）不提醒。5 号口径改版：新集更新不再推送（进度照常回写，供展示与 PT
// 综合水位），完结是剧集唯一的推送时机。
func notifyTVProgress(st *store.Store, follow *store.TVFollow, d *store.TVFollowDetailUpdate) {
	if !tvEnded(d.Status) || follow.Done {
		return
	}
	d.Done = true
	body := fmt.Sprintf("「%s」已完结：全剧 %d 季 %d 集", follow.Name, d.Seasons, d.Episodes)
	if d.LastEpisode > 0 {
		body += fmt.Sprintf("（最终集 S%dE%d 已于 %s 播出）", d.LastSeason, d.LastEpisode, d.LastAirDate)
	}
	body += embyLine(d)
	queueNotify(notifyLine{title: "追剧完结：" + follow.Name, body: body})
}

// notifyMovieProgress 电影流媒体上线推送（5 号口径新增；此前电影不轮询不推
// 送）：watch/providers 平台从无到有视为上线。基线首查不推（添加时已上线的
// 电影没有提醒价值）；推送后平台基线变非空，天然不再重推（平台全下架后再上
// 线会再推，属真实变化）。电影不打 done，持续轮询保持平台与在库数据新鲜。
func notifyMovieProgress(st *store.Store, follow *store.TVFollow, d *store.TVFollowDetailUpdate) {
	if follow.LastCheckedAt == 0 || d.Platforms == "" || follow.Platforms != "" {
		return
	}
	body := fmt.Sprintf("「%s」已上线流媒体：%s", follow.Name, d.Platforms) + embyLine(d)
	queueNotify(notifyLine{title: "追剧上线：" + follow.Name, body: body})
}

// embyLine 推送正文里的在库状态行（549：新集推送顺带提示是否已收录；合并行
// 只在未收录时提示，已收录不占宽度）。
func embyLine(d *store.TVFollowDetailUpdate) string {
	if !d.InEmbyChecked {
		return ""
	}
	if d.InEmby {
		return ""
	}
	return "（Emby 库未收录）"
}

// createTVMessage 站内消息（Type=tv；558 起聚合消息不带单条 Ref 关联）。
func createTVMessage(st *store.Store, subject, body string) {
	m := &store.Message{
		Type: MessageType, Title: subject, Content: body,
		CreatedAt: time.Now().Unix(),
	}
	if err := st.CreateMessage(m); err != nil {
		log.Printf("追剧消息写入失败: %v", err)
	}
}

// CheckEmbyFor 按名称搜索 + TMDB ID 精确比对（多 emby 集成回落首个启用者，
// 528 先例）。未配 Emby 或查询失败返回 ok=false（视为「本次未检查」）。
func CheckEmbyFor(st *store.Store, follow *store.TVFollow) (in bool, item string, ok bool) {
	// 单行取启用中的 emby 集成（含解密凭据），替代全量集成清单解密后线性找
	emby, err := st.GetEnabledIntegrationByType(store.IntegrationEmby)
	if err != nil || emby == nil {
		return false, "", false
	}
	names := []string{follow.Name}
	if follow.OriginalName != "" && follow.OriginalName != follow.Name {
		names = append(names, follow.OriginalName)
	}
	match, err := integrations.FindEmbyByTMDB(*emby, follow.Type, follow.TmdbID, names)
	if err != nil {
		log.Printf("追剧 Emby 在库检查失败 (follow=%d %s): %v", follow.ID, follow.Name, err)
		return false, "", false
	}
	return match.Found, match.ItemName, true
}
