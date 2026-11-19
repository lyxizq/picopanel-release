package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lz-monitor/internal/server/store"
)

// newQBitMock 起 qBittorrent 假上游：登录（校验账号密码）+ transfer/info +
// torrents/info；hits 记录各路径命中数（缓存行为断言用）。
func newQBitMock(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/auth/login", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = r.ParseForm()
		if r.PostFormValue("username") == "admin" && r.PostFormValue("password") == "pw" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sess1", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		_, _ = w.Write([]byte("Fails."))
	})
	mux.HandleFunc("/api/v2/transfer/info", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("X-Api-Key") == "k5.2" {
			_, _ = w.Write([]byte(`{"dl_info_speed":1048576,"up_info_speed":2048}`))
			return
		}
		if ck, err := r.Cookie("SID"); err != nil || ck.Value != "sess1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"dl_info_speed":1048576,"up_info_speed":2048}`))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`[
			{"state":"downloading"},{"state":"stalledDL"},{"state":"uploading"},
			{"state":"stalledUP"},{"state":"forcedUP"},{"state":"pausedUP"}
		]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenWithKey(filepath.Join(t.TempDir(), "it.db"), filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestFetchQBitLogin 登录路径：SID cookie 携带、状态计数与速率解析。
func TestFetchQBitLogin(t *testing.T) {
	hits := &atomic.Int64{}
	srv := newQBitMock(t, hits)
	in := store.Integration{Type: store.IntegrationQBit, URL: srv.URL,
		Auth: MarshalCredentials(Credentials{Username: "admin", Password: "pw"})}
	data, err := fetchQBit(in)
	if err != nil {
		t.Fatal(err)
	}
	if data["download_bps"].(int64) != 1048576 || data["upload_bps"].(int64) != 2048 {
		t.Fatalf("速率不一致: %v", data)
	}
	// 6 任务：downloading+stalledDL=2 下载中；uploading+stalledUP+forcedUP=3 做种；pausedUP 不计
	if data["leech"].(int) != 2 || data["seed"].(int) != 3 {
		t.Fatalf("计数不一致: %v", data)
	}
}

// TestFetchQBitToken qB ≥5.2 API Key 路径：X-Api-Key 头免登录。
func TestFetchQBitToken(t *testing.T) {
	hits := &atomic.Int64{}
	srv := newQBitMock(t, hits)
	before := hits.Load()
	in := store.Integration{Type: store.IntegrationQBit, URL: srv.URL,
		Auth: MarshalCredentials(Credentials{Token: "k5.2"})}
	data, err := fetchQBit(in)
	if err != nil {
		t.Fatal(err)
	}
	if data["download_bps"].(int64) != 1048576 {
		t.Fatalf("速率不一致: %v", data)
	}
	// 无登录请求（token 免登录）：只应有 transfer+torrents 两次命中
	if n := hits.Load() - before; n != 2 {
		t.Fatalf("token 路径不应登录，命中 %d 次", n)
	}
}

// TestFetchQBitBadCreds 账号密码错：登录失败整体报错。
func TestFetchQBitBadCreds(t *testing.T) {
	hits := &atomic.Int64{}
	srv := newQBitMock(t, hits)
	in := store.Integration{Type: store.IntegrationQBit, URL: srv.URL,
		Auth: MarshalCredentials(Credentials{Username: "x", Password: "y"})}
	if _, err := fetchQBit(in); err == nil || !strings.Contains(err.Error(), "登录失败") {
		t.Fatalf("应报登录失败: %v", err)
	}
}

// TestFetchEmby Items/Counts + Sessions：X-Emby-Token 头、计数、正在播放进度与封顶。
func TestFetchEmby(t *testing.T) {
	var gotKey string
	mux := http.NewServeMux()
	mux.HandleFunc("/emby/Items/Counts", func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Emby-Token")
		_, _ = w.Write([]byte(`{"MovieCount":699,"SeriesCount":695,"EpisodeCount":21696,"SongCount":0}`))
	})
	mux.HandleFunc("/emby/Sessions", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"UserName":"alice","Client":"Emby Web","DeviceName":"Chrome","DeviceId":"dev1",
			 "PlayState":{"PositionTicks":1500000000},
			 "NowPlayingItem":{"Id":"100","Type":"Episode","Name":"第三集","SeriesName":"好剧","ParentIndexNumber":1,"IndexNumber":3,"RunTimeTicks":3000000000}},
			{"UserName":"bob","Client":"Plezy%20Android%20TV","DeviceName":"SONY TV","DeviceId":"dev2",
			 "NowPlayingItem":{"Id":"200","Type":"Movie","Name":"电影X","RunTimeTicks":0}},
			{"UserName":"idle"}
		]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	in := store.Integration{Type: store.IntegrationEmby, URL: srv.URL,
		Auth: MarshalCredentials(Credentials{Token: "embykey"})}
	data, err := fetchEmby(in)
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "embykey" {
		t.Fatalf("X-Emby-Token 头未携带: %q", gotKey)
	}
	if data["movies"].(int64) != 699 || data["episodes"].(int64) != 21696 {
		t.Fatalf("计数不一致: %v", data)
	}
	streams := data["streams"].([]any)
	if len(streams) != 2 {
		t.Fatalf("应 2 条正在播放（idle 无 NowPlayingItem 剔除）: %v", streams)
	}
	first := streams[0].(map[string]any)
	if first["title"] != "第三集" || first["sub"] != "好剧" || first["user"] != "alice" {
		t.Fatalf("正在播放字段不一致: %v", first)
	}
	if pct := first["pct"].(float64); pct < 49.9 || pct > 50.1 {
		t.Fatalf("进度应 50%%: %v", pct)
	}
	// plays（528 播放记录差分观测源）：字段与差分键；PlayState 会话级平级形态（537）；
	// Plezy 客户端上报的 URL 编码名解码展示
	plays := data["plays"].([]EmbyPlay)
	if len(plays) != 2 || plays[0].Key != "dev1|100" || plays[0].Device != "Emby Web · Chrome" ||
		plays[0].Type != "Episode" || plays[0].Pos != 1500000000 || plays[0].Runtime != 3000000000 {
		t.Fatalf("plays 字段不一致: %+v", plays)
	}
	// 季集号（70）：ParentIndexNumber/IndexNumber 透传到 plays（电影缺省为 0）
	if plays[0].Season != 1 || plays[0].Episode != 3 || plays[1].Season != 0 || plays[1].Episode != 0 {
		t.Fatalf("plays 季集号不一致: %+v", plays)
	}
	if plays[1].Device != "Plezy Android TV · SONY TV" {
		t.Fatalf("设备名应解码百分号编码: %+v", plays[1])
	}
	// now_playing 只控展示：关掉后 streams 不下发、plays（记录源）仍在
	in2 := in
	in2.Config = `{"now_playing":false}`
	data2, err := fetchEmby(in2)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data2["streams"]; ok {
		t.Fatalf("now_playing=false 不应下发 streams: %v", data2)
	}
	if plays2 := data2["plays"].([]EmbyPlay); len(plays2) != 2 {
		t.Fatalf("now_playing=false plays 仍应下发（记录恒开）: %+v", plays2)
	}
}

