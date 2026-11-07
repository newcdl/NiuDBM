// Package ulid 生成 26 位 ULID：前 48 位是毫秒时间戳，后 80 位随机，天然可排序。
//
// 之所以不引第三方库：只需要"可排序、无自增、26 字符"这一点能力，
// 自己实现 80 行比维护一条外部依赖更划算。
package ulid

import (
	"crypto/rand"
	"sync"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	mu       sync.Mutex
	lastMs   int64
	lastRand [10]byte
)

// New 生成当前时刻的 ULID。
func New() string { return NewAt(time.Now()) }

// NewAt 生成指定时刻的 ULID；同一毫秒内调用会自增随机部分，保证单调唯一。
func NewAt(t time.Time) string {
	mu.Lock()
	defer mu.Unlock()

	ms := t.UnixMilli()
	if ms > lastMs {
		lastMs = ms
		if _, err := rand.Read(lastRand[:]); err != nil {
			// crypto/rand 失败在正常系统上不会发生；退化为时间纳秒填充，保证不出空值
			n := uint64(t.UnixNano())
			for i := range lastRand {
				lastRand[i] = byte(n >> (8 * uint(i)))
			}
		}
	} else {
		ms = lastMs
		increment()
	}

	return encode(ms, lastRand)
}

func increment() {
	for i := len(lastRand) - 1; i >= 0; i-- {
		lastRand[i]++
		if lastRand[i] != 0 {
			return
		}
	}
}

func encode(ms int64, tail [10]byte) string {
	var raw [16]byte
	for i := 0; i < 6; i++ {
		raw[i] = byte(uint64(ms) >> (8 * uint(5-i)))
	}
	copy(raw[6:], tail[:])

	var out [26]byte
	// 128 位有效数据前补 2 位零，凑成 26 组 × 5 位
	bit := func(pos int) byte {
		p := pos - 2
		if p < 0 {
			return 0
		}
		return (raw[p/8] >> uint(7-p%8)) & 1
	}
	for i := 0; i < 26; i++ {
		var v byte
		for j := 0; j < 5; j++ {
			v = v<<1 | bit(i*5+j)
		}
		out[i] = alphabet[v]
	}
	return string(out[:])
}
