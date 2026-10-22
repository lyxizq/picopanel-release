// 436-441 扩容 12 类集成的单测：全部 httptest 假上游（Docker 走项目内
// .tmp 短路径 unix socket），覆盖鉴权头/会话握手/回落路径/字段解析。
package integrations

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"lz-monitor/internal/server/store"
)

func cred(u, p, tok string) string {
	return MarshalCredentials(Credentials{Username: u, Password: p, Token: tok})
}

func mustFetch(t *testing.T, typ string, in store.Integration) map[string]any {
	t.Helper()
	in.Type = typ
	p := fetchWithError(in)
	if !p.OK {
		t.Fatalf("%s 抓取失败: %s", typ, p.Error)
	}
	return p.Data
}

func wantInt(t *testing.T, d map[string]any, key string, want int64) {
	t.Helper()
	got, ok := d[key].(int64)
	if !ok || got != want {
		t.Fatalf("%s = %v(%T)，期望 %d", key, d[key], d[key], want)
	}
}

// ---- AdGuard ----

func TestFetchAdGuard(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/control/stats", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "admin" || p != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"num_dns_queries":1000,"num_blocked_filtering":250,"avg_processing_time":0.0123}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationAdGuard, store.Integration{URL: srv.URL, Auth: cred("admin", "secret", "")})
	wantInt(t, d, "queries", 1000)
	wantInt(t, d, "blocked", 250)
	if pct, _ := d["blocked_pct"].(float64); pct != 25 {
		t.Fatalf("拦截率 = %v，期望 25", d["blocked_pct"])
	}
	if ms, _ := d["avg_ms"].(float64); ms < 12.29 || ms > 12.31 {
		t.Fatalf("平均延迟 = %v，期望 ≈12.3ms", d["avg_ms"])
	}
}

// ---- Pi-hole：v6 优先 + v5 回落 ----

