package claude

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

const (
	defaultRPM             = 1000
	defaultTPM             = 400000
	defaultInputTPM        = 320000
	defaultOutputTPM       = 80000
	windowCleanupInterval  = 2 * time.Minute
	windowCleanupThreshold = 5 * time.Minute
)

type rlWindow struct {
	requests     atomic.Int64
	tokens       atomic.Int64
	inputTokens  atomic.Int64
	outputTokens atomic.Int64
	minuteKey    int64
}

type rlEntry struct {
	mu      sync.Mutex
	current *rlWindow
}

var (
	rlStore   sync.Map
	rlCleanup sync.Once
)

func startCleanup() {
	rlCleanup.Do(func() {
		go func() {
			for {
				time.Sleep(windowCleanupInterval)
				cutoff := minuteKey(time.Now()) - int64(windowCleanupThreshold.Seconds())
				rlStore.Range(func(key, value any) bool {
					entry := value.(*rlEntry)
					entry.mu.Lock()
					if entry.current != nil && entry.current.minuteKey < cutoff {
						entry.current = nil
					}
					entry.mu.Unlock()
					if entry.current == nil {
						rlStore.Delete(key)
					}
					return true
				})
			}
		}()
	})
}

func minuteKey(t time.Time) int64 {
	return t.Unix() / 60
}

func getWindow(tokenID int) *rlWindow {
	startCleanup()
	now := time.Now()
	mk := minuteKey(now)

	val, _ := rlStore.LoadOrStore(tokenID, &rlEntry{})
	entry := val.(*rlEntry)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.current == nil || entry.current.minuteKey != mk {
		entry.current = &rlWindow{minuteKey: mk}
	}
	return entry.current
}

func recordClaudeUsage(tokenID int, inputTokens, outputTokens int) {
	w := getWindow(tokenID)
	w.requests.Add(1)
	total := int64(inputTokens + outputTokens)
	w.tokens.Add(total)
	w.inputTokens.Add(int64(inputTokens))
	w.outputTokens.Add(int64(outputTokens))
}

func setClaudeRatelimitHeaders(c *gin.Context, info *relaycommon.RelayInfo) {
	w := getWindow(info.TokenId)

	now := time.Now()
	nextMinute := now.Truncate(time.Minute).Add(time.Minute)
	resetStr := nextMinute.UTC().Format(time.RFC3339)

	reqUsed := w.requests.Load()
	tokUsed := w.tokens.Load()
	inTokUsed := w.inputTokens.Load()
	outTokUsed := w.outputTokens.Load()

	setRL := func(prefix string, limit int64, used int64, reset string) {
		remaining := limit - used
		if remaining < 0 {
			remaining = 0
		}
		c.Writer.Header().Set(fmt.Sprintf("anthropic-ratelimit-%s-limit", prefix), fmt.Sprintf("%d", limit))
		c.Writer.Header().Set(fmt.Sprintf("anthropic-ratelimit-%s-remaining", prefix), fmt.Sprintf("%d", remaining))
		c.Writer.Header().Set(fmt.Sprintf("anthropic-ratelimit-%s-reset", prefix), reset)
	}

	setRL("requests", defaultRPM, reqUsed, resetStr)
	setRL("tokens", defaultTPM, tokUsed, resetStr)
	setRL("input-tokens", defaultInputTPM, inTokUsed, resetStr)
	setRL("output-tokens", defaultOutputTPM, outTokUsed, resetStr)
}
