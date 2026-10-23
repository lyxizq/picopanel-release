package proto

// 视频下载任务线格式（TODO 4，追剧页「视频下载」tab）：server 入队 → push
// agent 上报响应捎带领取（executor=agent）或 server 本机执行器领取
//（executor=server）→ 执行机器跑 yt-dlp 落盘 mp4 → 结果回传。队列纪律见
// store/video_dl.go（长租约，视频分钟级到小时级）。

// VideoDlTask agent 领取到的视频下载任务（payload JSON）。
type VideoDlTask struct {
	TaskID  int64  `json:"task_id"`
	URL     string `json:"url"`
	SaveDir string `json:"save_dir"`
	Cookies string `json:"cookies,omitempty"` // Netscape 格式 Cookie 文件内容（可空；B 站高清等需登录的场景）
}

// VideoDlProgress 下载进度心跳（agent 回传；server 本机执行直接写库不过此通道）。
type VideoDlProgress struct {
	TaskID  int64   `json:"task_id"`
	Percent float64 `json:"percent"`           // 0-100
	Speed   string  `json:"speed,omitempty"`   // 如 "2.34MiB/s"
	Title   string  `json:"title,omitempty"`   // yt-dlp 解析出的标题（首次携带后续可空）
}

// VideoDlTaskResult agent 回传的任务结果（result 列里的 JSON）。
type VideoDlTaskResult struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	FileName string `json:"file_name,omitempty"` // 最终落盘路径（yt-dlp after_move 回传）
	FileSize int64  `json:"file_size,omitempty"` // 字节
	Title    string `json:"title,omitempty"`
}
