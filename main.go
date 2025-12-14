// Package main 实现了一个智能字幕处理工具。
//
// 该工具提供两大核心功能：
//  1. 基于拼音相似度的智能字幕纠错
//  2. 部分字幕内容向完整字幕文件的转移
//
// 使用示例：
//
//	# 智能修正字幕
//	ime -i input.srt output.srt
//
//	# 转移字幕内容
//	ime -t partial.srt full.srt output.srt
package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mozillazg/go-pinyin"
)

// 全局配置变量
var (
	// UserKeywords 存储用户自定义的关键词词典，用于纠错参考
	UserKeywords string
	
	// BlacklistWords 存储黑名单词汇，这些词汇不会被修正
	BlacklistWords string
	
	// ManualFixes 存储手动配置的错误-正确映射表
	ManualFixes = map[string]string{}
)

// ==========================================
// 配置文件处理
// ==========================================

// LoadConfig 从指定路径加载配置文件。
//
// 配置文件支持三个主要配置节：
//   - [UserKeywords]: 用户关键词词典
//   - [BlacklistWords]: 黑名单词汇
//   - [ManualFixes]: 手动修正映射（格式：错误=正确）
//
// 配置文件中以#开头的行会被视为注释，空行会被忽略。
func LoadConfig(configPath string) error {
	file, err := os.Open(configPath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	currentSection := ""
	var keywordsBuilder strings.Builder
	var blacklistBuilder strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 检测配置节
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = line[1 : len(line)-1]
			continue
		}

		// 根据当前配置节处理内容
		switch currentSection {
		case "UserKeywords":
			keywordsBuilder.WriteString(line)
			keywordsBuilder.WriteString(" ")
		case "BlacklistWords":
			blacklistBuilder.WriteString(line)
			blacklistBuilder.WriteString(" ")
		case "ManualFixes":
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				ManualFixes[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	UserKeywords = keywordsBuilder.String()
	BlacklistWords = blacklistBuilder.String()

	return scanner.Err()
}

// GetConfigPath 获取配置文件的路径。
//
// 优先返回与可执行文件同目录下的config.txt路径，
// 如果无法获取可执行文件路径，则返回当前目录下的config.txt。
func GetConfigPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return "config.txt"
	}
	return filepath.Join(filepath.Dir(execPath), "config.txt")
}

// ==========================================
// 核心数据结构
// ==========================================

// SubtitleBlock 表示一个字幕块，包含序号、时间轴和内容。
type SubtitleBlock struct {
	// Index 字幕序号
	Index string
	
	// TimeLine 时间轴信息，格式如：00:00:00,000 --> 00:00:05,000
	TimeLine string
	
	// Content 字幕文本内容
	Content string
}

// Corrector 是字幕纠错器，负责执行智能纠错算法。
//
// 它基于拼音相似度算法，结合用户词典和黑名单，
// 自动识别并修正字幕中的错误文本。
type Corrector struct {
	pyArgsNormal pinyin.Args
	pyArgsTone   pinyin.Args
	dictCache    []dictItem
	validWords   map[string]bool
	blacklist    map[string]bool
}

// dictItem 表示词典中的一个条目，包含词汇及其拼音信息。
type dictItem struct {
	word     string     // 词汇文本
	pyNormal []string   // 无声调拼音
	pyTone   []string   // 带声调拼音
}

// Suggestion 表示一个修正建议，包含原文本、修正后文本及相关信息。
type Suggestion struct {
	Original  string  // 原始错误文本
	Corrected string  // 修正后的文本
	Score     float64 // 相似度评分
	StartIdx  int     // 在输入文本中的起始索引
	EndIdx    int     // 在输入文本中的结束索引
}

// ==========================================
// 初始化
// ==========================================