// TestFetchHA 全量 states 拉回按实体过滤：友好名/单位回落、缺失实体 "-"。
func TestFetchHA(t *testing.T) {
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/states", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[
			{"entity_id":"sensor.temp","state":"24.5","attributes":{"friendly_name":"客厅温度","unit_of_measurement":"°C"}},
			{"entity_id":"binary_sensor.door","state":"on","attributes":{"friendly_name":"门"}}
		]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg, _ := json.Marshal(HAConfig{Entities: []HAEntity{
		{Label: "", Entity: "sensor.temp", Unit: ""},
		{Label: "入户门", Entity: "binary_sensor.door"},
		{Label: "不存在的", Entity: "sensor.missing"},
	}})
	in := store.Integration{Type: store.IntegrationHA, URL: srv.URL,
		Auth: MarshalCredentials(Credentials{Token: "haToken"}), Config: string(cfg)}
	data, err := fetchHA(in)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer haToken" {
		t.Fatalf("Bearer 头未携带: %q", gotAuth)
	}
	items := data["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("应 3 行: %v", items)
	}
	r0 := items[0].(map[string]any)
	if r0["label"] != "客厅温度" || r0["value"] != "24.5" || r0["unit"] != "°C" {
		t.Fatalf("实体 0 不一致: %v", r0)
	}
	r1 := items[1].(map[string]any)
	if r1["label"] != "入户门" || r1["value"] != "on" || r1["unit"] != "" {
		t.Fatalf("实体 1 不一致: %v", r1)
	}
	r2 := items[2].(map[string]any)
	if r2["value"] != "-" || r2["label"] != "不存在的" {
		t.Fatalf("缺失实体应 - 值并保自定义名: %v", r2)
	}
}

