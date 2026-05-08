// Package tz 集中處理時區。
//
// 引入 _ "time/tzdata" 把 IANA 時區資料庫打進 binary，否則在 distroless
// 等沒有 /usr/share/zoneinfo 的精簡映像中 time.LoadLocation 會找不到資料。
// 整個專案統一以台灣當地時間 (Asia/Taipei) 顯示時間。
package tz

import (
	"log"
	"time"
	_ "time/tzdata" // 把 IANA tzdata 嵌入 binary
)

// Taipei 是 Asia/Taipei 時區。在套件初始化時載入一次，後續直接重用。
var Taipei = mustLoad("Asia/Taipei")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// 理論上嵌入 tzdata 後不會失敗；萬一失敗仍要能跑，退回 UTC。
		log.Printf("tz: load %s failed (%v), 改用 UTC", name, err)
		return time.UTC
	}
	return loc
}

// Now 回傳當前的台灣當地時間。
func Now() time.Time { return time.Now().In(Taipei) }

// In 把任意 Time 轉換到台灣時區。
func In(t time.Time) time.Time { return t.In(Taipei) }
