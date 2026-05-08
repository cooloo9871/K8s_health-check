// agent/disk 在 DaemonSet 端負責收集節點上各掛點的容量資訊。
//
// 由於 agent 在容器內執行，host 的根目錄會以 hostPath 掛入容器的
// hostPrefix (預設 "/host")，所有 statfs 的對象都需要前綴。
package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"k8s-health-check/internal/model"
)

// 這份清單刻意收得保守: 都是 K8s 節點上會「直接影響 kubelet / runtime
// 健康」的關鍵掛點。其他掛點 (NFS、user data) 不在這份清單以避免雜訊。
var defaultMountPoints = []string{
	"/",
	"/var",
	"/var/log",
	"/var/lib/kubelet",
	"/var/lib/containerd",
	"/var/lib/docker",
	"/var/lib/etcd",
	"/run/containerd",
}

// CollectDisks 走訪節點上的關鍵掛點，回傳每個掛點的使用率。
// 找不到該掛點 (例如該節點是 worker 沒有 etcd 目錄) 會自動略過，不視為錯誤。
func CollectDisks(hostPrefix string) []model.DiskInfo {
	seen := map[string]bool{} // 避免同一個底層裝置被重複列出
	out := make([]model.DiskInfo, 0, len(defaultMountPoints))
	for _, mp := range defaultMountPoints {
		full := filepath.Join(hostPrefix, mp)
		st, err := statfs(full)
		if err != nil {
			continue
		}
		// 用 (Bsize * Blocks) 作為去重鍵: 同一裝置不論掛幾次數值會一樣
		key := fmt.Sprintf("%d-%d", st.Total, st.Avail)
		if seen[key] {
			continue
		}
		seen[key] = true
		st.MountPoint = mp // 對外回報節點視角的路徑，不要含 /host 前綴
		st.Filesystem = detectFilesystem(full)
		out = append(out, st)
	}
	return out
}

// statfs 以 syscall.Statfs 取得使用率，並回傳填好百分比與狀態的 DiskInfo。
func statfs(path string) (model.DiskInfo, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(path, &s); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return model.DiskInfo{}, err
		}
		return model.DiskInfo{}, fmt.Errorf("statfs %s: %w", path, err)
	}
	bsize := uint64(s.Bsize)
	total := s.Blocks * bsize
	avail := s.Bavail * bsize
	used := total - (s.Bfree * bsize)
	if total == 0 {
		return model.DiskInfo{}, fmt.Errorf("statfs %s: zero total", path)
	}
	pct := float64(used) / float64(total) * 100
	return model.DiskInfo{
		MountPoint: path,
		Total:      total,
		Used:       used,
		Avail:      avail,
		Percent:    pct,
		Status:     diskStatus(pct),
	}, nil
}

// diskStatus 用一組保守的閾值幫每個掛點上色: 90% 以上 CRITICAL，
// 80~89% WARN，其他 OK。閾值是節點視角的「準會出事」指標。
func diskStatus(pct float64) string {
	switch {
	case pct >= 90:
		return "CRITICAL"
	case pct >= 80:
		return "WARN"
	default:
		return "OK"
	}
}

// detectFilesystem 從 /proc/mounts 反查掛點對應的檔案系統類型。
// 找不到會回 "" — 純資訊欄位，缺失不影響其他邏輯。
func detectFilesystem(absPath string) string {
	// 優先讀 /host/proc/mounts (DaemonSet 容器內 hostPath 來的)，
	// 退回 /proc/mounts (容器自己的)。
	for _, p := range []string{"/host/proc/mounts", "/proc/mounts"} {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		best := ""
		bestLen := -1
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			mp, fs := fields[1], fields[2]
			if strings.HasPrefix(absPath, mp) && len(mp) > bestLen {
				best, bestLen = fs, len(mp)
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}
