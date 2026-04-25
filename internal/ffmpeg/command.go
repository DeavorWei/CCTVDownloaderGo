package ffmpeg

import (
	"fmt"
	"strings"
)

// CmdArg 命令参数
type CmdArg struct {
	IsFlag      bool
	Key         string
	Value       string
	IsPositional bool
}

// CommandBuilder FFmpeg命令构建器
type CommandBuilder struct {
	executable string
	args       []*CmdArg
}

// NewCommandBuilder 创建命令构建器
func NewCommandBuilder(executable string) *CommandBuilder {
	return &CommandBuilder{
		executable: executable,
		args:       make([]*CmdArg, 0),
	}
}

// Flag 添加标志参数（如 -y, -n）
func (b *CommandBuilder) Flag(key string) *CommandBuilder {
	b.args = append(b.args, &CmdArg{
		IsFlag: true,
		Key:    key,
	})
	return b
}

// Opt 添加键值参数（如 -i input.mp4）
func (b *CommandBuilder) Opt(key, value string) *CommandBuilder {
	b.args = append(b.args, &CmdArg{
		IsFlag: false,
		Key:    key,
		Value:  value,
	})
	return b
}

// Positional 添加位置参数
func (b *CommandBuilder) Positional(value string) *CommandBuilder {
	b.args = append(b.args, &CmdArg{
		IsPositional: true,
		Value:        value,
	})
	return b
}

// Build 构建命令参数列表
func (b *CommandBuilder) Build() []string {
	args := []string{b.executable}

	for _, arg := range b.args {
		if arg.IsPositional {
			args = append(args, arg.Value)
		} else if arg.IsFlag {
			args = append(args, "-"+arg.Key)
		} else {
			args = append(args, "-"+arg.Key, arg.Value)
		}
	}

	return args
}

// BuildString 构建命令字符串（用于日志）
func (b *CommandBuilder) BuildString() string {
	return strings.Join(b.Build(), " ")
}

// BuildDemuxVideoCommand 构建视频解复用命令
// -map 0:v:0 -an -c copy：精确提取视频流，忽略音频，直接复制
func BuildDemuxVideoCommand(ffmpegPath, input, videoOut string) []string {
	return NewCommandBuilder(ffmpegPath).
		Flag("y").
		Opt("i", input).
		Opt("map", "0:v:0"). // 精确映射视频流，避免包含数据流/字幕流
		Flag("an").           // 忽略音频
		Opt("c", "copy").     // 直接复制
		Positional(videoOut).
		Build()
}

// BuildDemuxAudioCommand 构建音频解复用命令
// -map 0:a:0 -vn -c copy：精确提取音频流，忽略视频，直接复制
func BuildDemuxAudioCommand(ffmpegPath, input, audioOut string) []string {
	return NewCommandBuilder(ffmpegPath).
		Flag("y").
		Opt("i", input).
		Opt("map", "0:a:0"). // 精确映射音频流，避免包含数据流/字幕流
		Flag("vn").           // 忽略视频
		Opt("c", "copy").     // 直接复制
		Positional(audioOut).
		Build()
}

// BuildConcatCommand 构建concat合并命令（旧版，保留向后兼容）
// 两个输入都使用concat格式，-map 0 -map 1 -c copy：合并视频和音频流，直接复制
func BuildConcatCommand(ffmpegPath, videoList, audioList, output string) []string {
	return NewCommandBuilder(ffmpegPath).
		Flag("y").
		Opt("f", "concat").
		Opt("safe", "0").
		Opt("i", videoList).
		Opt("f", "concat").
		Opt("safe", "0").
		Opt("i", audioList).
		Opt("map", "0").
		Opt("map", "1").
		Opt("c", "copy").
		Positional(output).
		Build()
}

// BuildRemuxSegmentCommand 构建单段remux命令
// 将解密后的H264与AAC封装为MP4，重新生成PTS时间戳
func BuildRemuxSegmentCommand(ffmpegPath, h264Input, aacInput, output string) []string {
	return NewCommandBuilder(ffmpegPath).
		Flag("y").
		Opt("fflags", "+genpts").    // 关键：重新生成PTS
		Opt("f", "h264").            // 指定输入格式为raw H264码流
		Opt("i", h264Input).
		Opt("i", aacInput).
		Opt("map", "0:v:0").         // 精确映射视频流
		Opt("map", "1:a:0").         // 精确映射音频流
		Opt("c:v", "copy").          // 视频直接复制不重编码
		Opt("c:a", "aac").           // 音频重编码，自动修剪对齐视频长度
		Opt("movflags", "+faststart"). // moov atom前置，Web友好
		Positional(output).
		Build()
}

// BuildMergeCCTVTsCommand 构建CCTV加密TS合并命令
// 合并所有分片MP4，首选-c copy避免重编码
func BuildMergeCCTVTsCommand(ffmpegPath, listFile, output string) []string {
	return NewCommandBuilder(ffmpegPath).
		Flag("y").
		Opt("fflags", "+genpts").          // 重新生成PTS时间戳
		Opt("f", "concat").
		Opt("safe", "0").
		Opt("i", listFile).
		Opt("avoid_negative_ts", "make_zero"). // 修正负时间戳偏移
		Opt("map", "0:v:0").              // 映射视频流
		Opt("map", "0:a?").               // 映射可选音频流
		Opt("c:v", "copy").               // 首选Stream Copy
		Opt("c:a", "copy").               // 首选Stream Copy
		Opt("movflags", "+faststart").    // moov atom前置
		Positional(output).
		Build()
}

// BuildDirectDownloadCommand 构建直接下载命令
// 普通流直接下载
func BuildDirectDownloadCommand(ffmpegPath, m3u8URL, output string) []string {
	return NewCommandBuilder(ffmpegPath).
		Flag("y").
		Opt("i", m3u8URL).
		Opt("c", "copy").
		Opt("bsf:a", "aac_adtstoasc"). // AAC格式转换
		Positional(output).
		Build()
}

// BuildConcatListFile 生成concat列表文件内容
func BuildConcatListFile(files []string) string {
	var sb strings.Builder
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("file '%s'\n", f))
	}
	return sb.String()
}
