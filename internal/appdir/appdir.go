package appdir

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvHome 允许用户强制指定数据目录（便携/无权限场景很有用）。
	EnvHome = "GOBILITICKET_HOME"
)

func BaseDir() string {
	if v := strings.TrimSpace(os.Getenv(EnvHome)); v != "" {
		if dir := ensureDir(v); dir != "" {
			return dir
		}
	}

	var candidates []string

	// 首选：系统配置目录（Windows: %AppData%）
	if d, err := os.UserConfigDir(); err == nil && strings.TrimSpace(d) != "" {
		candidates = append(candidates, filepath.Join(d, "gobiliticket"))
	}

	// 兼容：历史目录（~/.gobiliticket）
	if d, err := os.UserHomeDir(); err == nil && strings.TrimSpace(d) != "" {
		candidates = append(candidates, filepath.Join(d, ".gobiliticket"))
	}

	// 便携：exe 同目录（可能只读，失败就跳过）
	if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".gobiliticket"))
	}

	// 兜底：临时目录
	candidates = append(candidates, filepath.Join(os.TempDir(), "gobiliticket"))

	for _, c := range candidates {
		if dir := ensureDir(c); dir != "" {
			return dir
		}
	}
	return "."
}

func CookiesPath() string {
	return filepath.Join(BaseDir(), "cookies.json")
}

func LegacyCookiesPath() string {
	if d, err := os.UserHomeDir(); err == nil && strings.TrimSpace(d) != "" {
		return filepath.Join(d, ".gobiliticket", "cookies.json")
	}
	return ""
}

func FindCookiesPath() string {
	primary := CookiesPath()
	if fileExists(primary) {
		return primary
	}
	legacy := LegacyCookiesPath()
	if legacy != "" && fileExists(legacy) {
		return legacy
	}
	return primary
}

func HistoryPath() string {
	return filepath.Join(BaseDir(), "history.json")
}

func LegacyHistoryPath() string {
	if d, err := os.UserHomeDir(); err == nil && strings.TrimSpace(d) != "" {
		return filepath.Join(d, ".gobiliticket", "history.json")
	}
	return ""
}

func FindHistoryPath() string {
	primary := HistoryPath()
	if fileExists(primary) {
		return primary
	}
	legacy := LegacyHistoryPath()
	if legacy != "" && fileExists(legacy) {
		return legacy
	}
	return primary
}

func LogsDir() string {
	return filepath.Join(BaseDir(), "logs")
}

func ensureDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ""
	}
	return dir
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

