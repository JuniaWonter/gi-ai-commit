package debug

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func Enabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("GIT_AI_DEBUG")))
	if v == "1" || v == "true" || v == "yes" || v == "on" {
		return true
	}
	v = strings.TrimSpace(strings.ToLower(os.Getenv("GI_DEBUG")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func FDCount() int {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

func Logf(format string, args ...any) {
	if !Enabled() {
		return
	}
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(os.Stderr, "[git-ai-debug %s fd=%d] %s\n", time.Now().Format("15:04:05.000"), FDCount(), msg)
}
