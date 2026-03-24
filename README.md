# IME - SRT Intelligent Correction Tool

## Purpose

A specialized tool developed for video editing workflows, primarily solving pinyin input error problems in SRT subtitle files. During video editing, subtitles are often generated through speech recognition or manual input, frequently containing pinyin errors such as "泰尔" instead of "太傻", "私密度" instead of "四密度". These errors affect subtitle professionalism and readability if not corrected.

**Core value:**
- **Automated error correction**: Automatically detect and correct common pinyin errors based on predefined keyword lists
- **Intelligent filtering**: Avoid unnecessary modifications to specific technical terms through blacklist mechanism
- **Flexible configuration**: Support manual addition of replacement rules for different content needs
- **Batch processing**: Quickly process entire SRT files to improve work efficiency

Particularly suitable for processing subtitle content involving domain-specific vocabulary such as philosophy, spirituality, and science.

## Features

- **Pinyin correction**: Automatically detect and correct pinyin errors based on keyword lists
- **Blacklist filtering**: Avoid unnecessary corrections to specific words
- **Manual replacement rules**: Support custom error-to-correct word mappings
- **SRT file processing**: Specifically designed for SRT subtitle file format, preserving timestamps and formatting
- **Command-line interface**: Simple and easy-to-use CLI, supporting batch processing

## Installation

### Requirements

- Go 1.25.4 or higher
- macOS system (Homebrew support)

### Build and Install

1. Ensure Go language environment is installed
2. Clone or download project to local directory
3. Enter project directory: `cd ime`
4. Run build command: `make build`
5. Install to system path: `make install`

After installation, `ime` command will be available in system PATH.

### Uninstall

If needed: `make uninstall`

## Usage

### Basic Usage

The tool supports two main feature modes:

#### Feature 1: Intelligent Subtitle Correction (-i parameter)

Core feature for automatic correction of pinyin errors in SRT subtitle files.

**Usage:**
```bash
ime -i <input.srt> <output.srt>
```

**Example:**
```bash
ime -i input.srt corrected_output.srt
```

**Workflow:**
1. Automatically read `config.txt` configuration file from same directory
2. Parse input SRT file
3. Intelligently correct each subtitle block content:
   - First apply manual replacement rules (ManualFixes)
   - Then perform pinyin similarity matching and automatic error correction
4. Output corrected subtitle file
5. Display correction statistics (how many subtitle blocks modified, specific replacements)

**Output Example:**
```
>> Config loaded: config.txt
>> Reading file: input.srt
>> Executing correction...Modified 5 subtitle blocks
>> Writing output file: corrected_output.srt

Correction Statistics:
泰尔 -> 太傻: 2 times
私密度 -> 四密度: 3 times
```

#### Feature 2: Transfer Subtitle Content (-t parameter)

Apply partial subtitle file content to full subtitle file, commonly used in subtitle proofreading workflow.

**Usage:**
```bash
ime -t <partial.srt> <full.srt> <output.srt>
```

**Example:**
```bash
ime -t partial_corrections.srt full_subtitle.srt final_output.srt
```

**Partial subtitle file format:**
Partial subtitle files use special format starting with `[index]`:
```
[1] 00:00:00,260 --> 00:00:04,510
Corrected first subtitle content

[5] 00:00:20,150 --> 00:00:24,890
Corrected fifth subtitle content
```

**Workflow:**
1. Read partial subtitle file (with index format)
2. Read full subtitle file
3. Match by index and replace partial subtitle content to corresponding positions in full subtitles
4. Generate final output file
5. Display transfer statistics

**Output Example:**
```
>> Reading partial subtitle file: partial.srt
>> Reading full subtitle file: full.srt
>> Transferring subtitle content...Transferred 3 subtitle blocks
>> Writing output file: output.srt
```

## Configuration

`config.txt` is the core configuration file:

```
# SRT Intelligent Correction Tool Configuration

[UserKeywords]
太傻天书 无限维创 意识密度 九维宇宙
四密度 复合叠加 波叠加 问答环节
太傻 密度 维度 意识 魔法

[BlacklistWords]
第一密度 第二密度 第三密度 第四密度

[ManualFixes]
泰尔=太傻
私密度=四密度
多四密度=第四密度
```

### Sections

- **[UserKeywords]**: Keyword list for pinyin matching and error correction
- **[BlacklistWords]**: Blacklist words that won't be corrected (protects technical terms)
- **[ManualFixes]**: Manual replacement rules (format: error=correct)

## Notes

- **Backup importance**: Tool automatically backs up original files
- **Format preservation**: SRT file timestamps and structure are fully preserved
- **Manual review**: Although algorithm is intelligent, manual verification of correction results is recommended
- **Configuration updates**: Continuously improve rules in config.txt based on usage
- **Encoding support**: Supports UTF-8 encoded SRT files, automatically handles BOM

## Development

### Local Development

```bash
make build    # Build
make run      # Run tests
make clean    # Clean
```

## License

MIT
