package api

import (
	"encoding/json"
	"fmt"
)

// parseJSONResponse 解析JSON响应
func parseJSONResponse(body []byte, v any) error {
	if len(body) == 0 {
		return fmt.Errorf("空响应")
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("JSON解析失败: %w", err)
	}
	return nil
}