// NewCorrector 创建一个新的字幕纠错器实例。
//
// 参数：
//   - keywordsStr: 空格分隔的关键词字符串，构成纠错词典
//   - blacklistStr: 空格分隔的黑名单词汇字符串
//
// 返回的纠错器会预处理词典，按词长降序排列以优先匹配长词。
func NewCorrector(keywordsStr, blacklistStr string) *Corrector {
	argsNormal := pinyin.NewArgs()
	argsNormal.Style = pinyin.Normal

	argsTone := pinyin.NewArgs()
	argsTone.Style = pinyin.Tone3

	keywords := strings.Fields(keywordsStr)
	validSet := make(map[string]bool)
	var cache []dictItem

	blackListFields := strings.Fields(blacklistStr)
	blackSet := make(map[string]bool)
	for _, w := range blackListFields {
		blackSet[w] = true
	}

	for _, word := range keywords {
		validSet[word] = true

		if utf8.RuneCountInString(word) < 2 {
			continue
		}

		pyN := pinyin.LazyConvert(word, &argsNormal)

		pyTMatrix := pinyin.Pinyin(word, argsTone)
		var pyT []string
		for _, s := range pyTMatrix {
			if len(s) > 0 {
				pyT = append(pyT, s[0])
			} else {
				pyT = append(pyT, "")
			}
		}

		cache = append(cache, dictItem{
			word:     word,
			pyNormal: pyN,
			pyTone:   pyT,
		})
	}

	sort.Slice(cache, func(i, j int) bool {
		return utf8.RuneCountInString(cache[i].word) > utf8.RuneCountInString(cache[j].word)
	})

	return &Corrector{
		pyArgsNormal: argsNormal,
		pyArgsTone:   argsTone,
		dictCache:    cache,
		validWords:   validSet,
		blacklist:    blackSet,
	}
}

// ==========================================
// 核心算法区
// ==========================================

// GetThreshold 根据词长返回动态相似度阈值。
//
// 阈值用于判断是否接受一个修正建议。词越短要求越严格：
//   - 2字词: 0.95 (几乎要求完美匹配)
//   - 3字词: 0.80
//   - 4字及以上: 0.70
func GetThreshold(wordLen int) float64 {
	switch wordLen {
	case 2:
		return 0.95 // 2字词几乎要求完美匹配
	case 3:
		return 0.80
	default:
		return 0.70
	}
}

// GetEditCost 计算两个拼音音节之间的替换代价。
//
// 返回值范围：
//   - 0.0: 完全相同
//   - 0.2: 模糊音相似（如zh→z, n→l, f→h等）
//   - 1.0: 完全不同
//
// 这个代价值会被用于Levenshtein距离算法中。
func GetEditCost(p1, p2 string) float64 {
	if p1 == p2 {
		return 0.0
	}
	s1 := simplify(p1)
	s2 := simplify(p2)
	if s1 == s2 {
		return 0.2 // 模糊音罚分 0.2
	}
	return 1.0 // 完全不同罚分 1.0
}

// simplify 简化拼音以识别常见的模糊音。
//
// 转换规则：
//   - zh→z, ch→c, sh→s
//   - ang→an, ing→in, eng→en
//   - n→l (前鼻音与边音混淆)
//   - f→h (唇齿音混淆)
func simplify(p string) string {
	p = strings.ReplaceAll(p, "zh", "z")
	p = strings.ReplaceAll(p, "ch", "c")
	p = strings.ReplaceAll(p, "sh", "s")
	p = strings.ReplaceAll(p, "ang", "an")
	p = strings.ReplaceAll(p, "ing", "in")
	p = strings.ReplaceAll(p, "eng", "en")
	if strings.HasPrefix(p, "n") {
		p = "l" + p[1:]
	}
	if strings.HasPrefix(p, "f") {
		p = "h" + p[1:]
	}
	return p
}