// TestManagerCache 管理器：All() 拉取后 30s 内走缓存（上游命中数不增）；
// Drop 后重新抓取；停用集成不出数据。
func TestManagerCache(t *testing.T) {
	hits := &atomic.Int64{}
	srv := newQBitMock(t, hits)
	s := testStore(t)
	id, err := s.CreateIntegration(store.IntegrationQBit, "PT", srv.URL,
		MarshalCredentials(Credentials{Username: "admin", Password: "pw"}), "{}")
	if err != nil {
		t.Fatal(err)
	}

	m := New(s)
	snaps := m.All()
	if len(snaps) != 1 || !snaps[0].OK {
		t.Fatalf("首次 All 应同步补拉成功: %+v", snaps)
	}
	if snaps[0].Data["leech"].(int) != 2 {
		t.Fatalf("数据不一致: %+v", snaps[0])
	}
	after := hits.Load()
	for i := 0; i < 5; i++ {
		m.All() // 缓存新鲜，不再打上游
	}
	if hits.Load() != after {
		t.Fatalf("缓存期内不应再打上游: %d → %d", after, hits.Load())
	}

	// Drop 后重抓
	m.Drop(id)
	m.All()
	if hits.Load() <= after {
		t.Fatalf("Drop 后应重抓")
	}

	// 健康态已回写库
	in, _ := s.GetIntegration(id)
	if in.LastOKAt == 0 || in.LastError != "" {
		t.Fatalf("健康态未回写: %+v", in)
	}

	// 停用：All 仍返回元信息但无 Data、OK=false
	if err := s.UpdateIntegration(id, "PT", srv.URL, "", "{}", false); err != nil {
		t.Fatal(err)
	}
	snaps = m.All()
	if snaps[0].Enabled || snaps[0].OK || snaps[0].Data != nil {
		t.Fatalf("停用集成不应出数据: %+v", snaps[0])
	}
}

// TestManagerOpenURL 主页卡片打开地址（464）：config JSON open_url 键经 All() 透出，
// 清空/坏 config 回落空（前端回落服务地址）。
func TestManagerOpenURL(t *testing.T) {
	s := testStore(t)
	// config 带 open_url：All 透出；不带：空（卡片回落服务地址）
	id, err := s.CreateIntegration(store.IntegrationQBit, "PT", "http://127.0.0.1:1",
		MarshalCredentials(Credentials{Username: "a", Password: "b"}), `{"insecure":true,"open_url":"https://pt.example.com"}`)
	if err != nil {
		t.Fatal(err)
	}
	m := New(s)
	snaps := m.All()
	if len(snaps) != 1 || snaps[0].OpenURL != "https://pt.example.com" {
		t.Fatalf("open_url 应透出: %+v", snaps)
	}
	if err := s.UpdateIntegration(id, "PT", "http://127.0.0.1:1", "", `{"insecure":true}`, false); err != nil {
		t.Fatal(err)
	}
	snaps = m.All()
	if len(snaps) != 1 || snaps[0].OpenURL != "" {
		t.Fatalf("清空 open_url 应回落空: %+v", snaps)
	}
	if v := configOpenURL("not-json"); v != "" {
		t.Fatalf("坏 config 应返回空: %q", v)
	}
}

// TestTestInput 管理弹窗连通性测试：成功/失败两路。
func TestTestInput(t *testing.T) {
	hits := &atomic.Int64{}
	srv := newQBitMock(t, hits)
	if err := TestInput(store.IntegrationQBit, srv.URL,
		MarshalCredentials(Credentials{Username: "admin", Password: "pw"}), "{}"); err != nil {
		t.Fatalf("应成功: %v", err)
	}
	if err := TestInput(store.IntegrationQBit, srv.URL,
		MarshalCredentials(Credentials{Username: "bad", Password: "bad"}), "{}"); err == nil {
		t.Fatal("坏凭据应失败")
	}
}

// TestRunctx Run 随 ctx 退出（不卡死）。
func TestRunCtx(t *testing.T) {
	hits := &atomic.Int64{}
	srv := newQBitMock(t, hits)
	s := testStore(t)
	if _, err := s.CreateIntegration(store.IntegrationQBit, "PT", srv.URL,
		MarshalCredentials(Credentials{Username: "admin", Password: "pw"}), "{}"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		New(s).Run(ctx)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run 未随 ctx 退出")
	}
}
