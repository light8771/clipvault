package clipvault

import (
	"net/http"
	"os"
	"time"
)

// handleSystemInfo 返回设置首页运行信息区域所需的只读全局状态：
// 版本、运行时长、记录总数、活动数据库体积、数据目录、录制状态
// 以及"联系支持"元信息（维护页只读展示）。
func (s *SearchServer) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]any{
		"version":   AppVersion,
		"dataDir":   s.env.DataDir,
		"dbBytes":   activeDatabaseSize(s.env.DataDir),
		"paused":    false,
		"uptimeSec": int64(0),
		"support":   currentApplicationMetadata().Support,
	}
	if s.state != nil {
		info["paused"] = s.state.IsPaused()
	}
	if !s.startedAt.IsZero() {
		info["uptimeSec"] = int64(time.Since(s.startedAt).Seconds())
	}
	if s.store != nil {
		if n, err := s.store.CountAll(r.Context()); err == nil {
			info["recordCount"] = n
		}
	}
	s.writeJSON(w, info)
}

// activeDatabaseSize 汇总活动数据库主文件与 WAL 的体积。
func activeDatabaseSize(dataDir string) int64 {
	var total int64
	for _, name := range []string{databaseFileName, databaseFileName + ".wal"} {
		if st, err := os.Stat(activeDatabaseFile(dataDir, name)); err == nil {
			total += st.Size()
		}
	}
	return total
}
