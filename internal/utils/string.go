package utils

import "unicode/utf8"

// TruncateString 安全截断字符串，确保不会在多字节字符中间截断
// maxLen 为最大字符数（非字节数）
func TruncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) > maxLen {
		return string(runes[:maxLen])
	}
	return s
}

// RuneCount 返回字符串的字符数（非字节数）
func RuneCount(s string) int {
	return utf8.RuneCountInString(s)
}
