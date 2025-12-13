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

var UserKeywords string
var BlacklistWords string
var ManualFixes = map[string]string{}

// LoadConfig 从配置文件加载配置
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

// GetConfigPath 获取配置文件路径(与可执行文件同目录)
func GetConfigPath() string {
	execPath, err := os.Executable()
	if err != nil {
		return "config.txt"
	}
	return filepath.Join(filepath.Dir(execPath), "config.txt")
}

// ==========================================
// 2. 核心结构
// ==========================================

type SubtitleBlock struct {
	Index    string
	TimeLine string
	Content  string
}

type Corrector struct {
	pyArgsNormal pinyin.Args
	pyArgsTone   pinyin.Args
	dictCache    []dictItem
	validWords   map[string]bool
	blacklist    map[string]bool
}

type dictItem struct {
	word     string
	pyNormal []string
	pyTone   []string
}

type Suggestion struct {
	Original  string
	Corrected string
	Score     float64
	StartIdx  int
	EndIdx    int
}

// ==========================================
// 3. 初始化
// ==========================================

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
// 4. 核心算法区 (算法升级)
// ==========================================

// GetThreshold 动态阈值 (针对编辑距离归一化后的分数)
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

// GetEditCost 计算两个拼音音节之间的替换代价 (Levenshtein Cost)
// 返回值越小越相似:0=完全一样,0.2=模糊音,1.0=完全不同
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

// CalculateLevenshteinSimilarity [算法核心] 加权拼音编辑距离
// 返回 0.0 ~ 1.0 的相似度
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
// 5. 修正逻辑实现
// ==========================================

func (c *Corrector) Correct(content string, stats map[string]int) string {
	if content == "" {
		return ""
	}

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

				// [关键更新] 使用 Levenshtein 计算相似度
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
// 6. 文件操作
// ==========================================

func RemoveBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

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

// ParsePartialSRT 解析带方括号序号的字幕文件 [序号] 时间轴
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
// 7. 新功能：转移字幕
// ==========================================

// TransferSubtitles 将部分字幕应用到完整版SRT
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
// 8. 主程序入口
// ==========================================

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