func TestFetchPiholeV6(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["password"] != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"session":{"sid":"S1","valid":true}}`))
	})
	mux.HandleFunc("/api/stats/summary", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-FTL-SID") != "S1" || r.Header.Get("Authorization") != "Bearer S1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"queries":{"total":800,"valid":600,"blocked":200},"gravity":{"domains_being_blocked":120000}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationPihole, store.Integration{URL: srv.URL, Auth: cred("", "", "pw")})
	wantInt(t, d, "queries", 800)
	wantInt(t, d, "blocked", 200)
	wantInt(t, d, "gravity", 120000)
	if pct, _ := d["blocked_pct"].(float64); pct != 25 {
		t.Fatalf("拦截率 = %v，期望 25", d["blocked_pct"])
	}
}

func TestFetchPiholeV5Fallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // v5 不认识 v6 端点
	})
	mux.HandleFunc("/api.php", func(w http.ResponseWriter, r *http.Request) {
		auth := r.URL.Query().Get("auth")
		// auth = sha256(sha256("pw"))（v5 webpassword 口径）
		if auth != sha256Hex(sha256Hex("pw")) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"dns_queries_today":"800","ads_blocked_today":"200","ads_percentage_today":"25.0","domains_being_blocked":"120000"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationPihole, store.Integration{URL: srv.URL, Auth: cred("", "", "pw")})
	wantInt(t, d, "queries", 800)
	wantInt(t, d, "blocked", 200)
	wantInt(t, d, "gravity", 120000)
}

// ---- Transmission：409 会话握手 + session-stats ----

func TestFetchTransmission(t *testing.T) {
	sid := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "adm" || p != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Transmission-Session-Id") != sid {
			sid = "TR-SID"
			w.Header().Set("X-Transmission-Session-Id", sid)
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"activeTorrentCount":3,"pausedTorrentCount":5,"torrentCount":8,"downloadSpeed":1048576,"uploadSpeed":2048}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationTransmission, store.Integration{URL: srv.URL, Auth: cred("adm", "pw", "")})
	wantInt(t, d, "active", 3)
	wantInt(t, d, "paused", 5)
	wantInt(t, d, "download_bps", 1048576)
	wantInt(t, d, "upload_bps", 2048)
}

// ---- Deluge：auth.login 会话 + web.update_ui 计数 ----

func TestFetchDeluge(t *testing.T) {
	logged := false
	mux := http.NewServeMux()
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "auth.login":
			if req.Params[0] == "pw" {
				logged = true
				http.SetCookie(w, &http.Cookie{Name: "deluge_base", Value: "D1", Path: "/"})
				_, _ = w.Write([]byte(`{"id":1,"result":true,"error":null}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":1,"result":false,"error":null}`))
		case "web.update_ui":
			if !logged {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"id":2,"result":{"torrents":{
				"a":{"state":"Downloading","download_payload_rate":1024,"upload_payload_rate":0},
				"b":{"state":"Seeding","download_payload_rate":0,"upload_payload_rate":512},
				"c":{"state":"Seeding","download_payload_rate":0,"upload_payload_rate":512},
				"d":{"state":"Paused","download_payload_rate":0,"upload_payload_rate":0}
			}},"error":null}`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationDeluge, store.Integration{URL: srv.URL, Auth: cred("", "pw", "")})
	wantInt(t, d, "leech", 1)
	wantInt(t, d, "seed", 2)
	wantInt(t, d, "paused", 1)
	wantInt(t, d, "download_bps", 1024)
	wantInt(t, d, "upload_bps", 1024)
}

// ---- aria2：/jsonrpc 404 回落 /rpc + token 认证 ----

func TestFetchAria2Fallback(t *testing.T) {
	mux := http.NewServeMux()
	handle := func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params []any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Params) != 1 || req.Params[0] != "token:SEC" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"picopanel","result":{"activeCount":"2","waitingCount":"1","stoppedCount":"7","downloadSpeed":"2048","uploadSpeed":"512"}}`))
	}
	mux.HandleFunc("/jsonrpc", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // 第一路径不存在 → 回落 /rpc
	})
	mux.HandleFunc("/rpc", handle)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationAria2, store.Integration{URL: srv.URL, Auth: cred("", "", "SEC")})
	wantInt(t, d, "active", 2)
	wantInt(t, d, "waiting", 1)
	wantInt(t, d, "stopped", 7)
	wantInt(t, d, "download_bps", 2048)
	wantInt(t, d, "upload_bps", 512)
}

// ---- Navidrome：ping + getArtists 聚合（songCount 缺失回落 getScanStatus）----

func TestFetchNavidromeScanFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("u") != "adm" || r.URL.Query().Get("p") != "pw" {
			_, _ = w.Write([]byte(`{"subsonic-response":{"status":"failed","error":{"message":"Wrong credentials"}}}`))
			return
		}
		switch r.URL.Path {
		case "/rest/ping":
			_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","version":"1.16.1","serverVersion":"0.55.2 (8b30f96f)"}}`))
		case "/rest/getArtists":
			// 老版本 artist 无 songCount
			_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","artists":{"index":[
				{"artist":[{"id":"1","name":"A","albumCount":2},{"id":"2","name":"B","albumCount":3}]}
			]}}}`))
		case "/rest/getScanStatus":
			_, _ = w.Write([]byte(`{"subsonic-response":{"status":"ok","scanStatus":{"scanning":false,"count":42}}}`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationNavidrome, store.Integration{URL: srv.URL, Auth: cred("adm", "pw", "")})
	wantInt(t, d, "artists", 2)
	wantInt(t, d, "albums", 5)
	wantInt(t, d, "songs", 42)
	if d["version"] != "0.55.2" {
		t.Fatalf("version = %v，期望 0.55.2", d["version"])
	}
}

// ---- Nextcloud：status.php + OCS serverinfo ----

func TestFetchNextcloud(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/status.php", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"installed":true,"version":"30.0.2.1","productname":"Nextcloud"}`))
	})
	mux.HandleFunc("/ocs/v2.php/apps/serverinfo/api/v1/serverinfo", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "adm" || p != "app-pw" || r.Header.Get("OCS-APIRequest") != "true" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ocs":{"meta":{"status":"ok"},"data":{"nextcloud":{
			"storage":{"num_users":3,"num_files":1234},"shares":{"num_shares":12}}}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationNextcloud, store.Integration{URL: srv.URL, Auth: cred("adm", "app-pw", "")})
	wantInt(t, d, "users", 3)
	wantInt(t, d, "files", 1234)
	wantInt(t, d, "shares", 12)
	if d["version"] != "30.0.2" {
		t.Fatalf("version = %v，期望 30.0.2", d["version"])
	}
}