// CalculateLevenshteinSimilarity 计算两个拼音序列的加权编辑距离相似度。
//
// 该函数是纠错算法的核心，使用动态规划计算最小编辑距离，
// 并考虑了模糊音的特殊处理。
//
// 返回值范围为 0.0 到 1.0，越接近 1.0 表示越相似。
func CalculateLevenshteinSimilarity(input, target []string) float64 {
	len1 := len(input)
	len2 := len(target)

	// dp[i][j] 表示 input的前i个 和 target的前j个 的最小编辑距离代价
	dp := make([][]float64, len1+1)
	for i := range dp {
		dp[i] = make([]float64, len2+1)
	}

	// 初始化:空字符串变为非空字符串的代价 (全插入/全删除)
	for i := 0; i <= len1; i++ {
		dp[i][0] = float64(i)
	}
	for j := 0; j <= len2; j++ {
		dp[0][j] = float64(j)
	}

	for i := 1; i <= len1; i++ {
		for j := 1; j <= len2; j++ {
			cost := GetEditCost(input[i-1], target[j-1])

			// 状态转移:删除、插入、替换
			deleteCost := dp[i-1][j] + 1.0
			insertCost := dp[i][j-1] + 1.0
			replaceCost := dp[i-1][j-1] + cost

			dp[i][j] = math.Min(deleteCost, math.Min(insertCost, replaceCost))
		}
	}

	editDistance := dp[len1][len2]
	maxLen := math.Max(float64(len1), float64(len2))
	if maxLen == 0 {
		return 0.0
	}

	// 归一化为相似度: 1 - (距离 / 最大长度)
	return 1.0 - (editDistance / maxLen)
}

// CompareTones 比较两个拼音序列的声调匹配度。
//
// 如果有效声调匹配率超过60%，返回0.1作为额外奖励分数，
// 否则返回0.0。该函数用于在相似度基础上提供声调辅助判断。
func CompareTones(t1, t2 []string) float64 {
	if len(t1) != len(t2) {
		return 0.0
	}
	matchCount := 0
	validCount := 0
	for i := 0; i < len(t1) && i < len(t2); i++ {
		if len(t1[i]) > 0 && len(t2[i]) > 0 {
			c1 := t1[i][len(t1[i])-1]
			c2 := t2[i][len(t2[i])-1]
			if c1 >= '0' && c1 <= '5' && c2 >= '0' && c2 <= '5' {
				validCount++
				if c1 == c2 {
					matchCount++
				}
			}
		}
	}
	if validCount > 0 && float64(matchCount)/float64(validCount) > 0.6 {
		return 0.1 // 声调奖励
	}
	return 0.0
}

// ==========================================
// 修正逻辑实现
// ==========================================

