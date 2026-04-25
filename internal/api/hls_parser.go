package api

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// HLSVariant HLS变体流信息
type HLSVariant struct {
	Bandwidth  int
	Resolution string
	Width      int
	Height     int
	URI        string
}

// CCTVHLSBestParser CCTV HLS最佳流选择器
type CCTVHLSBestParser struct{}

// NewCCTVHLSBestParser 创建HLS最佳流选择器
func NewCCTVHLSBestParser() *CCTVHLSBestParser {
	return &CCTVHLSBestParser{}
}

// Best 从M3U8内容中选择最佳流
// 评分公式：分辨率面积 × 带宽
func (p *CCTVHLSBestParser) Best(m3u8Content string) (string, error) {
	variants := p.parseMasterPlaylist(m3u8Content)
	if len(variants) == 0 {
		return "", fmt.Errorf("未找到可用的HLS变体流")
	}

	// 选择得分最高的变体
	best := variants[0]
	bestScore := p.calculateScore(best)

	for _, v := range variants[1:] {
		score := p.calculateScore(v)
		if score > bestScore {
			best = v
			bestScore = score
		}
	}

	return best.URI, nil
}

// calculateScore 计算变体流得分
func (p *CCTVHLSBestParser) calculateScore(v *HLSVariant) int {
	area := v.Width * v.Height
	if area == 0 {
		area = 1 // 默认面积
	}
	return area * v.Bandwidth
}

// parseMasterPlaylist 解析主播放列表
func (p *CCTVHLSBestParser) parseMasterPlaylist(content string) []*HLSVariant {
	var variants []*HLSVariant
	var currentVariant *HLSVariant

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			// 解析变体流信息
			currentVariant = &HLSVariant{}
			p.parseStreamInf(line, currentVariant)
		} else if !strings.HasPrefix(line, "#") && line != "" && currentVariant != nil {
			// URI行
			currentVariant.URI = line
			variants = append(variants, currentVariant)
			currentVariant = nil
		}
	}

	return variants
}

// parseStreamInf 解析#EXT-X-STREAM-INF行
func (p *CCTVHLSBestParser) parseStreamInf(line string, v *HLSVariant) {
	// 移除前缀
	line = strings.TrimPrefix(line, "#EXT-X-STREAM-INF:")

	// 解析属性
	attrs := p.parseAttributes(line)

	if bandwidth, ok := attrs["BANDWIDTH"]; ok {
		v.Bandwidth, _ = strconv.Atoi(bandwidth)
	}

	if resolution, ok := attrs["RESOLUTION"]; ok {
		v.Resolution = resolution
		// 解析宽高
		parts := strings.Split(resolution, "x")
		if len(parts) == 2 {
			v.Width, _ = strconv.Atoi(parts[0])
			v.Height, _ = strconv.Atoi(parts[1])
		}
	}
}

// parseAttributes 解析属性键值对
func (p *CCTVHLSBestParser) parseAttributes(line string) map[string]string {
	attrs := make(map[string]string)

	// 使用正则匹配 KEY=VALUE 格式
	re := regexp.MustCompile(`([A-Z-]+)=("[^"]*"|[^,]+)`)
	matches := re.FindAllStringSubmatch(line, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			key := match[1]
			value := match[2]
			// 移除引号
			value = strings.Trim(value, `"`)
			attrs[key] = value
		}
	}

	return attrs
}