// ---- Proxmox：token 头 + ticket 回落 ----

func TestFetchProxmoxToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=root@pam!mon=UUID-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[
			{"type":"node","status":"online"},{"type":"node","status":"online"},{"type":"node","status":"offline"},
			{"type":"qemu","status":"running"},{"type":"qemu","status":"stopped"},
			{"type":"lxc","status":"running"},{"type":"storage","status":"available"}
		]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationProxmox, store.Integration{URL: srv.URL, Auth: cred("", "", "root@pam!mon=UUID-1")})
	wantInt(t, d, "nodes_up", 2)
	wantInt(t, d, "nodes_total", 3)
	wantInt(t, d, "qemu_running", 1)
	wantInt(t, d, "lxc_running", 1)
	wantInt(t, d, "vm_running", 2)
}

func TestFetchProxmoxTicket(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostFormValue("username") == "root@pam" && r.PostFormValue("password") == "pw" {
			_, _ = w.Write([]byte(`{"data":{"ticket":"PVE:T1","CSRFPreventionToken":"CSRF:X"}}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/api2/json/cluster/resources", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "PVEAuthCookie=PVE:T1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"type":"node","status":"online"},{"type":"qemu","status":"running"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationProxmox, store.Integration{URL: srv.URL, Auth: cred("root@pam", "pw", "")})
	wantInt(t, d, "nodes_up", 1)
	wantInt(t, d, "qemu_running", 1)
}

// ---- Docker：项目内 .tmp 短路径 unix socket ----

func TestFetchDocker(t *testing.T) {
	dir := ".tmp"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(dir, "pp-docker-test.sock")
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("unix socket 不可用: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Version":"27.3.1"}`))
	})
	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("all") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"State":"running"},{"State":"running"},{"State":"exited"}]`))
	})
	mux.HandleFunc("/images/json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"a"},{"Id":"b"}]`))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = os.Remove(sockPath) })

	d := mustFetch(t, store.IntegrationDocker, store.Integration{Config: fmt.Sprintf(`{"sock":%q}`, sockPath)})
	wantInt(t, d, "running", 2)
	wantInt(t, d, "stopped", 1)
	wantInt(t, d, "images", 2)
	if d["version"] != "27.3.1" {
		t.Fatalf("version = %v，期望 27.3.1", d["version"])
	}
}

// ---- Portainer：Snapshots 统计 + 缺快照兜底 ----