// Correct 对给定内容执行智能纠错。
//
// 该方法首先应用手动修正规则，然后使用拼音相似度算法
// 逐行扫描并修正错误。修正过程中会：
//   1. 避免修正正确词汇（词典中存在的）
//   2. 避免修正黑名单词汇
//   3. 使用贪心策略优先匹配长词
//   4. 记录所有修正统计信息
//
// 参数：
//   - content: 待修正的文本内容
//   - stats: 用于记录修正统计的map，键为"原文->修正文"格式
//
// 返回修正后的文本。
func (c *Corrector) Correct(content string, stats map[string]int) string {
	if content == "" {
		return ""
	}

	// 首先应用手动修正规则
	for wrong, right := range ManualFixes {
		if strings.Contains(content, wrong) {
			count := strings.Count(content, wrong)
			content = strings.ReplaceAll(content, wrong, right)
			key := fmt.Sprintf("%s -> %s", wrong, right)
			stats[key] += count
		}
	}

	lines := strings.Split(content, "\n")
	var resultLines []string

	for _, line := range lines {
		inputRunes := []rune(line)
		inputPyNormal := pinyin.LazyConvert(line, &c.pyArgsNormal)

		inputPyToneMatrix := pinyin.Pinyin(line, c.pyArgsTone)
		var inputPyTone []string
		for _, s := range inputPyToneMatrix {
			if len(s) > 0 {
				inputPyTone = append(inputPyTone, s[0])
			} else {
				inputPyTone = append(inputPyTone, "")
			}
		}

		if len(inputRunes) != len(inputPyNormal) {
			resultLines = append(resultLines, line)
			continue
		}

		mask := make([]bool, len(inputRunes))
		var suggestions []Suggestion

		for _, item := range c.dictCache {
			wordLen := len(item.pyNormal)
			threshold := GetThreshold(wordLen)

			for i := 0; i <= len(inputPyNormal)-wordLen; i++ {
				collision := false
				for k := 0; k < wordLen; k++ {
					if mask[i+k] {
						collision = true
						break
					}
				}
				if collision {
					continue
				}

				// 提取拼音片段
				windowPyNormal := inputPyNormal[i : i+wordLen]

				// 使用 Levenshtein 计算相似度
				simScore := CalculateLevenshteinSimilarity(windowPyNormal, item.pyNormal)

				originalText := string(inputRunes[i : i+wordLen])

				// 正身保护 & 黑名单
				if c.validWords[originalText] || c.blacklist[originalText] {
					continue
				}

				// 声调辅助
				if simScore >= threshold {
					windowPyTone := inputPyTone[i : i+wordLen]
					simScore += CompareTones(windowPyTone, item.pyTone)
				}

				if simScore >= threshold {
					if originalText != item.word {
						suggestions = append(suggestions, Suggestion{
							Original:  originalText,
							Corrected: item.word,
							Score:     simScore,
							StartIdx:  i,
							EndIdx:    i + wordLen,
						})
						for k := 0; k < wordLen; k++ {
							mask[i+k] = true
						}
					}
				}
			}
		}

		sort.Slice(suggestions, func(i, j int) bool {
			return suggestions[i].StartIdx < suggestions[j].StartIdx
		})

		var sb strings.Builder
		cursor := 0
		for _, s := range suggestions {
			sb.WriteString(string(inputRunes[cursor:s.StartIdx]))
			sb.WriteString(s.Corrected)
			cursor = s.EndIdx

			key := fmt.Sprintf("%s -> %s", s.Original, s.Corrected)
			stats[key]++
		}
		sb.WriteString(string(inputRunes[cursor:]))
		resultLines = append(resultLines, sb.String())
	}

	return strings.Join(resultLines, "\n")
}

// ==========================================
// 文件操作
// ==========================================

// RemoveBOM 移除UTF-8 BOM标记。
//
// 某些文本编辑器会在UTF-8文件开头添加BOM标记（EF BB BF），
// 该函数检测并移除这些字节。
func RemoveBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// ParseSRT 解析标准格式的SRT字幕文件。
//
// SRT格式说明：
//   - 每个字幕块由序号、时间轴和内容组成
//   - 字幕块之间用空行分隔
//   - 时间轴格式：00:00:00,000 --> 00:00:05,000
//
// 函数会自动处理BOM和不同的换行符（\r\n, \r, \n）。
func ParseSRT(filePath string) ([]*SubtitleBlock, error) {
	rawBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	content := string(RemoveBOM(rawBytes))
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	rawBlocks := strings.Split(content, "\n\n")
	var blocks []*SubtitleBlock
	for _, rb := range rawBlocks {
		rb = strings.TrimSpace(rb)
		if rb == "" {
			continue
		}
		lines := strings.Split(rb, "\n")
		if len(lines) < 2 {
			continue
		}
		block := &SubtitleBlock{}
		timeLineIdx := -1
		for i, line := range lines {
			if strings.Contains(line, "-->") {
				timeLineIdx = i
				break
			}
		}
		if timeLineIdx != -1 {
			block.TimeLine = lines[timeLineIdx]
			if timeLineIdx > 0 {
				block.Index = lines[timeLineIdx-1]
			} else {
				block.Index = "0"
			}
			if timeLineIdx+1 < len(lines) {
				block.Content = strings.Join(lines[timeLineIdx+1:], "\n")
			}
			blocks = append(blocks, block)
		}
	}
	return blocks, nil
}

