package utils

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// DependencyError 依赖错误
type DependencyError struct {
	Name    string
	Message string
}

func (e *DependencyError) Error() string {
	return fmt.Sprintf("%s: %s", e.Name, e.Message)
}

// Dependency 依赖项信息
type Dependency struct {
	Name        string
	DisplayName string
	Required    bool
	Path        string
	Version     string
}

// CheckDependencies 检查外部依赖
func CheckDependencies(ffmpegPath, nodePath string) ([]*Dependency, error) {
	deps := []*Dependency{
		{Name: "ffmpeg", DisplayName: "FFmpeg", Required: true, Path: ffmpegPath},
		{Name: "node", DisplayName: "Node.js", Required: false, Path: nodePath}, // 仅加密流需要
	}

	var missingRequired error

	for _, dep := range deps {
		path, err := exec.LookPath(dep.Path)
		if err != nil {
			dep.Path = ""
			if dep.Required {
				missingRequired = &DependencyError{
					Name:    dep.DisplayName,
					Message: "未找到，请安装后重试",
				}
			}
			continue
		}
		dep.Path = path

		// 获取版本信息
		version, err := getDependencyVersion(dep.Name, path)
		if err == nil {
			dep.Version = version
		}
	}

	return deps, missingRequired
}

// getDependencyVersion 获取依赖版本
func getDependencyVersion(name, path string) (string, error) {
	var cmd *exec.Cmd
	switch name {
	case "ffmpeg":
		cmd = exec.Command(path, "-version")
	case "node":
		cmd = exec.Command(path, "-v")
	default:
		return "", errors.New("unknown dependency")
	}

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// 解析版本号
	return parseVersion(name, string(output)), nil
}

// parseVersion 从输出中解析版本号
func parseVersion(name, output string) string {
	switch name {
	case "ffmpeg":
		// ffmpeg version 6.0 ...
		re := regexp.MustCompile(`ffmpeg version\s+([^\s]+)`)
		if match := re.FindStringSubmatch(output); len(match) > 1 {
			return match[1]
		}
	case "node":
		// v20.10.0
		return strings.TrimSpace(output)
	}
	return ""
}

// CheckFFmpeg 检查FFmpeg是否可用
func CheckFFmpeg(ffmpegPath string) error {
	path, err := exec.LookPath(ffmpegPath)
	if err != nil {
		return &DependencyError{
			Name:    "FFmpeg",
			Message: "未找到，请安装后重试",
		}
	}
	_ = path
	return nil
}

// CheckNodeJS 检查Node.js是否可用
func CheckNodeJS(nodePath string) error {
	path, err := exec.LookPath(nodePath)
	if err != nil {
		return &DependencyError{
			Name:    "Node.js",
			Message: "未找到，加密视频需要Node.js，请安装后重试",
		}
	}
	_ = path
	return nil
}