func TestFetchPortainer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/endpoints", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "PK" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`[
			{"Id":1,"Name":"local","Status":1,"Type":1,"Snapshots":[{"RunningContainerCount":4,"StoppedContainerCount":2,"StackCount":3}]},
			{"Id":2,"Name":"remote","Status":1,"Type":1,"Snapshots":[]}
		]`))
	})
	mux.HandleFunc("/api/endpoints/2/docker/containers/json", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("all") != "1" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"State":"running"},{"State":"paused"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationPortainer, store.Integration{URL: srv.URL, Auth: cred("", "", "PK")})
	wantInt(t, d, "endpoints_up", 2)
	wantInt(t, d, "endpoints_total", 2)
	wantInt(t, d, "running", 5) // 4（快照）+ 1（兜底）
	wantInt(t, d, "stopped", 3) // 2（快照）+ 1（兜底）
	wantInt(t, d, "stacks", 3)
}

// ---- DSM：登录 sid → info + utilization → logout ----

func TestFetchDSM(t *testing.T) {
	var sid string
	mux := http.NewServeMux()
	mux.HandleFunc("/webapi/entry.cgi", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm() // 登录走表单体、取数走 query，ParseForm 两头都覆盖
		q := r.Form
		w.Header().Set("Content-Type", "application/json")
		switch {
		case q.Get("method") == "login":
			if q.Get("account") == "adm" && q.Get("passwd") == "pw" {
				sid = "DSM-S1"
				_, _ = w.Write([]byte(`{"success":true,"data":{"sid":"DSM-S1"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":false}`))
		case q.Get("method") == "getinfo":
			if q.Get("_sid") != sid {
				_, _ = w.Write([]byte(`{"success":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"temperature":42,"uptime":"5 days 3 hrs 21 mins","version":"7.2.2-72806"}}`))
		case q.Get("method") == "get":
			if q.Get("_sid") != sid {
				_, _ = w.Write([]byte(`{"success":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"cpu":{"total_load":"12.5%"},"memory":{"real_usage":55.2,"total_real":1000,"used_real":552}}}`))
		case q.Get("method") == "logout":
			sid = ""
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationDSM, store.Integration{URL: srv.URL, Auth: cred("adm", "pw", "")})
	if cpu, _ := d["cpu_pct"].(float64); cpu != 12.5 {
		t.Fatalf("cpu_pct = %v，期望 12.5", d["cpu_pct"])
	}
	if mem, _ := d["mem_pct"].(float64); mem != 55.2 {
		t.Fatalf("mem_pct = %v，期望 55.2", d["mem_pct"])
	}
	wantInt(t, d, "temp_c", 42)
	wantInt(t, d, "uptime_days", 5)
	if sid != "" {
		t.Fatalf("取数后未 logout（会话泄漏）")
	}
}

// ---- TrueNAS SCALE：Bearer key + pool 健康 ----

func TestFetchTrueNAS(t *testing.T) {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer TN-KEY" {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("/api/v2.0/system/info", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = w.Write([]byte(`{"version":"TrueNAS-SCALE-24.04.2.2","uptime_seconds":432000,"loadavg":{"1min":0.5,"5min":0.4,"15min":0.3}}`))
	})
	mux.HandleFunc("/api/v2.0/pool", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = w.Write([]byte(`[{"name":"tank","status":"ONLINE"},{"name":"boot-pool","status":"ONLINE"},{"name":"bad","status":"DEGRADED"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := mustFetch(t, store.IntegrationTrueNAS, store.Integration{URL: srv.URL, Auth: cred("", "", "TN-KEY")})
	if d["version"] != "24.04.2" {
		t.Fatalf("version = %v，期望 24.04.2", d["version"])
	}
	wantInt(t, d, "pools_healthy", 2)
	wantInt(t, d, "pools_total", 3)
	wantInt(t, d, "uptime_days", 5)
	if load, _ := d["load1"].(float64); load != 0.5 {
		t.Fatalf("load1 = %v，期望 0.5", d["load1"])
	}
}

// ---- 上游故障路径（负例）----

func TestFetchExtendedNegative(t *testing.T) {
	// Deluge 密码错
	mux := http.NewServeMux()
	mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":1,"result":false,"error":null}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	in := store.Integration{Type: store.IntegrationDeluge, URL: srv.URL, Auth: cred("", "wrong", "")}
	if p := fetchWithError(in); p.OK {
		t.Fatal("Deluge 密码错误应失败")
	}

	// Pi-hole v6 密码错 → 明确报错不回落 v5
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/api/auth", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()
	if p := fetchWithError(store.Integration{Type: store.IntegrationPihole, URL: srv2.URL, Auth: cred("", "bad", "")}); p.OK {
		t.Fatal("Pi-hole 密码错误应失败")
	}

	// Docker socket 不存在
	if p := fetchWithError(store.Integration{Type: store.IntegrationDocker, Config: `{"sock":"/nonexistent/sock"}`}); p.OK {
		t.Fatal("Docker socket 缺失应失败")
	}
}