// ParsePartialSRT 解析带方括号序号格式的部分字幕文件。
//
// 该格式用于仅包含部分字幕的文件，格式为：
//   [序号] 时间轴
//   内容行1
//   内容行2
//   ...
//
// 例如：
//   [1] 00:00:00,260 --> 00:00:04,510
//   这是第一行字幕
//   这是第二行字幕
func ParsePartialSRT(filePath string) ([]*SubtitleBlock, error) {
	rawBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	content := string(RemoveBOM(rawBytes))
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	var blocks []*SubtitleBlock
	
	// 正则表达式匹配 [序号] 格式
	// 例如: [1] 00:00:00,260 --> 00:00:04,510
	blockPattern := regexp.MustCompile(`(?m)^\[(\d+)\]\s+(.+?-->.+?)$`)
	
	lines := strings.Split(content, "\n")
	i := 0
	
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		
		// 匹配 [序号] 时间轴 格式
		matches := blockPattern.FindStringSubmatch(line)
		if len(matches) >= 3 {
			block := &SubtitleBlock{
				Index:    matches[1],
				TimeLine: matches[2],
			}
			
			// 收集后续的内容行（直到遇到下一个 [序号] 或文件结束）
			i++
			var contentLines []string
			for i < len(lines) {
				nextLine := strings.TrimSpace(lines[i])
				// 如果遇到下一个块的开始或空行，停止
				if blockPattern.MatchString(nextLine) {
					break
				}
				if nextLine != "" {
					contentLines = append(contentLines, nextLine)
				}
				i++
			}
			
			if len(contentLines) > 0 {
				block.Content = strings.Join(contentLines, "\n")
			}
			
			blocks = append(blocks, block)
		} else {
			i++
		}
	}
	
	return blocks, nil
}

// WriteSRT 将字幕块写入SRT格式文件。
//
// 如果字幕块的序号为空或为"0"，会自动生成递增的序号。
func WriteSRT(filePath string, blocks []*SubtitleBlock) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for i, block := range blocks {
		idx := block.Index
		if idx == "" || idx == "0" {
			idx = fmt.Sprintf("%d", i+1)
		}
		fmt.Fprintln(writer, idx)
		fmt.Fprintln(writer, block.TimeLine)
		fmt.Fprintln(writer, block.Content)
		fmt.Fprintln(writer)
	}
	return writer.Flush()
}

// ==========================================
// 字幕转移功能
// ==========================================

// TransferSubtitles 将部分字幕的内容应用到完整版SRT文件中。
//
// 该功能用于以下场景：
//   - 已有完整的字幕文件（含所有时间轴）
//   - 手动修正了部分字幕的内容
//   - 希望将修正后的内容合并回完整版
//
// 参数：
//   - partialFile: 部分字幕文件路径（使用[序号]格式）
//   - fullFile: 完整字幕文件路径（标准SRT格式）
//   - outputFile: 输出文件路径
//
// 函数会根据序号匹配，自动替换对应位置的字幕内容。
func TransferSubtitles(partialFile, fullFile, outputFile string) error {
	fmt.Printf(">> 读取部分字幕文件: %s\n", partialFile)
	partialBlocks, err := ParsePartialSRT(partialFile)
	if err != nil {
		return fmt.Errorf("读取部分字幕文件失败: %v", err)
	}

	fmt.Printf(">> 读取完整字幕文件: %s\n", fullFile)
	fullBlocks, err := ParseSRT(fullFile)
	if err != nil {
		return fmt.Errorf("读取完整字幕文件失败: %v", err)
	}

	// 构建部分字幕的索引映射
	partialMap := make(map[int]*SubtitleBlock)
	for _, block := range partialBlocks {
		// 解析序号
		idx, err := strconv.Atoi(strings.TrimSpace(block.Index))
		if err != nil {
			fmt.Printf("警告: 无法解析序号 '%s', 跳过\n", block.Index)
			continue
		}
		partialMap[idx] = block
	}

	fmt.Printf(">> 正在转移字幕内容...")
	transferCount := 0

	// 将部分字幕应用到完整版
	for i, fullBlock := range fullBlocks {
		// 解析完整版字幕的序号
		idx, err := strconv.Atoi(strings.TrimSpace(fullBlock.Index))
		if err != nil {
			idx = i + 1 // 如果解析失败，使用位置索引
		}

		// 检查是否有对应的部分字幕
		if partialBlock, exists := partialMap[idx]; exists {
			fullBlocks[i].Content = partialBlock.Content
			transferCount++
		}
	}

	fmt.Printf("共转移了 %d 个字幕块\n", transferCount)

	fmt.Printf(">> 写入输出文件: %s\n", outputFile)
	err = WriteSRT(outputFile, fullBlocks)
	if err != nil {
		return fmt.Errorf("写入输出文件失败: %v", err)
	}

	return nil
}

