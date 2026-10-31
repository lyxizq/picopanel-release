package proto

// B 站追更任务线格式（TODO 6）：server 入队 → push agent 上报响应捎带领取 →
// agent 机器执行（解析地址/下载音频转 m4a）→ 结果回传。与 PT/中转任务同通道
// 不同表（追更长任务单独 2h 租约，见 store/bili_task.go）。

// BiliTaskMode 任务形态：probe=解析追更地址（添加/改地址时）、seed10=首抓最新
// 10 条、all=全量增量追更。
const (
	BiliTaskProbe  = "probe"
	BiliTaskSeed10 = "seed10"
	BiliTaskAll    = "all"
)

// BiliTask agent 领取到的追更任务。probe 用 UpName+Address，run 用已解析的
// Mid/Kind/ListID；FetchedBVIDs 为服务端已抓记录的 bvid 清单（TODO 4 判重
// 改服务端口径：agent 不再扫目录，只对清单外条目下载）；Cookie 为服务端配置
// 的 SESSDATA（可空=未登录音质）。
type BiliTask struct {
	TaskID       int64    `json:"task_id"`
	FollowID     int64    `json:"follow_id"`
	Mode         string   `json:"mode"`
	Mid          int64    `json:"mid"`
	Kind         string   `json:"kind"` // season | series
	ListID       int64    `json:"list_id"`
	UpName       string   `json:"up_name"`
	ListTitle    string   `json:"list_title"`
	Address      string   `json:"address"`
	SaveDir      string   `json:"save_dir"`
	SubDir       string   `json:"sub_dir"`
	Cookie       string   `json:"cookie"`
	FetchedBVIDs []string `json:"fetched_bvids,omitempty"`
	Watermark    int64    `json:"watermark,omitempty"` // 已见最新 pubdate（7：只抓比它新的，不回填旧视频；seed10 忽略）
}

// BiliTarget probe 解析结果（回填 bili_follows 行）。
type BiliTarget struct {
	Mid    int64  `json:"mid"`
	Kind   string `json:"kind"`
	ListID int64  `json:"list_id"`
	Title  string `json:"title"`
	Total  int    `json:"total"`
	UpName string `json:"up_name"`
}

// BiliItem 追更条目。Status：downloaded=本次新下载 / exists=目标文件已存在
// 跳过（写保护；回传服务端补记账，任务中断后重跑自愈不重下）。failed 为保留
// 值：当前 agent 下载失败直接跳过不回传（水位不推进，下轮自动重试），该状态
// 仅在需要人工对账/补记时手工落库。
type BiliItem struct {
	BVID     string `json:"bvid"`
	Title    string `json:"title"`
	PubDate  int64  `json:"pubdate,omitempty"`
	FileName string `json:"file_name,omitempty"`
	Status   string `json:"status,omitempty"`
}

// BiliTaskResult agent 回传的任务结果（result 列里的 JSON）。
type BiliTaskResult struct {
	OK         bool        `json:"ok"`
	Stopped    bool        `json:"stopped,omitempty"` // 用户手动停止（8：已完成部分正常记账后中止）
	Error      string      `json:"error,omitempty"`
	Total      int         `json:"total"`      // 列表内总条目
	Downloaded int         `json:"downloaded"` // 本次新增条数
	FileCount  int         `json:"file_count"` // 目录内已抓条目数
	Items      []BiliItem  `json:"items,omitempty"`
	Target     *BiliTarget `json:"target,omitempty"` // probe 专用
}
