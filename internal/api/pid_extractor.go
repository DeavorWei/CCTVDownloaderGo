package api

import (
	"errors"
	"regexp"
)

// pidPatterns PID提取正则规则（6条，对齐videodl-master cctv.py:39-43）
var pidPatterns = []string{
	`var\s+guid\s*=\s*["']([0-9a-fA-F]+)`,
	`videoCenterId(?:["']\s*,|:)\s*["']([0-9a-fA-F]+)`,
	`changePlayer\s*\(\s*["']([0-9a-fA-F]+)`,
	`load[Vv]ideo\s*\(\s*["']([0-9a-fA-F]+)`,
	`var\s+initMyAray\s*=\s*["']([0-9a-fA-F]+)`,
	`var\s+ids\s*=\s*\[["']([0-9a-fA-F]+)`,
}

// compiledPatterns 预编译正则
var compiledPatterns []*regexp.Regexp

func init() {
	for _, pattern := range pidPatterns {
		re := regexp.MustCompile(pattern)
		compiledPatterns = append(compiledPatterns, re)
	}
}

// ExtractPID 从HTML内容中提取PID
func ExtractPID(htmlContent string) (string, error) {
	for i, re := range compiledPatterns {
		if match := re.FindStringSubmatch(htmlContent); match != nil {
			_ = i
			return match[1], nil
		}
	}
	return "", ErrPIDNotFound
}

// ValidateGUID 验证GUID格式（32位十六进制）
func ValidateGUID(guid string) error {
	matched, err := regexp.MatchString(`^[a-fA-F0-9]{32}$`, guid)
	if err != nil {
		return err
	}
	if !matched {
		return errors.New("GUID格式不正确，应为32位十六进制字符")
	}
	return nil
}