// ==========================================
// 主程序入口
// ==========================================

// printUsage 打印程序使用说明。
func printUsage() {
	fmt.Println("字幕处理工具 - 使用说明")
	fmt.Println("====================")
	fmt.Println()
	fmt.Println("功能 1: 智能修正字幕 (使用 -i 参数)")
	fmt.Println("  用法: ime -i <输入文件.srt> <输出文件.srt>")
	fmt.Println("  示例: ime -i input.srt output.srt")
	fmt.Println("  说明: 根据配置文件自动修正字幕中的错误")
	fmt.Println()
	fmt.Println("功能 2: 转移字幕内容 (使用 -t 参数)")
	fmt.Println("  用法: ime -t <部分字幕.srt> <完整字幕.srt> <输出文件.srt>")
	fmt.Println("  示例: ime -t partial.srt full.srt output.srt")
	fmt.Println("  说明: 将部分字幕文件的内容应用到完整字幕文件中")
	fmt.Println("        支持 [序号] 格式的部分字幕文件")
	fmt.Println("        根据序号匹配,自动替换对应位置的字幕内容")
	fmt.Println()
}

// main 是程序的主入口函数。
//
// 支持两种运行模式：
//   1. -i 模式：智能修正字幕
//   2. -t 模式：转移字幕内容
//
// 无参数运行时显示帮助信息。
func main() {
	// 无参数时显示帮助
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	mode := os.Args[1]

	switch mode {
	case "-i":
		// 智能修正模式
		if len(os.Args) < 4 {
			fmt.Println("错误: -i 模式需要 2 个参数")
			fmt.Println("用法: ime -i <输入文件.srt> <输出文件.srt>")
			return
		}

		// 加载配置文件
		configPath := GetConfigPath()
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			configPath = "config.txt"
		}

		if err := LoadConfig(configPath); err != nil {
			fmt.Printf("警告: 无法加载配置文件 %s: %v\n", configPath, err)
			fmt.Println("请确保 config.txt 存在于程序目录或当前目录")
			return
		}
		fmt.Printf(">> 已加载配置: %s\n", configPath)

		inputFile := os.Args[2]
		outputFile := os.Args[3]
		corrector := NewCorrector(UserKeywords, BlacklistWords)
		
		fmt.Printf(">> 读取文件: %s\n", inputFile)
		blocks, err := ParseSRT(inputFile)
		if err != nil {
			fmt.Printf("错误: %v\n", err)
			return
		}
		
		stats := make(map[string]int)
		fmt.Printf(">> 正在执行修正...")
		changeCount := 0
		for i, b := range blocks {
			newContent := corrector.Correct(b.Content, stats)
			if newContent != b.Content {
				blocks[i].Content = newContent
				changeCount++
			}
		}
		
		fmt.Printf("共修改了 %d 个字幕块\n", changeCount)
		
		fmt.Printf(">> 写入输出文件: %s\n", outputFile)
		err = WriteSRT(outputFile, blocks)
		if err != nil {
			fmt.Printf("写入错误: %v\n", err)
			return
		}

	case "-t":
		// 转移字幕模式
		if len(os.Args) < 5 {
			fmt.Println("错误: -t 模式需要 3 个参数")
			fmt.Println("用法: ime -t <部分字幕.srt> <完整字幕.srt> <输出文件.srt>")
			return
		}

		partialFile := os.Args[2]
		fullFile := os.Args[3]
		outputFile := os.Args[4]

		err := TransferSubtitles(partialFile, fullFile, outputFile)
		if err != nil {
			fmt.Printf("错误: %v\n", err)
			return
		}

	default:
		fmt.Printf("错误: 未知的参数 '%s'\n\n", mode)
		printUsage()
	}
}
